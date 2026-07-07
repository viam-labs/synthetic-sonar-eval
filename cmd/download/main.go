// Command download fetches raw sonar/screen data for a part, either from a
// whole recorded sequence (--sequence-id) or by polling a time range
// (--start/--end), and writes it under
// <output>/<part-id>/<hash-of-params>/ so re-running with the same
// parameters is a cheap no-op (skipped once that hash directory exists).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/joho/godotenv"
	datapb "go.viam.com/api/app/data/v1"
	apppb "go.viam.com/api/app/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	"synthetic-sonar-eval/internal/fetch"
)

func main() {
	_ = godotenv.Load()

	partID := flag.String("part-id", "", "part ID to download data for (required)")
	orgID := flag.String("org-id", os.Getenv("VIAM_ORG_ID"), "organization ID (required for --start/--end mode; or set VIAM_ORG_ID in .env)")
	// Default deliberately omitted from the flag registration (and thus from --help output) so a
	// live token never gets echoed to the terminal; VIAM_AUTH_TOKEN is applied as a fallback below.
	authToken := flag.String("token", "", "auth token (or set VIAM_AUTH_TOKEN in .env)")
	outputDir := flag.String("output", "output", "output directory")

	sequenceID := flag.String("sequence-id", "", "sequence ID to download (mode A — mutually exclusive with --start/--end)")
	pageSize := flag.Uint("page-size", 100, "page size for tabular data pagination (sequence mode)")

	start := flag.String("start", "", "only include data at/after this RFC3339 time (mode B — mutually exclusive with --sequence-id)")
	end := flag.String("end", "", "only include data at/before this RFC3339 time (mode B)")
	imagePageSize := flag.Uint("image-page-size", 50, "page size for image pagination (time-range mode)")
	flag.Parse()

	if *authToken == "" {
		*authToken = os.Getenv("VIAM_AUTH_TOKEN")
	}

	if *partID == "" {
		fmt.Fprintln(os.Stderr, "error: --part-id is required")
		flag.Usage()
		os.Exit(1)
	}
	if *authToken == "" {
		fmt.Fprintln(os.Stderr, "error: auth token required (set VIAM_AUTH_TOKEN in .env or use --token)")
		os.Exit(1)
	}

	sequenceMode := *sequenceID != ""
	rangeMode := *start != "" || *end != ""
	if sequenceMode == rangeMode {
		fmt.Fprintln(os.Stderr, "error: exactly one of --sequence-id or --start/--end is required")
		os.Exit(1)
	}
	if rangeMode && (*start == "" || *end == "") {
		fmt.Fprintln(os.Stderr, "error: --start and --end must both be given")
		os.Exit(1)
	}
	if rangeMode && *orgID == "" {
		fmt.Fprintln(os.Stderr, "error: organization ID required for --start/--end mode (set VIAM_ORG_ID in .env or use --org-id)")
		os.Exit(1)
	}

	var hash string
	if sequenceMode {
		hash = fetch.Hash(*sequenceID)
	} else {
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
		hash = fetch.Hash(*orgID, *start, *end)
	}

	dir, exists, err := fetch.ResolveDir(*outputDir, *partID, hash)
	if err != nil {
		log.Fatalf("resolve download dir: %v", err)
	}
	if exists {
		fmt.Printf("found existing download for part %s at %s (hash %s) — skipping\n", *partID, dir, hash)
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", dir, err)
	}

	ctx := context.Background()
	creds := credentials.NewClientTLSFromCert(nil, "")
	conn, err := grpc.NewClient(fetch.ViamEndpoint, grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(32*1024*1024)))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	if sequenceMode {
		downloadSequence(ctx, conn, *authToken, *sequenceID, dir, uint32(*pageSize))
	} else {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+*authToken)
		downloadTimeRange(ctx, conn, *orgID, *partID, *start, *end, dir, uint64(*imagePageSize))
	}

	fmt.Printf("Done. Data at %s\n", dir)
}

func downloadSequence(ctx context.Context, conn *grpc.ClientConn, authToken, sequenceID, dir string, pageSize uint32) {
	tabularDir := filepath.Join(dir, "tabular")
	imagesDir := filepath.Join(dir, "images")
	for _, d := range []string{tabularDir, imagesDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			log.Fatalf("mkdir %s: %v", d, err)
		}
	}

	manifestPath := filepath.Join(dir, "manifest.json")
	progressPath := filepath.Join(dir, "progress.json")

	m, err := fetch.LoadManifest(manifestPath)
	if err != nil {
		log.Fatalf("load manifest: %v", err)
	}
	p, err := fetch.LoadSequenceProgress(progressPath, sequenceID)
	if err != nil {
		log.Fatalf("load progress: %v", err)
	}

	dl := fetch.NewSequenceDownloader(conn, authToken, m, p)

	fmt.Println("Downloading tabular data...")
	if err := dl.DownloadTabular(ctx, sequenceID, tabularDir, pageSize); err != nil {
		log.Fatalf("tabular: %v", err)
	}
	fmt.Printf("Tabular complete: %d total manifest entries\n\n", len(m.Entries()))

	fmt.Println("Downloading binary data...")
	if err := dl.DownloadBinary(ctx, sequenceID, imagesDir); err != nil {
		log.Fatalf("binary: %v", err)
	}
	fmt.Printf("Binary complete: %d total manifest entries\n\n", len(m.Entries()))
}

func downloadTimeRange(ctx context.Context, conn *grpc.ClientConn, orgID, partID, start, end, dir string, imagePageSize uint64) {
	manifestPath := filepath.Join(dir, "manifest.json")
	m, err := fetch.LoadManifest(manifestPath)
	if err != nil {
		log.Fatalf("load manifest: %v", err)
	}

	client := datapb.NewDataServiceClient(conn)
	appClient := apppb.NewAppServiceClient(conn)

	fmt.Println("Fetching screen images...")
	if err := fetch.FetchImagesTimeRange(ctx, client, orgID, partID, start, end, imagePageSize, dir, m); err != nil {
		log.Fatalf("fetch images: %v", err)
	}

	robotID, locationID, err := fetch.ResolveRobotAndLocation(ctx, appClient, partID)
	if err != nil {
		log.Fatalf("resolve robot/location for sonar query: %v", err)
	}

	fmt.Println("Fetching sonar readings...")
	if err := fetch.FetchSonarTimeRange(ctx, client, orgID, locationID, robotID, partID, start, end, dir, m); err != nil {
		log.Fatalf("fetch sonar: %v", err)
	}
}
