package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	datapb "go.viam.com/api/app/data/v1"
	apppb "go.viam.com/api/app/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// parseRFC3339 parses an optional RFC3339 timestamp string into a
// timestamppb.Timestamp, returning nil for an empty string (no bound).
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

// ScreenComponentNames/ScreenComponentType identify the screen-capture camera
// components polled by time-range downloads. All three are treated as the
// same logical "screen" source: their images are merged into one images/
// directory and manifest, and downstream consumers (e.g. markerplayback)
// don't distinguish between them.
var ScreenComponentNames = []string{
	"camera-save-predictions",
	"camera-save-potentials",
	"camera-data-capture",
}

const ScreenComponentType = "rdk:component:camera"

// SonarSensorNames are the four sonar fans polled by time-range downloads.
var SonarSensorNames = []string{
	"horizontal-h-sensor",
	"horizontal-h3-1-sensor",
	"horizontal-h3-2-sensor",
	"horizontal-h3-3-sensor",
}

// QueryToBinary marshals an MQL aggregation pipeline (a slice of stage maps)
// into the BSON-per-stage wire format TabularDataByMQL expects.
func QueryToBinary(pipeline []map[string]interface{}) ([][]byte, error) {
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

// ResolveRobotAndLocation looks up the robot and location IDs that own
// partID via GetRobotPart. Required for the capture_day-bucketed sonar
// query below, which mirrors the exact query shape confirmed fast (~1.3s)
// against this dataset's capture_day index, in cmd/mqlquery.
func ResolveRobotAndLocation(ctx context.Context, client apppb.AppServiceClient, partID string) (robotID, locationID string, err error) {
	resp, err := client.GetRobotPart(ctx, &apppb.GetRobotPartRequest{Id: partID})
	if err != nil {
		return "", "", fmt.Errorf("GetRobotPart: %w", err)
	}
	return resp.Part.Robot, resp.Part.LocationId, nil
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

// fetchImageMetadata paginates BinaryDataByFilter (IncludeBinary: false) to
// enumerate matching captures' metadata, without downloading bytes. The API
// forces limit=1 whenever include_binary is true, so metadata listing and
// byte downloading are done as two separate phases. SortOrder must be set to
// ASCENDING or DESCENDING for multi-page pagination — leaving it unspecified
// (the zero value) reliably 500s server-side past the first page.
func fetchImageMetadata(
	ctx context.Context, client datapb.DataServiceClient, filter *datapb.Filter, pageSize uint64,
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
		fmt.Printf("  listed %d image(s) so far...\n", len(out))
		if resp.Last == "" || uint64(len(resp.Data)) < pageSize {
			break
		}
		last = resp.Last
	}
	return out, nil
}

// FetchImagesTimeRange downloads every screen-capture image (components
// ScreenComponentNames, all treated as the same "screen" source) for partID
// within [start, end], writing raw bytes to <outputDir>/images/ and
// recording each in m.
func FetchImagesTimeRange(
	ctx context.Context, client datapb.DataServiceClient, orgID, partID, start, end string, pageSize uint64,
	outputDir string, m *Manifest,
) error {
	startTS, err := parseRFC3339(start)
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}
	endTS, err := parseRFC3339(end)
	if err != nil {
		return fmt.Errorf("end: %w", err)
	}

	var metas []*datapb.BinaryData
	for _, componentName := range ScreenComponentNames {
		filter := &datapb.Filter{
			PartId:          partID,
			OrganizationIds: []string{orgID},
			ComponentName:   componentName,
			ComponentType:   ScreenComponentType,
		}
		if startTS != nil || endTS != nil {
			filter.Interval = &datapb.CaptureInterval{Start: startTS, End: endTS}
		}

		fmt.Printf("  listing images for component %q...\n", componentName)
		found, err := fetchImageMetadata(ctx, client, filter, pageSize)
		if err != nil {
			return fmt.Errorf("BinaryDataByFilter (%s): %w", componentName, err)
		}
		metas = append(metas, found...)
	}
	if len(metas) == 0 {
		fmt.Println("  no images found in range")
		return nil
	}
	fmt.Printf("  downloading %d image(s)...\n", len(metas))

	imagesDir := filepath.Join(outputDir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return err
	}

	downloaded := 0
	for i := 0; i < len(metas); i += binaryDataIDBatchSize {
		end := min(i+binaryDataIDBatchSize, len(metas))
		ids := make([]string, 0, end-i)
		for _, mm := range metas[i:end] {
			ids = append(ids, mm.Metadata.BinaryDataId)
		}

		resp, err := client.BinaryDataByIDs(ctx, &datapb.BinaryDataByIDsRequest{
			IncludeBinary: true,
			BinaryDataIds: ids,
		})
		if err != nil {
			return fmt.Errorf("BinaryDataByIDs: %w", err)
		}

		var pageEntries []ManifestEntry
		for _, d := range resp.Data {
			meta := d.Metadata
			mimeType := ""
			componentName := ""
			if meta.CaptureMetadata != nil {
				mimeType = meta.CaptureMetadata.MimeType
				componentName = meta.CaptureMetadata.ComponentName
			}
			if mimeType == "" {
				mimeType = mimeTypeFromExt(meta.FileExt)
			}
			ext := ".jpg"
			if mimeType == "image/png" {
				ext = ".png"
			}
			ts := meta.GetTimeRequested().AsTime().UTC()
			// The BinaryDataId is included (not just the timestamp) because
			// multiple captures can share the same instant, which would
			// otherwise collide on a single filename and silently overwrite
			// each other on disk. BinaryDataId is a composite
			// "<org-id>/<part>/<unique-id>" string, so take the last (unique)
			// path segment rather than the shared org/part prefix.
			idSuffix := meta.GetBinaryDataId()
			if idx := strings.LastIndex(idSuffix, "/"); idx >= 0 {
				idSuffix = idSuffix[idx+1:]
			}
			if len(idSuffix) > 12 {
				idSuffix = idSuffix[:12]
			}
			path := filepath.Join(imagesDir, SanitizeTimestamp(ts.Format(time.RFC3339Nano))+"_"+idSuffix+ext)
			if err := os.WriteFile(path, d.Binary, 0644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			pageEntries = append(pageEntries, ManifestEntry{
				Type:          "binary",
				Path:          path,
				TimeCaptured:  ts.Format(time.RFC3339Nano),
				ComponentName: componentName,
			})
		}
		if err := m.Add(pageEntries); err != nil {
			log.Printf("warning: manifest flush failed: %v", err)
		}
		downloaded += len(pageEntries)
		fmt.Printf("  downloaded %d/%d image(s)\n", downloaded, len(metas))
	}

	return nil
}

// sonarResultLimit is a generous cap on a single leaf query's result size —
// mostly a safety net; the real defense against the server's "result set is
// too large" guard is windowStart/windowEnd bisection in fetchSonarWindow
// below, since that guard triggers on the total matched-document count
// itself, before $skip/$limit ever get a chance to slice it.
const sonarResultLimit = 5000

// minSonarBisectWindow is the smallest time.Duration fetchSonarWindow will
// still bisect on a "too large" error. Below this, a slice that's still too
// large is logged and given up on rather than split forever.
const minSonarBisectWindow = 30 * time.Second

// shouldBisectWindow reports whether err is one this package can work around
// by narrowing the query window and retrying, rather than a hard failure to
// just log and skip:
//   - "result set is too large; try adding limits to your query using
//     $limit and $skip" — the server's guard on total matched-document count,
//     which triggers before $limit/$skip ever get a chance to slice it.
//   - DeadlineExceeded ("query timed out") — a server-side query timeout,
//     which a smaller (cheaper to scan) window is also likely to fix.
//
// Other errors (network blips, auth failures, ...) aren't retried this way.
func shouldBisectWindow(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "result set is too large") {
		return true
	}
	if status.Code(err) == codes.DeadlineExceeded {
		return true
	}
	return false
}

