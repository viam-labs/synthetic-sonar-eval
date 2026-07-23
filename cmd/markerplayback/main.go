// Command markerplayback pulls marker-placement tabular readings from Viam's
// TabularDataByMQL API for a single part/component, ensures the underlying
// screen images / sonar readings for that time span are downloaded (via
// internal/fetch — reusing the cache if cmd/download already fetched the
// same part-id/window), renders the sonar readings, optionally runs ML
// detection, and writes it all out in the flat shape the placement-playback
// viewer expects.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
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

	"synthetic-sonar-eval/internal/detector"
	"synthetic-sonar-eval/internal/fetch"
	"synthetic-sonar-eval/internal/sonar"
)

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
	TS         int64                `json:"ts"`
	MimeType   string               `json:"mimeType"`
	DataBase64 string               `json:"dataBase64"`
	Detections []detector.Detection `json:"detections,omitempty"`
}

// SonarFrame is a single sonar ping, rendered to a heatmap PNG via
// internal/sonar and embedded as base64 alongside the camera frames.
type SonarFrame struct {
	SensorName string               `json:"sensorName"`
	TS         int64                `json:"ts"`
	MimeType   string               `json:"mimeType"`
	DataBase64 string               `json:"dataBase64"`
	Detections []detector.Detection `json:"detections,omitempty"`
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

// parseManifestTime parses a manifest entry's RFC3339(-ish) TimeCaptured
// string into unix milliseconds.
func parseManifestTime(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

// detectOnBytes decodes raw image bytes and runs the detector on them,
// logging a warning and returning nil (not failing the whole run) if the
// bytes can't be decoded as an image.
func detectOnBytes(det *detector.Detector, raw []byte, label string, minConfidence float32) []detector.Detection {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		log.Printf("warning: detect on %s: decode image: %v", label, err)
		return nil
	}
	dets, err := det.DetectImage(img, minConfidence)
	if err != nil {
		log.Printf("warning: detect on %s: %v", label, err)
		return nil
	}
	return dets
}

// detectImageFrames runs the detector over already-fetched screen images'
// base64-encoded bytes, in place, logging progress for every image — this is
// typically the slowest stage, so progress is reported one image at a time
// rather than batched.
func detectImageFrames(det *detector.Detector, minConfidence float32, images []ImageFrame) {
	if det == nil || len(images) == 0 {
		return
	}
	fmt.Printf("  running detection on %d screen image(s)...\n", len(images))
	for i := range images {
		raw, err := base64.StdEncoding.DecodeString(images[i].DataBase64)
		if err != nil {
			log.Printf("warning: detect on screen image %d: decode base64: %v", i, err)
			continue
		}
		images[i].Detections = detectOnBytes(det, raw, fmt.Sprintf("screen image %d", i), minConfidence)
		fmt.Printf("  detected %d/%d screen image(s)\n", i+1, len(images))
	}
}

// detectSonarFrames runs the detector over already-rendered sonar frames'
// base64-encoded PNG bytes, in place, logging progress for every frame.
func detectSonarFrames(det *detector.Detector, minConfidence float32, frames []SonarFrame) {
	if det == nil || len(frames) == 0 {
		return
	}
	fmt.Printf("  running detection on %d sonar frame(s)...\n", len(frames))
	for i := range frames {
		raw, err := base64.StdEncoding.DecodeString(frames[i].DataBase64)
		if err != nil {
			log.Printf("warning: detect on %s frame %d: decode base64: %v", frames[i].SensorName, i, err)
			continue
		}
		frames[i].Detections = detectOnBytes(det, raw, fmt.Sprintf("%s frame %d", frames[i].SensorName, i), minConfidence)
		fmt.Printf("  detected %d/%d sonar frame(s)\n", i+1, len(frames))
	}
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
	windowPad := flag.Duration("window-pad", 5*time.Minute,
		"padding applied around the placed-marker span when scoping the screen image / sonar download")
	outputDir := flag.String("output", "output", "output directory")
	runDetection := flag.Bool("detect", false, "run object detection (fish/triangle) on fetched images/sonar frames and attach the results (opt-in; off by default)")
	modelDir := flag.String("model-dir", "omni-detector-fcos-0_0_4", "directory containing model.onnx + labels.txt for detection")
	confidence := flag.Float64("confidence", 0.6, "minimum detection confidence to record")
	onnxLibPath := flag.String("onnxruntime-lib", "", "path to libonnxruntime.{dylib,so}; defaults to $ONNXRUNTIME_LIB_PATH or common install locations")
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
	if endTime.Sub(startTime) > fetch.MaxQueryWindow {
		fmt.Fprintf(os.Stderr, "error: --start/--end window must be at most %s (got %s)\n", fetch.MaxQueryWindow, endTime.Sub(startTime))
		os.Exit(1)
	}

	pipeline := buildPipeline(*partID, *start, *end)
	mqlBinary, err := fetch.QueryToBinary(pipeline)
	if err != nil {
		log.Fatalf("build query: %v", err)
	}

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+*authToken)

	creds := credentials.NewClientTLSFromCert(nil, "")
	conn, err := grpc.NewClient(fetch.ViamEndpoint, grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32*1024*1024)))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	fmt.Println("Fetching marker readings...")
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

	// Screen images and sonar readings are captured/retained far beyond what's
	// relevant here, so scope the download to the actual placed-marker span
	// (real and synthetic placements alike) rather than the full --start/--end
	// window, which only bounds how far back the marker query itself may look.
	// Padded a bit on each side since captures land "around" — not exactly at —
	// marker placement.
	windowStart, windowEnd := *start, *end
	if len(readings) > 0 {
		minTS, maxTS := readings[0].TS, readings[0].TS
		for _, r := range readings {
			if r.TS < minTS {
				minTS = r.TS
			}
			if r.TS > maxTS {
				maxTS = r.TS
			}
		}
		padStart := time.UnixMilli(minTS).Add(-*windowPad)
		padEnd := time.UnixMilli(maxTS).Add(*windowPad)
		if padStart.Before(startTime) {
			padStart = startTime
		}
		if padEnd.After(endTime) {
			padEnd = endTime
		}
		windowStart, windowEnd = padStart.UTC().Format(time.RFC3339), padEnd.UTC().Format(time.RFC3339)
		log.Printf("scoping download to the placed-marker span %s to %s (padded %s each side)",
			windowStart, windowEnd, *windowPad)
	} else {
		log.Printf("no readings found; scoping download to the full --start/--end window")
	}

	// 2. ensure the underlying screen images + sonar readings for that window are
	// downloaded, reusing cmd/download's cache (internal/fetch) if another run
	// already fetched the same part-id/window.
	hash := fetch.Hash(*orgID, windowStart, windowEnd)
	dir := fetch.ResolveDir(*outputDir, *partID, hash)
	imagesDone, err := fetch.DirHasContent(filepath.Join(dir, "images"))
	if err != nil {
		log.Fatalf("check images dir: %v", err)
	}
	tabularDone, err := fetch.DirHasContent(filepath.Join(dir, "tabular"))
	if err != nil {
		log.Fatalf("check tabular dir: %v", err)
	}
	if imagesDone && tabularDone {
		fmt.Printf("found existing download at %s (hash %s) — reusing\n", dir, hash)
	} else {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("mkdir %s: %v", dir, err)
		}
		m, err := fetch.LoadManifest(filepath.Join(dir, "manifest.json"))
		if err != nil {
			log.Fatalf("load manifest: %v", err)
		}

		if imagesDone {
			fmt.Println("images already downloaded, skipping")
		} else {
			fmt.Println("Fetching screen images...")
			if err := fetch.FetchImagesTimeRange(ctx, client, *orgID, *partID, windowStart, windowEnd, uint64(*imagePageSize), dir, m); err != nil {
				log.Fatalf("fetch images: %v", err)
			}
		}

		if tabularDone {
			fmt.Println("sonar readings already downloaded, skipping")
		} else {
			appClient := apppb.NewAppServiceClient(conn)
			robotID, locationID, err := fetch.ResolveRobotAndLocation(ctx, appClient, *partID)
			if err != nil {
				log.Fatalf("resolve robot/location for sonar query: %v", err)
			}

			fmt.Println("Fetching sonar readings...")
			if err := fetch.FetchSonarTimeRange(ctx, client, *orgID, locationID, robotID, *partID, windowStart, windowEnd, dir, m); err != nil {
				log.Fatalf("fetch sonar: %v", err)
			}
		}
	}

	// 3. render the downloaded sonar readings to PNGs (idempotent — skips
	// readings that already have a rendered PNG from a prior run).
	tabularDir := filepath.Join(dir, "tabular")
	sonarImagesDir := filepath.Join(dir, "sonar-images")
	fmt.Println("Rendering sonar frames...")
	rendered, renderSkipped, err := sonar.RenderDirectory(tabularDir, sonarImagesDir, "", 500, nil, sonar.PingPingOff, -100, sonar.CompositeWindow{})
	if err != nil {
		log.Fatalf("render sonar: %v", err)
	}
	fmt.Printf("rendered %d sonar image(s) (%d already present)\n", rendered, renderSkipped)

	// 4. load the manifest (works whether the download just ran or was a cache
	// hit) and rebuild the ImageFrame/SonarFrame lists the viewer expects.
	m, err := fetch.LoadManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		log.Fatalf("load manifest: %v", err)
	}

	var images []ImageFrame
	var sonarFrames []SonarFrame
	for _, e := range m.Entries() {
		ts, err := parseManifestTime(e.TimeCaptured)
		if err != nil {
			log.Printf("warning: parse timestamp for %s: %v", e.Path, err)
			continue
		}
		switch e.Type {
		case "binary":
			raw, err := os.ReadFile(e.Path)
			if err != nil {
				log.Printf("warning: read %s: %v", e.Path, err)
				continue
			}
			images = append(images, ImageFrame{
				TS:         ts,
				MimeType:   mimeTypeFromExt(filepath.Ext(e.Path)),
				DataBase64: base64.StdEncoding.EncodeToString(raw),
			})
		case "tabular":
			rel, err := filepath.Rel(tabularDir, e.Path)
			if err != nil {
				continue
			}
			pngPath := filepath.Join(sonarImagesDir, strings.TrimSuffix(rel, ".json")+".png")
			raw, err := os.ReadFile(pngPath)
			if err != nil {
				continue // empty grid, never rendered
			}
			sonarFrames = append(sonarFrames, SonarFrame{
				SensorName: e.ResourceName,
				TS:         ts,
				MimeType:   "image/png",
				DataBase64: base64.StdEncoding.EncodeToString(raw),
			})
		}
	}
	sort.Slice(images, func(i, j int) bool { return images[i].TS < images[j].TS })
	sort.Slice(sonarFrames, func(i, j int) bool { return sonarFrames[i].TS < sonarFrames[j].TS })
	fmt.Printf("loaded %d image(s) and %d sonar frame(s) from %s\n", len(images), len(sonarFrames), dir)

	// 5. optionally run the fish detector over all of it (opt-in via --detect).
	// A missing onnxruntime lib or model is treated as a soft failure: the rest
	// of the pull still succeeds, just without detections.
	var det *detector.Detector
	if *runDetection {
		lib, err := detector.ResolveLibPath(*onnxLibPath)
		if err != nil {
			log.Printf("warning: skipping detection: %v", err)
		} else if det, err = detector.New(*modelDir, lib); err != nil {
			log.Printf("warning: skipping detection: load model from %s: %v", *modelDir, err)
			det = nil
		} else {
			defer det.Close()
		}
	}
	if det != nil {
		fmt.Println("Running ML detection...")
		detectConfidence := float32(*confidence)
		detectImageFrames(det, detectConfidence, images)
		detectSonarFrames(det, detectConfidence, sonarFrames)
	}

	// 6. write the readings, images, and sonar frames to a JSON file for the placement-playback viewer to consume
	viewerDir := filepath.Join(dir, "marker-playback")
	if err := os.MkdirAll(viewerDir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", viewerDir, err)
	}
	path := filepath.Join(viewerDir, "readings.json")
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
