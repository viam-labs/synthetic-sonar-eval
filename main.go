package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/grpcreflect"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/runtime/protoiface"
)

const (
	viamEndpoint  = "app.viam.com:443"
	tabularMethod = "datamanagement.internalapi.v1.InternalDataService/GetSequenceTabularData"
	binaryMethod  = "datamanagement.internalapi.v1.InternalDataService/GetSequenceBinaryData"
)

var tabularResources = []string{
	"horizontal-h-sensor",
	"horizontal-h3-1-sensor",
	"horizontal-h3-2-sensor",
	"horizontal-h3-3-sensor",
}

// --- Response types ---

type TabularDataPoint struct {
	PartID          string          `json:"partId"`
	ResourceName    string          `json:"resourceName"`
	ResourceSubtype string          `json:"resourceSubtype"`
	MethodName      string          `json:"methodName"`
	TimeCaptured    string          `json:"timeCaptured"`
	TimeSynced      string          `json:"timeSynced"`
	Payload         json.RawMessage `json:"payload"`
}

type TabularResponse struct {
	DataPoints    []TabularDataPoint `json:"dataPoints"`
	NextPageToken string             `json:"nextPageToken"`
}

type CaptureMetadata struct {
	OrganizationID string `json:"organizationId"`
	LocationID     string `json:"locationId"`
	RobotName      string `json:"robotName"`
	RobotID        string `json:"robotId"`
	PartName       string `json:"partName"`
	PartID         string `json:"partId"`
	ComponentType  string `json:"componentType"`
	ComponentName  string `json:"componentName"`
	MethodName     string `json:"methodName"`
	MimeType       string `json:"mimeType"`
}

type BinaryMetadata struct {
	ID              string          `json:"id"`
	CaptureMetadata CaptureMetadata `json:"captureMetadata"`
	TimeRequested   string          `json:"timeRequested"`
	TimeReceived    string          `json:"timeReceived"`
	FileName        string          `json:"fileName"`
	FileExt         string          `json:"fileExt"`
	URI             string          `json:"uri"`
	BinaryDataID    string          `json:"binaryDataId"`
	FileSizeBytes   string          `json:"fileSizeBytes"`
}

type BinaryDataItem struct {
	Metadata BinaryMetadata `json:"metadata"`
}

type BinaryResponse struct {
	Data          []BinaryDataItem `json:"data"`
	NextPageToken string           `json:"nextPageToken"`
}

// ManifestEntry records every saved file with its timestamp for later correlation.
type ManifestEntry struct {
	Type          string `json:"type"`
	Path          string `json:"path"`
	TimeCaptured  string `json:"timeCaptured"`
	ResourceName  string `json:"resourceName,omitempty"`
	ComponentName string `json:"componentName,omitempty"`
}

// --- Checkpoint state ---

type resourceProgress struct {
	NextPageToken string `json:"nextPageToken"`
	Done          bool   `json:"done"`
}

// progress tracks where we left off so a failed run can resume.
type progress struct {
	path string

	SequenceID          string                      `json:"sequenceId"`
	TabularResources    map[string]resourceProgress `json:"tabularResources"`
	BinaryNextPageToken string                      `json:"binaryNextPageToken"`
	BinaryDone          bool                        `json:"binaryDone"`
}

func (p *progress) tabularDone() bool {
	for _, r := range tabularResources {
		if !p.TabularResources[r].Done {
			return false
		}
	}
	return true
}

func loadProgress(path, sequenceID string) (*progress, error) {
	p := &progress{
		path:             path,
		SequenceID:       sequenceID,
		TabularResources: map[string]resourceProgress{},
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("corrupt progress file: %w", err)
	}
	if p.SequenceID != sequenceID {
		log.Printf("progress file belongs to sequence %s, starting fresh for %s", p.SequenceID, sequenceID)
		return &progress{path: path, SequenceID: sequenceID, TabularResources: map[string]resourceProgress{}}, nil
	}
	if p.TabularResources == nil {
		p.TabularResources = map[string]resourceProgress{}
	}
	return p, nil
}

func (p *progress) save() error {
	return atomicWriteJSON(p.path, p)
}

// manifest tracks all downloaded files and deduplicates entries by path.
type manifest struct {
	mu      sync.Mutex
	path    string
	entries []ManifestEntry
	seen    map[string]bool
}