// buildCaptureDayPipeline matches on capture_day (an indexed day-bucket
// field) instead of a bare time_received range via $expr, which isn't
// index-backed and can time out once a sensor has more than a few thousand
// matching documents across the *whole* multi-day query window (some sonar
// sensors here have 250k+ pings across just a few days). windowStart/
// windowEnd additionally bound time_received via $expr (same pattern as
// buildPipeline) on top of that already-cheap, indexed day-bucket match, so
// the matched set is only what's actually requested, not the entire day.
func buildCaptureDayPipeline(
	locationID, robotID, partID, componentName, componentType, methodName string, day time.Time,
	windowStart, windowEnd string,
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

	var bounds []interface{}
	if windowStart != "" {
		bounds = append(bounds, map[string]interface{}{
			"$gte": []interface{}{"$time_received", map[string]interface{}{"$toDate": windowStart}},
		})
	}
	if windowEnd != "" {
		bounds = append(bounds, map[string]interface{}{
			"$lte": []interface{}{"$time_received", map[string]interface{}{"$toDate": windowEnd}},
		})
	}
	switch len(bounds) {
	case 0:
	case 1:
		match["$expr"] = bounds[0]
	default:
		match["$expr"] = map[string]interface{}{"$and": bounds}
	}

	return []map[string]interface{}{
		{"$match": match},
		{"$limit": sonarResultLimit},
	}
}

