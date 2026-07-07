// Command markerplayback pulls marker-placement tabular readings from Viam's
// TabularDataByMQL API for a single part/component and writes them out in the
// flat shape the placement-playback viewer expects.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	datapb "go.viam.com/api/app/data/v1"
	apppb "go.viam.com/api/app/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"synthetic-sonar-eval/internal/sonar"
)

const viamEndpoint = "app.viam.com:443"

// maxQueryWindow caps how much history a single run can pull, since the
// sonar/image resources here can hold vastly more data than is practical to
// render in one go (some sensors have 250k+ pings across just a few days).
const maxQueryWindow = 3 * 24 * time.Hour

// Reading is the flat shape consumed by the placement-playback viewer.
type Reading struct {
	Depth       float64 `json:"depth"`
	IsSynthetic bool    `json:"is_synthetic"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	MarkerID    string  `json:"marker_id"`
	TS          int64   `json:"ts"`
}

// ImageFrame is a single camera capture, embedded as base64 so the viewer can
// load everything from the one dropped JSON file.
type ImageFrame struct {
	TS         int64  `json:"ts"`
	MimeType   string `json:"mimeType"`
	DataBase64 string `json:"dataBase64"`
}

// SonarFrame is a single sonar ping, rendered to a heatmap PNG via
// internal/sonar and embedded as base64 alongside the camera frames.
type SonarFrame struct {
	SensorName string `json:"sensorName"`
	TS         int64  `json:"ts"`
	MimeType   string `json:"mimeType"`
	DataBase64 string `json:"dataBase64"`
}

// buildPipeline mirrors the requested MQL shape: a single $match stage on the
// resource identity fields, plus an optional $expr time_received bound.
func buildPipeline(partID, start, end string) []map[string]interface{} {
	match := map[string]interface{}{
		"part_id":        partID,
		"component_name": "placemarker-synth-ai",
		"component_type": "rdk:component:sensor",
		"method_name":    "Readings",
	}

	var bounds []interface{}
	if start != "" {
		bounds = append(bounds, map[string]interface{}{
			"$gte": []interface{}{"$time_received", map[string]interface{}{"$toDate": start}},
		})
	}
	if end != "" {
		bounds = append(bounds, map[string]interface{}{
			"$lte": []interface{}{"$time_received", map[string]interface{}{"$toDate": end}},
		})
	}
	switch len(bounds) {
	case 0:
	case 1:
		match["$expr"] = bounds[0]
	default:
		match["$expr"] = map[string]interface{}{"$and": bounds}
	}

	return []map[string]interface{}{{"$match": match}}
}

func queryToBinary(pipeline []map[string]interface{}) ([][]byte, error) {
	binary := make([][]byte, 0, len(pipeline))
	for _, stage := range pipeline {
		b, err := bson.Marshal(stage)
		if err != nil {
			return nil, fmt.Errorf("marshal stage: %w", err)
		}
		binary = append(binary, b)
	}
	return binary, nil
}

// extractReading pulls the {depth, is_synthetic, latitude, longitude,
// marker_id, ts} reading out of a raw TabularDataByMQL document. The reading
// is expected under data.readings (matching the payload.readings shape
// already used by the local tabular dumps in output/tabular), falling back
// to data directly in case the sensor reports it unwrapped.
func extractReading(doc map[string]interface{}, fallbackIndex int) (Reading, bool) {
	data, _ := doc["data"].(map[string]interface{})
	values := data
	if nested, ok := data["readings"].(map[string]interface{}); ok {
		values = nested
	}

	lat, latOK := toFloat(values["latitude"])
	lon, lonOK := toFloat(values["longitude"])
	if !latOK || !lonOK {
		return Reading{}, false
	}

	r := Reading{
		Depth:       mustFloat(values["depth"]),
		IsSynthetic: toBool(values["is_synthetic"]),
		Latitude:    lat,
		Longitude:   lon,
		MarkerID:    toString(values["marker_id"]),
	}

	if ts, ok := toFloat(values["ts"]); ok {
		r.TS = int64(ts)
	} else if received, ok := doc["time_received"].(primitive.DateTime); ok {
		r.TS = int64(received.Time().UnixMilli())
	}

	if r.MarkerID == "" {
		r.MarkerID = fmt.Sprintf("reading-%d", fallbackIndex)
	}

	return r, true
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func mustFloat(v interface{}) float64 {
	f, _ := toFloat(v)
	return f
}

func toBool(v interface{}) bool {
	b, _ := v.(bool)
	return b
}

func toString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func sanitizeName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(name)
}

func sanitizeTimestamp(ts int64) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(time.UnixMilli(ts).UTC().Format(time.RFC3339Nano))
}

func parseRFC3339(s string) (*timestamppb.Timestamp, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("parse time %q: %w", s, err)
	}
	return timestamppb.New(t), nil
}

func mimeTypeFromExt(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "jpg", "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// binaryDataIDBatchSize caps how many IDs go into a single BinaryDataByIDs
// call when downloading actual image bytes.
const binaryDataIDBatchSize = 10

// countImages returns the total number of documents matching filter,
// independent of pagination.
func countImages(ctx context.Context, client datapb.DataServiceClient, filter *datapb.Filter) (uint64, error) {
	resp, err := client.BinaryDataByFilter(ctx, &datapb.BinaryDataByFilterRequest{
		DataRequest: &datapb.DataRequest{Filter: filter},
		CountOnly:   true,
	})
	if err != nil {
		return 0, err
	}
	return resp.Count, nil
}

// fetchImageMetadata paginates BinaryDataByFilter (IncludeBinary: false) to
// enumerate matching captures' metadata, without downloading bytes, stopping
// once maxResults have been collected (0 = unlimited). The API forces
// limit=1 whenever include_binary is true, so metadata listing and byte
// downloading are done as two separate phases. SortOrder must be set to
// ASCENDING or DESCENDING for multi-page pagination — leaving it unspecified
// (the zero value) reliably 500s server-side past the first page.
func fetchImageMetadata(
	ctx context.Context, client datapb.DataServiceClient, filter *datapb.Filter, pageSize, maxResults uint64,
) ([]*datapb.BinaryData, error) {
	var out []*datapb.BinaryData
	last := ""
	for {
		resp, err := client.BinaryDataByFilter(ctx, &datapb.BinaryDataByFilterRequest{
			DataRequest: &datapb.DataRequest{
				Filter:    filter,
				Limit:     pageSize,
				Last:      last,
				SortOrder: datapb.Order_ORDER_DESCENDING,
			},
			IncludeBinary: false,
		})
		if err != nil {
			return nil, err
		}
		if len(resp.Data) == 0 {
			break
		}
		out = append(out, resp.Data...)
		if maxResults > 0 && uint64(len(out)) >= maxResults {
			out = out[:maxResults]
			break
		}
		if resp.Last == "" || uint64(len(resp.Data)) < pageSize {
			break
		}
		last = resp.Last
	}
	return out, nil
}

// fetchImages lists matching camera captures, then downloads their bytes via
// BinaryDataByIDs in small batches (BinaryDataByFilter can't return bytes for
// more than one document per call).
func fetchImages(
	ctx context.Context, client datapb.DataServiceClient, filter *datapb.Filter, pageSize, maxResults uint64,
) ([]ImageFrame, error) {
	metas, err := fetchImageMetadata(ctx, client, filter, pageSize, maxResults)
	if err != nil {
		return nil, err
	}

	frames := make([]ImageFrame, 0, len(metas))
	for i := 0; i < len(metas); i += binaryDataIDBatchSize {
		end := min(i+binaryDataIDBatchSize, len(metas))
		ids := make([]string, 0, end-i)
		for _, m := range metas[i:end] {
			ids = append(ids, m.Metadata.BinaryDataId)
		}

		resp, err := client.BinaryDataByIDs(ctx, &datapb.BinaryDataByIDsRequest{
			IncludeBinary: true,
			BinaryDataIds: ids,
		})
		if err != nil {
			return nil, fmt.Errorf("BinaryDataByIDs: %w", err)
		}
		for _, d := range resp.Data {
			meta := d.Metadata
			mimeType := ""
			if meta.CaptureMetadata != nil {
				mimeType = meta.CaptureMetadata.MimeType
			}
			if mimeType == "" {
				mimeType = mimeTypeFromExt(meta.FileExt)
			}
			frames = append(frames, ImageFrame{
				TS:         meta.GetTimeRequested().AsTime().UnixMilli(),
				MimeType:   mimeType,
				DataBase64: base64.StdEncoding.EncodeToString(d.Binary),
			})
		}
	}

	sort.Slice(frames, func(i, j int) bool { return frames[i].TS < frames[j].TS })
	return frames, nil
}

// extractFanSampleGrid pulls the sonar ping payload out of a raw
// TabularDataByMQL document (expected under data.readings, matching the
// payload.readings shape used by the local tabular dumps in output/tabular)
// via a JSON round-trip, since its field names already match FanSampleGrid's
// json tags exactly.
func extractFanSampleGrid(doc map[string]interface{}) (*sonar.FanSampleGrid, bool) {
	data, _ := doc["data"].(map[string]interface{})
	values, ok := data["readings"].(map[string]interface{})
	if !ok {
		values = data
	}
	if values == nil {
		return nil, false
	}

	raw, err := json.Marshal(values)
	if err != nil {
		return nil, false
	}
	var grid sonar.FanSampleGrid
	if err := json.Unmarshal(raw, &grid); err != nil {
		return nil, false
	}
	if grid.NBeams == 0 || grid.NSamples == 0 {
		return nil, false
	}
	return &grid, true
}

// renderOneSonarDoc decodes a single TabularDataByMQL raw document into a
// FanSampleGrid and renders it, returning ok=false (not an error) for
// documents that can't be turned into a frame.
func renderOneSonarDoc(raw []byte, sensorName string, size int) (SonarFrame, bool) {
	var doc map[string]interface{}
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return SonarFrame{}, false
	}
	grid, ok := extractFanSampleGrid(doc)
	if !ok {
		return SonarFrame{}, false
	}
	received, ok := doc["time_received"].(primitive.DateTime)
	if !ok {
		return SonarFrame{}, false
	}
	img, err := sonar.RenderFanSampleGrid(grid, size, nil)
	if err != nil {
		return SonarFrame{}, false
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return SonarFrame{}, false
	}
	return SonarFrame{
		SensorName: sensorName,
		TS:         received.Time().UnixMilli(),
		MimeType:   "image/png",
		DataBase64: base64.StdEncoding.EncodeToString(buf.Bytes()),
	}, true
}

// resolveRobotAndLocation looks up the robot and location IDs that own
// partID via GetRobotPart. Required for buildCaptureDayPipeline's $match,
// which mirrors the exact query shape confirmed fast (~1.3s) against this
// dataset's capture_day index, in cmd/mqlquery.
func resolveRobotAndLocation(ctx context.Context, client apppb.AppServiceClient, partID string) (robotID, locationID string, err error) {
	resp, err := client.GetRobotPart(ctx, &apppb.GetRobotPartRequest{Id: partID})
	if err != nil {
		return "", "", fmt.Errorf("GetRobotPart: %w", err)
	}
	return resp.Part.Robot, resp.Part.LocationId, nil
}

// buildCaptureDayPipeline matches on capture_day (an indexed day-bucket
// field) instead of a time_received range via $expr, which isn't
// index-backed and can time out once a sensor has more than a few thousand
// matching documents (some sonar sensors here have 250k+ pings across just a
// few days).
func buildCaptureDayPipeline(
	locationID, robotID, partID, componentName, componentType, methodName string, day time.Time,
) []map[string]interface{} {
	match := map[string]interface{}{
		"location_id":    locationID,
		"robot_id":       robotID,
		"part_id":        partID,
		"component_name": componentName,
		"component_type": componentType,
		"method_name":    methodName,
		"capture_day":    day,
	}
	pipeline := []map[string]interface{}{{"$match": match}}
	return pipeline
}

// fetchSonarFrames pulls a handful of pings per sensor per calendar day
// across [start, end] via buildCaptureDayPipeline, rendering each to a
// heatmap PNG.
func fetchSonarFrames(
	ctx context.Context, client datapb.DataServiceClient, orgID, locationID, robotID, partID string,
	sensorNames []string, componentType, methodName string, start, end string, size int,
) ([]SonarFrame, error) {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return nil, fmt.Errorf("parse start: %w", err)
	}
	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return nil, fmt.Errorf("parse end: %w", err)
	}

	startDay := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(endTime.Year(), endTime.Month(), endTime.Day(), 0, 0, 0, 0, time.UTC)
	var days []time.Time
	for d := startDay; !d.After(endDay); d = d.AddDate(0, 0, 1) {
		days = append(days, d)
	}

	var frames []SonarFrame
	for _, sensorName := range sensorNames {
		found, failed := 0, 0
		for _, day := range days {
			pipeline := buildCaptureDayPipeline(locationID, robotID, partID, sensorName, componentType, methodName, day)
			mqlBinary, err := queryToBinary(pipeline)
			if err != nil {
				return nil, fmt.Errorf("%s: build query: %w", sensorName, err)
			}

			// A day can still time out server-side on rare occasions — treat that as a
			// soft failure (skip the day) rather than aborting the whole run over one
			// slow query.
			resp, err := client.TabularDataByMQL(ctx, &datapb.TabularDataByMQLRequest{
				OrganizationId: orgID,
				MqlBinary:      mqlBinary,
			})
			if err != nil {
				log.Printf("error: %s: TabularDataByMQL: %v", sensorName, err)
				failed++
				continue
			}

			for _, raw := range resp.RawData {
				frame, ok := renderOneSonarDoc(raw, sensorName, size)
				if !ok {
					continue
				}
				frames = append(frames, frame)
				found++
			}
		}
		if failed > 0 {
			log.Printf("warning: %s: %d/%d day quer(y/ies) failed (timeout or error) and were skipped", sensorName, failed, len(days))
		}
		fmt.Printf("%s: rendered %d frame(s) across %d day(s)\n", sensorName, found, len(days))
	}

	sort.Slice(frames, func(i, j int) bool { return frames[i].TS < frames[j].TS })
	return frames, nil
}

func main() {
	_ = godotenv.Load()

	partID := flag.String("part-id", "", "part ID to pull marker readings for (required)")
	orgID := flag.String("org-id", os.Getenv("VIAM_ORG_ID"), "organization ID (or set VIAM_ORG_ID in .env)")
	// Default deliberately omitted from the flag registration (and thus from --help output) so a
	// live token never gets echoed to the terminal; VIAM_AUTH_TOKEN is applied as a fallback below.
	authToken := flag.String("token", "", "auth token (or set VIAM_AUTH_TOKEN in .env)")
	start := flag.String("start", "", "only include readings at/after this RFC3339 time_received (required)")
	end := flag.String("end", "", "only include readings at/before this RFC3339 time_received (required)")
	imagePageSize := flag.Uint("image-page-size", 50, "page size for image pagination")
	outputDir := flag.String("output", "output", "output directory")
	flag.Parse()

	if *authToken == "" {
		*authToken = os.Getenv("VIAM_AUTH_TOKEN")
	}

	if *partID == "" {
		fmt.Fprintln(os.Stderr, "error: --part-id is required")
		flag.Usage()
		os.Exit(1)
	}
	if *orgID == "" {
		fmt.Fprintln(os.Stderr, "error: organization ID required (set VIAM_ORG_ID in .env or use --org-id)")
		os.Exit(1)
	}
	if *authToken == "" {
		fmt.Fprintln(os.Stderr, "error: auth token required (set VIAM_AUTH_TOKEN in .env or use --token)")
		os.Exit(1)
	}
	if *start == "" {
		fmt.Fprintln(os.Stderr, "error: --start is required")
		os.Exit(1)
	}
	if *end == "" {
		fmt.Fprintln(os.Stderr, "error: --end is required")
		os.Exit(1)
	}
	startTime, err := time.Parse(time.RFC3339, *start)
	if err != nil {
		log.Fatalf("parse --start: %v", err)
	}
	endTime, err := time.Parse(time.RFC3339, *end)
	if err != nil {
		log.Fatalf("parse --end: %v", err)
	}
	if endTime.Before(startTime) {
		fmt.Fprintln(os.Stderr, "error: --end must not be before --start")
		os.Exit(1)
	}
	if endTime.Sub(startTime) > maxQueryWindow {
		fmt.Fprintf(os.Stderr, "error: --start/--end window must be at most %s (got %s)\n", maxQueryWindow, endTime.Sub(startTime))
		os.Exit(1)
	}

	pipeline := buildPipeline(*partID, *start, *end)
	mqlBinary, err := queryToBinary(pipeline)
	if err != nil {
		log.Fatalf("build query: %v", err)
	}

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+*authToken)

	creds := credentials.NewClientTLSFromCert(nil, "")
	conn, err := grpc.NewClient(viamEndpoint, grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32*1024*1024)))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	client := datapb.NewDataServiceClient(conn)
	resp, err := client.TabularDataByMQL(ctx, &datapb.TabularDataByMQLRequest{
		OrganizationId: *orgID,
		MqlBinary:      mqlBinary,
	})
	if err != nil {
		log.Fatalf("TabularDataByMQL: %v", err)
	}
	fmt.Printf("matched %d documents for synthetic and screen marker placements\n", len(resp.RawData))

	// 1. get the TabularDataByMQL for silent mode markers placed (screen based and synthetic)
	readings := make([]Reading, 0, len(resp.RawData))
	skipped := 0
	for i, raw := range resp.RawData {
		var doc map[string]interface{}
		if err := bson.Unmarshal(raw, &doc); err != nil {
			log.Fatalf("decode document %d: %v", i, err)
		}
		reading, ok := extractReading(doc, i)
		if !ok {
			skipped++
			continue
		}
		readings = append(readings, reading)
	}
	if skipped > 0 {
		log.Printf("warning: skipped %d document(s) missing latitude/longitude", skipped)
	}

	dir := filepath.Join(*outputDir, "marker-playback", sanitizeName(*partID))
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", dir, err)
	}

	var images []ImageFrame
	startTS, err := parseRFC3339(*start)
	if err != nil {
		log.Fatalf("start: %v", err)
	}
	endTS, err := parseRFC3339(*end)
	if err != nil {
		log.Fatalf("end: %v", err)
	}

	imageFilter := &datapb.Filter{
		PartId:          *partID,
		OrganizationIds: []string{*orgID},
		ComponentName:   "camera-save-predictions",
		ComponentType:   "rdk:component:camera",
	}
	if startTS != nil || endTS != nil {
		imageFilter.Interval = &datapb.CaptureInterval{Start: startTS, End: endTS}
	}

	// 2. get the images for when the camera-save-predictions saves images (which happens around when the screen marker is placed)
	images, err = fetchImages(ctx, client, imageFilter, uint64(*imagePageSize), 0)
	if err != nil {
		log.Fatalf("BinaryDataByFilter: %v", err)
	}
	fmt.Printf("matched %d image(s)\n", len(images))

	if len(images) > 0 {
		imagesDir := filepath.Join(dir, "images")
		if err := os.MkdirAll(imagesDir, 0755); err != nil {
			log.Fatalf("mkdir %s: %v", imagesDir, err)
		}
		for _, img := range images {
			raw, err := base64.StdEncoding.DecodeString(img.DataBase64)
			if err != nil {
				log.Fatalf("decode image at ts %d: %v", img.TS, err)
			}
			ext := ".jpg"
			if img.MimeType == "image/png" {
				ext = ".png"
			}
			imgPath := filepath.Join(imagesDir, sanitizeTimestamp(img.TS)+ext)
			if err := os.WriteFile(imgPath, raw, 0644); err != nil {
				log.Fatalf("write %s: %v", imgPath, err)
			}
		}
	}

	var sonarFrames []SonarFrame

	var sensorNames []string
	sensorNames = append(sensorNames, "horizontal-h-sensor", "horizontal-h3-1-sensor", "horizontal-h3-2-sensor", "horizontal-h3-3-sensor")

	appClient := apppb.NewAppServiceClient(conn)
	robotID, locationID, err := resolveRobotAndLocation(ctx, appClient, *partID)
	if err != nil {
		log.Fatalf("resolve robot/location for sonar query: %v", err)
	}

	// 3. get the sonar frames for the sonar sensors (horizontal-h-sensor, horizontal-h3-1-sensor, horizontal-h3-2-sensor, horizontal-h3-3-sensor) and render them to PNGs
	sonarFrames, err = fetchSonarFrames(
		ctx, client, *orgID, locationID, robotID, *partID, sensorNames, "rdk:component:sensor", "Readings",
		*start, *end, 500,
	)
	if err != nil {
		log.Fatalf("fetch sonar: %v", err)
	}
	fmt.Printf("rendered %d sonar frame(s) total\n", len(sonarFrames))

	if len(sonarFrames) > 0 {
		for _, frame := range sonarFrames {
			raw, err := base64.StdEncoding.DecodeString(frame.DataBase64)
			if err != nil {
				log.Fatalf("decode sonar frame at ts %d: %v", frame.TS, err)
			}
			sonarDir := filepath.Join(dir, "sonar-images", sanitizeName(frame.SensorName))
			if err := os.MkdirAll(sonarDir, 0755); err != nil {
				log.Fatalf("mkdir %s: %v", sonarDir, err)
			}
			framePath := filepath.Join(sonarDir, sanitizeTimestamp(frame.TS)+".png")
			if err := os.WriteFile(framePath, raw, 0644); err != nil {
				log.Fatalf("write %s: %v", framePath, err)
			}
		}
	}

	// 4. write the readings, images, and sonar frames to a JSON file for the placement-playback viewer to consume
	path := filepath.Join(dir, "readings.json")
	data, err := json.MarshalIndent(map[string]any{"readings": readings, "images": images, "sonarFrames": sonarFrames}, "", "  ")
	if err != nil {
		log.Fatalf("marshal readings: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}

	fmt.Printf("wrote %d readings, %d images, and %d sonar frames to %s\n",
		len(readings), len(images), len(sonarFrames), path)
}