func loadManifest(path string) (*manifest, error) {
	m := &manifest{path: path, seen: map[string]bool{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &m.entries); err != nil {
		return nil, fmt.Errorf("corrupt manifest: %w", err)
	}
	for _, e := range m.entries {
		m.seen[e.Path] = true
	}
	return m, nil
}

// add appends entries that haven't been recorded yet, then flushes to disk.
func (m *manifest) add(entries []ManifestEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range entries {
		if !m.seen[e.Path] {
			m.entries = append(m.entries, e)
			m.seen[e.Path] = true
		}
	}
	return atomicWriteJSON(m.path, m.entries)
}

func atomicWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- gRPC plumbing ---

type grpcHandler struct {
	grpcurl.DefaultEventHandler
	mu        sync.Mutex
	responses []string
}

func (h *grpcHandler) OnReceiveResponse(resp protoiface.MessageV1) {
	s, err := h.Formatter(resp)
	if err != nil {
		log.Printf("format error: %v", err)
		return
	}
	h.mu.Lock()
	h.responses = append(h.responses, s)
	h.mu.Unlock()
}

// --- Downloader ---

type downloader struct {
	conn       *grpc.ClientConn
	authToken  string
	authHeader string
	httpClient *http.Client
	manifest   *manifest
	progress   *progress
}

func newDownloader(conn *grpc.ClientConn, authToken string, m *manifest, p *progress) *downloader {
	return &downloader{
		conn:       conn,
		authToken:  authToken,
		authHeader: "Authorization: Bearer " + authToken,
		httpClient: &http.Client{},
		manifest:   m,
		progress:   p,
	}
}

func (d *downloader) callGRPC(ctx context.Context, method, requestJSON string) ([]json.RawMessage, error) {
	authCtx := metadata.NewOutgoingContext(ctx, grpcurl.MetadataFromHeaders([]string{d.authHeader}))

	refClient := grpcreflect.NewClientAuto(authCtx, d.conn)
	refClient.AllowMissingFileDescriptors()
	defer refClient.Reset()
	source := grpcurl.DescriptorSourceFromServer(authCtx, refClient)

	rf, formatter, err := grpcurl.RequestParserAndFormatterFor(
		grpcurl.FormatJSON, source, true, false, strings.NewReader(requestJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("parser setup: %w", err)
	}

	handler := &grpcHandler{
		DefaultEventHandler: grpcurl.DefaultEventHandler{Formatter: formatter},
	}
	if err := grpcurl.InvokeRPC(authCtx, source, d.conn, method, []string{d.authHeader}, handler, rf.Next); err != nil {
		return nil, fmt.Errorf("RPC: %w", err)
	}
	if handler.Status != nil && handler.Status.Code() != 0 {
		return nil, fmt.Errorf("RPC error %v: %s", handler.Status.Code(), handler.Status.Message())
	}

	var results []json.RawMessage
	for _, r := range handler.responses {
		results = append(results, json.RawMessage(r))
	}
	return results, nil
}

func (d *downloader) downloadTabular(ctx context.Context, sequenceID, outputDir string, pageSize uint32) error {
	for _, resourceName := range tabularResources {
		rp := d.progress.TabularResources[resourceName]
		if rp.Done {
			fmt.Printf("  tabular %s: already complete, skipping\n", resourceName)
			continue
		}
		fmt.Printf("  tabular %s: starting from page token %q\n", resourceName, rp.NextPageToken)
		if err := d.downloadTabularResource(ctx, sequenceID, outputDir, pageSize, resourceName, rp.NextPageToken); err != nil {
			return fmt.Errorf("%s: %w", resourceName, err)
		}
	}
	return nil
}

func (d *downloader) downloadTabularResource(ctx context.Context, sequenceID, outputDir string, pageSize uint32, resourceName, startToken string) error {
	pageToken := startToken
	total := 0

	for page := 1; ; page++ {
		req := map[string]any{
			"sequence_id": sequenceID,
			"page_size":   1,
			"resource": map[string]any{
				"resource_name": resourceName,
				"method_name":   "Readings",
			},
		}
		if pageToken != "" {
			req["page_token"] = pageToken
		}
		reqJSON, _ := json.Marshal(req)

		responses, err := d.callGRPC(ctx, tabularMethod, string(reqJSON))
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}

		var nextToken string
		var pageEntries []ManifestEntry
		for _, raw := range responses {
			var resp TabularResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("parse tabular response: %w", err)
			}
			nextToken = resp.NextPageToken

			for _, dp := range resp.DataPoints {
				dir := filepath.Join(outputDir, sanitizeName(dp.ResourceName))
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}
				path := filepath.Join(dir, sanitizeTimestamp(dp.TimeCaptured)+".json")
				data, err := json.MarshalIndent(dp, "", "  ")
				if err != nil {
					return err
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					return err
				}
				pageEntries = append(pageEntries, ManifestEntry{
					Type:         "tabular",
					Path:         path,
					TimeCaptured: dp.TimeCaptured,
					ResourceName: dp.ResourceName,
				})
			}
		}

		// Flush manifest first, then progress. If we crash between the two,
		// the seen map deduplicates on the next run.
		if err := d.manifest.add(pageEntries); err != nil {
			log.Printf("warning: manifest flush failed: %v", err)
		}
		total += len(pageEntries)
		d.progress.TabularResources[resourceName] = resourceProgress{
			NextPageToken: nextToken,
			Done:          nextToken == "",
		}
		if err := d.progress.save(); err != nil {
			log.Printf("warning: progress save failed: %v", err)
		}

		fmt.Printf("    page %d: %d total records\n", page, total)
		pageToken = nextToken
		if pageToken == "" {
			break
		}
	}
	return nil
}