// extractReadingsPayload pulls the raw sonar reading payload out of a raw
// TabularDataByMQL document (expected under data.readings, matching the
// payload.readings shape used elsewhere), returning it re-marshaled into the
// {"readings": ...} shape RenderDirectory expects on disk, plus the ping's
// receive time. ok=false (not an error) means the document can't be turned
// into a frame (e.g. an empty grid).
func extractReadingsPayload(doc map[string]interface{}) (payload json.RawMessage, timeCaptured time.Time, ok bool) {
	data, _ := doc["data"].(map[string]interface{})
	values, isMap := data["readings"].(map[string]interface{})
	if !isMap || values == nil {
		return nil, time.Time{}, false
	}

	nBeams, _ := values["n_beams"].(float64)
	nSamples, _ := values["n_samples"].(float64)
	if nBeams == 0 || nSamples == 0 {
		return nil, time.Time{}, false
	}

	received, ok := doc["time_received"].(primitive.DateTime)
	if !ok {
		return nil, time.Time{}, false
	}

	raw, err := json.Marshal(map[string]interface{}{"readings": values})
	if err != nil {
		return nil, time.Time{}, false
	}
	return raw, received.Time().UTC(), true
}

// sonarWindowSize is the fixed query span used to fetch sonar readings.
// Small fixed windows keep each query well under the server's "result set is
// too large" guard from the outset, so windows can simply be fetched
// concurrently instead of the old scheme of querying a whole day and
// reactively bisecting it on failure.
const sonarWindowSize = 5 * time.Minute

// sonarFetchConcurrency caps how many sonar window queries run at once.
const sonarFetchConcurrency = 8

// sonarWindowJob is one (sensor, day, sub-window) query to run, where day is
// the capture_day bucket the window falls within (see buildCaptureDayPipeline).
type sonarWindowJob struct {
	sensorName             string
	day                    time.Time
	windowStart, windowEnd time.Time
}

// FetchSonarTimeRange downloads raw sonar tabular readings for SonarSensorNames
// within [start, end] for partID, writing TabularDataPoint-shaped JSON (so
// internal/sonar.RenderDirectory can render either download mode uniformly)
// to <outputDir>/tabular/<sensor>/ and recording each in m. Queries run as
// sonarWindowSize-wide windows fetched concurrently (up to
// sonarFetchConcurrency at a time) across every sensor and window, rather
// than one sensor/day at a time.
func FetchSonarTimeRange(
	ctx context.Context, client datapb.DataServiceClient, orgID, locationID, robotID, partID string,
	start, end string, outputDir string, m *Manifest,
) error {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return fmt.Errorf("parse start: %w", err)
	}
	endTime, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return fmt.Errorf("parse end: %w", err)
	}

	startDay := time.Date(startTime.Year(), startTime.Month(), startTime.Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(endTime.Year(), endTime.Month(), endTime.Day(), 0, 0, 0, 0, time.UTC)

	var jobs []sonarWindowJob
	for _, sensorName := range SonarSensorNames {
		sensorDir := filepath.Join(outputDir, "tabular", SanitizeName(sensorName))
		if err := os.MkdirAll(sensorDir, 0755); err != nil {
			return err
		}

		for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
			// Intersect the requested [start, end] with this calendar day's
			// bounds, since capture_day matches the whole day but we only want
			// what's actually in range.
			dayLowerBound := day
			if startTime.After(dayLowerBound) {
				dayLowerBound = startTime
			}
			dayUpperBound := day.AddDate(0, 0, 1)
			if endTime.Before(dayUpperBound) {
				dayUpperBound = endTime
			}

			for ws := dayLowerBound; ws.Before(dayUpperBound); ws = ws.Add(sonarWindowSize) {
				we := ws.Add(sonarWindowSize)
				if we.After(dayUpperBound) {
					we = dayUpperBound
				}
				jobs = append(jobs, sonarWindowJob{sensorName: sensorName, day: day, windowStart: ws, windowEnd: we})
			}
		}
	}
	fmt.Printf("fetching sonar readings across %d window(s) (%d sensor(s), %s each, up to %d at a time)...\n",
		len(jobs), len(SonarSensorNames), sonarWindowSize, sonarFetchConcurrency)

	var (
		wg        sync.WaitGroup
		sem       = make(chan struct{}, sonarFetchConcurrency)
		mu        sync.Mutex
		found     = make(map[string]int, len(SonarSensorNames))
		failed    = make(map[string]int, len(SonarSensorNames))
		completed int
	)
	for _, j := range jobs {
		sensorDir := filepath.Join(outputDir, "tabular", SanitizeName(j.sensorName))

		wg.Add(1)
		sem <- struct{}{}
		go func(j sonarWindowJob) {
			defer wg.Done()
			defer func() { <-sem }()

			n, err := fetchSonarWindow(ctx, client, orgID, locationID, robotID, partID, j.sensorName, sensorDir, j.day, j.windowStart, j.windowEnd, m)

			mu.Lock()
			defer mu.Unlock()
			found[j.sensorName] += n
			if err != nil {
				log.Printf("error: %s %s to %s: %v", j.sensorName, j.windowStart.Format(time.RFC3339), j.windowEnd.Format(time.RFC3339), err)
				failed[j.sensorName]++
			}
			completed++
			if completed%25 == 0 || completed == len(jobs) {
				fmt.Printf("  %d/%d window(s) done\n", completed, len(jobs))
			}
		}(j)
	}
	wg.Wait()

	for _, sensorName := range SonarSensorNames {
		if failed[sensorName] > 0 {
			log.Printf("warning: %s: %d window(s) failed (timeout or error) and were skipped", sensorName, failed[sensorName])
		}
		fmt.Printf("%s: fetched %d record(s)\n", sensorName, found[sensorName])
	}

	return nil
}

