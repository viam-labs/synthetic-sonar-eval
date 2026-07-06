// Command markerplayback pulls marker-placement tabular readings from Viam's
// TabularDataByMQL API for a single part/component and writes them out in the
// flat shape the placement-playback viewer expects.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	datapb "go.viam.com/api/app/data/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

const viamEndpoint = "app.viam.com:443"

// Reading is the flat shape consumed by the placement-playback viewer.
type Reading struct {
	Depth       float64 `json:"depth"`
	IsSynthetic bool    `json:"is_synthetic"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
	MarkerID    string  `json:"marker_id"`
	TS          int64   `json:"ts"`
}

// buildPipeline mirrors the requested MQL shape: a single $match stage on the
// resource identity fields, plus an optional $expr time_received bound.
func buildPipeline(partID, componentName, componentType, methodName, start, end string) []map[string]interface{} {
	match := map[string]interface{}{
		"part_id":        partID,
		"component_name": componentName,
		"component_type": componentType,
		"method_name":    methodName,
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

func main() {
	_ = godotenv.Load()

	partID := flag.String("part-id", "", "part ID to pull marker readings for (required)")
	orgID := flag.String("org-id", os.Getenv("VIAM_ORG_ID"), "organization ID (or set VIAM_ORG_ID in .env)")
	authToken := flag.String("token", os.Getenv("VIAM_AUTH_TOKEN"), "auth token (or set VIAM_AUTH_TOKEN in .env)")
	componentName := flag.String("component-name", "placemarker-synth-ai", "component name to match")
	componentType := flag.String("component-type", "rdk:component:sensor", "component type to match")
	methodName := flag.String("method-name", "Readings", "method name to match")
	start := flag.String("start", "", "only include readings at/after this RFC3339 time_received (optional)")
	end := flag.String("end", "", "only include readings at/before this RFC3339 time_received (optional)")
	limit := flag.Uint("limit", 0, "cap the number of matched documents via $limit (0 = no cap)")
	outputDir := flag.String("output", "output", "output directory")
	flag.Parse()

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

	pipeline := buildPipeline(*partID, *componentName, *componentType, *methodName, *start, *end)
	if *limit > 0 {
		pipeline = append(pipeline, map[string]interface{}{"$limit": *limit})
	}
	mqlBinary, err := queryToBinary(pipeline)
	if err != nil {
		log.Fatalf("build query: %v", err)
	}

	ctx := context.Background()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+*authToken)

	creds := credentials.NewClientTLSFromCert(nil, "")
	conn, err := grpc.NewClient(viamEndpoint, grpc.WithTransportCredentials(creds))
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
	fmt.Printf("matched %d documents\n", len(resp.RawData))

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
	path := filepath.Join(dir, "readings.json")
	data, err := json.MarshalIndent(map[string]any{"readings": readings}, "", "  ")
	if err != nil {
		log.Fatalf("marshal readings: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Fatalf("write %s: %v", path, err)
	}

	fmt.Printf("wrote %d readings to %s\n", len(readings), path)
}