func (d *downloader) downloadBinary(ctx context.Context, sequenceID, outputDir string) error {
	if d.progress.BinaryDone {
		fmt.Println("  binary: already complete, skipping")
		return nil
	}

	pageToken := d.progress.BinaryNextPageToken
	total := 0

	for page := 1; ; page++ {
		req := map[string]any{"sequence_id": sequenceID}
		if pageToken != "" {
			req["page_token"] = pageToken
		}
		reqJSON, _ := json.Marshal(req)

		responses, err := d.callGRPC(ctx, binaryMethod, string(reqJSON))
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}

		var nextToken string
		var pageEntries []ManifestEntry
		for _, raw := range responses {
			var resp BinaryResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("parse binary response: %w", err)
			}
			nextToken = resp.NextPageToken

			for _, item := range resp.Data {
				meta := item.Metadata
				dir := filepath.Join(outputDir, sanitizeName(meta.CaptureMetadata.ComponentName))
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}

				filename := meta.FileName
				if filename == "" {
					ext := meta.FileExt
					if ext == "" {
						ext = ".jpeg"
					}
					filename = sanitizeTimestamp(meta.TimeRequested) + ext
				}
				path := filepath.Join(dir, filename)

				// Skip if already downloaded (covers mid-page crash recovery).
				if _, err := os.Stat(path); err == nil {
					pageEntries = append(pageEntries, ManifestEntry{
						Type:          "binary",
						Path:          path,
						TimeCaptured:  meta.TimeRequested,
						ComponentName: meta.CaptureMetadata.ComponentName,
					})
					continue
				}

				if err := d.downloadFile(meta.URI, path); err != nil {
					log.Printf("warning: failed to download %s: %v", meta.URI, err)
					continue
				}
				pageEntries = append(pageEntries, ManifestEntry{
					Type:          "binary",
					Path:          path,
					TimeCaptured:  meta.TimeRequested,
					ComponentName: meta.CaptureMetadata.ComponentName,
				})
			}
		}

		if err := d.manifest.add(pageEntries); err != nil {
			log.Printf("warning: manifest flush failed: %v", err)
		}
		total += len(pageEntries)
		d.progress.BinaryNextPageToken = nextToken
		if nextToken == "" {
			d.progress.BinaryDone = true
		}
		if err := d.progress.save(); err != nil {
			log.Printf("warning: progress save failed: %v", err)
		}

		fmt.Printf("  binary page %d: %d total files\n", page, total)
		pageToken = nextToken
		if pageToken == "" {
			break
		}
	}
	return nil
}

func (d *downloader) downloadFile(uri, path string) error {
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+d.authToken)

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// --- Helpers ---

func sanitizeTimestamp(ts string) string {
	return strings.NewReplacer(":", "-", ".", "-", " ", "_", "/", "-").Replace(ts)
}

func sanitizeName(name string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_").Replace(name)
}

// --- Entry point ---

func main() {
	_ = godotenv.Load()

	sequenceID := flag.String("sequence-id", "", "sequence ID to download (required)")
	outputDir := flag.String("output", "output", "output directory")
	authToken := flag.String("token", os.Getenv("VIAM_AUTH_TOKEN"), "auth token (or set VIAM_AUTH_TOKEN in .env)")
	pageSize := flag.Uint("page-size", 100, "page size for tabular data pagination")
	flag.Parse()

	if *sequenceID == "" {
		fmt.Fprintln(os.Stderr, "error: --sequence-id is required")
		flag.Usage()
		os.Exit(1)
	}
	if *authToken == "" {
		fmt.Fprintln(os.Stderr, "error: auth token required (set VIAM_AUTH_TOKEN in .env or use --token)")
		os.Exit(1)
	}

	ctx := context.Background()

	creds := credentials.NewClientTLSFromCert(nil, "")
	conn, err := grpc.NewClient(viamEndpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	tabularDir := filepath.Join(*outputDir, "tabular")
	imagesDir := filepath.Join(*outputDir, "images")
	for _, dir := range []string{tabularDir, imagesDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	manifestPath := filepath.Join(*outputDir, "manifest.json")
	progressPath := filepath.Join(*outputDir, "progress.json")

	m, err := loadManifest(manifestPath)
	if err != nil {
		log.Fatalf("load manifest: %v", err)
	}
	p, err := loadProgress(progressPath, *sequenceID)
	if err != nil {
		log.Fatalf("load progress: %v", err)
	}

	if p.tabularDone() && p.BinaryDone {
		fmt.Printf("Already complete (%d entries in manifest). Delete %s to re-download.\n", len(m.entries), progressPath)
		return
	}

	dl := newDownloader(conn, *authToken, m, p)

	fmt.Println("Downloading tabular data...")
	if err := dl.downloadTabular(ctx, *sequenceID, tabularDir, uint32(*pageSize)); err != nil {
		log.Fatalf("tabular: %v", err)
	}
	fmt.Printf("Tabular complete: %d total manifest entries\n\n", len(m.entries))

	fmt.Println("Downloading binary data...")
	if err := dl.downloadBinary(ctx, *sequenceID, imagesDir); err != nil {
		log.Fatalf("binary: %v", err)
	}
	fmt.Printf("Binary complete: %d total manifest entries\n\n", len(m.entries))

	fmt.Printf("Done. Manifest: %s\n", manifestPath)
}