// fetchSonarWindow queries [windowStart, windowEnd) within a single calendar
// day for one sensor, writing any resulting readings to disk and recording
// them in m. If the server rejects the query as "too large" and the window
// is still wider than minSonarBisectWindow, it splits the window in half and
// retries each half recursively — the guard triggers on the total matched
// count before $limit ever applies, so narrowing the query is the only way
// around it. Returns the number of readings written; a non-nil error means
// at least one leaf window still failed (or was too large to split further),
// but any readings from other leaves are still returned/written.
func fetchSonarWindow(
	ctx context.Context, client datapb.DataServiceClient, orgID, locationID, robotID, partID, sensorName, sensorDir string,
	day, windowStart, windowEnd time.Time, m *Manifest,
) (int, error) {
	pipeline := buildCaptureDayPipeline(locationID, robotID, partID, sensorName, "rdk:component:sensor", "Readings", day,
		windowStart.UTC().Format(time.RFC3339Nano), windowEnd.UTC().Format(time.RFC3339Nano))
	mqlBinary, err := QueryToBinary(pipeline)
	if err != nil {
		return 0, fmt.Errorf("build query: %w", err)
	}

	resp, err := client.TabularDataByMQL(ctx, &datapb.TabularDataByMQLRequest{
		OrganizationId: orgID,
		MqlBinary:      mqlBinary,
	})
	if err != nil {
		fmt.Printf("window start: %s, window end: %s, error: %v\n", windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), err)
		if shouldBisectWindow(err) && windowEnd.Sub(windowStart) > minSonarBisectWindow {
			mid := windowStart.Add(windowEnd.Sub(windowStart) / 2)
			left, leftErr := fetchSonarWindow(ctx, client, orgID, locationID, robotID, partID, sensorName, sensorDir, day, windowStart, mid, m)
			right, rightErr := fetchSonarWindow(ctx, client, orgID, locationID, robotID, partID, sensorName, sensorDir, day, mid, windowEnd, m)
			if leftErr != nil {
				return left + right, leftErr
			}
			return left + right, rightErr
		}
		return 0, fmt.Errorf("TabularDataByMQL (%s to %s): %w", windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), err)
	}

	var pageEntries []ManifestEntry
	for _, raw := range resp.RawData {
		var doc map[string]interface{}
		if err := bson.Unmarshal(raw, &doc); err != nil {
			continue
		}
		payload, timeCaptured, ok := extractReadingsPayload(doc)
		if !ok {
			continue
		}

		dp := TabularDataPoint{
			ResourceName: sensorName,
			TimeCaptured: timeCaptured.Format(time.RFC3339Nano),
			Payload:      payload,
		}
		data, err := json.MarshalIndent(dp, "", "  ")
		if err != nil {
			continue
		}
		path := filepath.Join(sensorDir, SanitizeTimestamp(dp.TimeCaptured)+".json")
		if err := os.WriteFile(path, data, 0644); err != nil {
			return len(pageEntries), fmt.Errorf("write %s: %w", path, err)
		}
		pageEntries = append(pageEntries, ManifestEntry{
			Type:         "tabular",
			Path:         path,
			TimeCaptured: dp.TimeCaptured,
			ResourceName: sensorName,
		})
	}
	if err := m.Add(pageEntries); err != nil {
		log.Printf("warning: manifest flush failed: %v", err)
	}
	return len(pageEntries), nil
}
