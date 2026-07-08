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

	"github.com/fullstorydev/grpcurl"
	"github.com/jhump/protoreflect/grpcreflect"
	datapb "go.viam.com/api/app/data/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/runtime/protoiface"
)

const (
	sequenceTabularMethod = "datamanagement.internalapi.v1.InternalDataService/GetSequenceTabularData"
	sequenceBinaryMethod  = "viam.app.data.v1.DataService/GetSequenceBinaryData"
)

// SequenceTabularResources are the sonar sensors pulled by sequence-mode
// downloads (mirroring the fixed set used everywhere else in this repo).
var SequenceTabularResources = []string{
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

type tabularResponse struct {
	DataPoints    []TabularDataPoint `json:"dataPoints"`
	NextPageToken string             `json:"nextPageToken"`
}

type captureMetadata struct {
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

type binaryMetadata struct {
	ID              string          `json:"id"`
	CaptureMetadata captureMetadata `json:"captureMetadata"`
	TimeRequested   string          `json:"timeRequested"`
	TimeReceived    string          `json:"timeReceived"`
	FileName        string          `json:"fileName"`
	FileExt         string          `json:"fileExt"`
	URI             string          `json:"uri"`
	BinaryDataID    string          `json:"binaryDataId"`
	FileSizeBytes   string          `json:"fileSizeBytes"`
}

type binaryDataItem struct {
	Metadata binaryMetadata `json:"metadata"`
}

type binaryResponse struct {
	Data          []binaryDataItem `json:"data"`
	NextPageToken string           `json:"nextPageToken"`
}

// --- Checkpoint state ---

type resourceProgress struct {
	NextPageToken string `json:"nextPageToken"`
	Done          bool   `json:"done"`
}

// SequenceProgress tracks where a sequence download left off so a failed run
// can resume.
type SequenceProgress struct {
	path string

	SequenceID          string                      `json:"sequenceId"`
	TabularResources    map[string]resourceProgress `json:"tabularResources"`
	BinaryNextPageToken string                      `json:"binaryNextPageToken"`
	BinaryDone          bool                        `json:"binaryDone"`
}

// TabularDone reports whether every resource in SequenceTabularResources has
// finished paginating, so callers can tell a fully-completed download apart
// from one that merely has a tabular/ directory (which downloadTabularResource
// creates as soon as the first data point for any resource arrives, well
// before pagination finishes).
func (p *SequenceProgress) TabularDone() bool {
	for _, r := range SequenceTabularResources {
		if !p.TabularResources[r].Done {
			return false
		}
	}
	return true
}

// LoadSequenceProgress reads an existing progress.json, or starts fresh if
// it belongs to a different sequence or doesn't exist yet.
func LoadSequenceProgress(path, sequenceID string) (*SequenceProgress, error) {
	p := &SequenceProgress{
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
		return &SequenceProgress{path: path, SequenceID: sequenceID, TabularResources: map[string]resourceProgress{}}, nil
	}
	if p.TabularResources == nil {
		p.TabularResources = map[string]resourceProgress{}
	}
	return p, nil
}

func (p *SequenceProgress) save() error {
	return atomicWriteJSON(p.path, p)
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

// SequenceDownloader fetches all tabular + binary data for a single sequence
// ID via Viam's internal sequence-based data API (grpcurl reflection, since
// that API isn't in the public go.viam.com/api proto set), with resumable
// progress and a deduplicating manifest.
type SequenceDownloader struct {
	conn       *grpc.ClientConn
	authToken  string
	authHeader string
	Manifest   *Manifest
	Progress   *SequenceProgress
}

func NewSequenceDownloader(conn *grpc.ClientConn, authToken string, m *Manifest, p *SequenceProgress) *SequenceDownloader {
	return &SequenceDownloader{
		conn:       conn,
		authToken:  authToken,
		authHeader: "Authorization: Bearer " + authToken,
		Manifest:   m,
		Progress:   p,
	}
}

func (d *SequenceDownloader) callGRPC(ctx context.Context, method, requestJSON string) ([]json.RawMessage, error) {
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

func (d *SequenceDownloader) DownloadTabular(ctx context.Context, sequenceID, outputDir string, pageSize uint32) error {
	for _, resourceName := range SequenceTabularResources {
		rp := d.Progress.TabularResources[resourceName]
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

func (d *SequenceDownloader) downloadTabularResource(ctx context.Context, sequenceID, outputDir string, pageSize uint32, resourceName, startToken string) error {
	pageToken := startToken
	total := 0

	for page := 1; ; page++ {
		req := map[string]any{
			"sequence_id": sequenceID,
			"page_size":   pageSize,
			"resource": map[string]any{
				"resource_name": resourceName,
				"method_name":   "Readings",
			},
		}
		if pageToken != "" {
			req["page_token"] = pageToken
		}
		reqJSON, _ := json.Marshal(req)

		responses, err := d.callGRPC(ctx, sequenceTabularMethod, string(reqJSON))
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}

		var nextToken string
		var pageEntries []ManifestEntry
		for _, raw := range responses {
			var resp tabularResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("parse tabular response: %w", err)
			}
			nextToken = resp.NextPageToken

			for _, dp := range resp.DataPoints {
				dir := filepath.Join(outputDir, SanitizeName(dp.ResourceName))
				if err := os.MkdirAll(dir, 0755); err != nil {
					return err
				}
				path := filepath.Join(dir, SanitizeTimestamp(dp.TimeCaptured)+".json")
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
		if err := d.Manifest.Add(pageEntries); err != nil {
			log.Printf("warning: manifest flush failed: %v", err)
		}
		total += len(pageEntries)
		d.Progress.TabularResources[resourceName] = resourceProgress{
			NextPageToken: nextToken,
			Done:          nextToken == "",
		}
		if err := d.Progress.save(); err != nil {
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

// DownloadBinary lists binary captures via the internal sequence API
// (grpcurl reflection, same as before), then fetches the actual image bytes
// via the public BinaryDataByIDs API in batches of binaryDataIDBatchSize —
// mirroring FetchImagesTimeRange's approach — instead of an individual HTTP
// GET per item's signed URI.
func (d *SequenceDownloader) DownloadBinary(ctx context.Context, sequenceID, outputDir string) error {
	if d.Progress.BinaryDone {
		fmt.Println("  binary: already complete, skipping")
		return nil
	}

	client := datapb.NewDataServiceClient(d.conn)
	authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+d.authToken)

	pageToken := d.Progress.BinaryNextPageToken
	total := 0

	for page := 1; ; page++ {
		req := map[string]any{"sequence_id": sequenceID}
		if pageToken != "" {
			req["page_token"] = pageToken
		}
		reqJSON, _ := json.Marshal(req)

		responses, err := d.callGRPC(ctx, sequenceBinaryMethod, string(reqJSON))
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}

		var nextToken string
		var metas []binaryMetadata
		for _, raw := range responses {
			var resp binaryResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return fmt.Errorf("parse binary response: %w", err)
			}
			nextToken = resp.NextPageToken
			for _, item := range resp.Data {
				metas = append(metas, item.Metadata)
			}
		}

		pageEntries, err := d.downloadBinaryBatch(authCtx, client, outputDir, metas)
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}

		if err := d.Manifest.Add(pageEntries); err != nil {
			log.Printf("warning: manifest flush failed: %v", err)
		}
		total += len(pageEntries)
		d.Progress.BinaryNextPageToken = nextToken
		if nextToken == "" {
			d.Progress.BinaryDone = true
		}
		if err := d.Progress.save(); err != nil {
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

// downloadBinaryBatch resolves each item's on-disk path up front (skipping
// ones already downloaded, for mid-page crash recovery), then fetches bytes
// for the rest in batches of binaryDataIDBatchSize via BinaryDataByIDs.
func (d *SequenceDownloader) downloadBinaryBatch(ctx context.Context, client datapb.DataServiceClient, outputDir string, metas []binaryMetadata) ([]ManifestEntry, error) {
	var pageEntries []ManifestEntry
	pathByID := make(map[string]string, len(metas))
	metaByID := make(map[string]binaryMetadata, len(metas))
	var pending []string

	for _, meta := range metas {
		dir := filepath.Join(outputDir, SanitizeName(meta.CaptureMetadata.ComponentName))
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}

		filename := meta.FileName
		if filename == "" {
			ext := meta.FileExt
			if ext == "" {
				ext = ".jpeg"
			}
			filename = SanitizeTimestamp(meta.TimeRequested) + ext
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

		pathByID[meta.BinaryDataID] = path
		metaByID[meta.BinaryDataID] = meta
		pending = append(pending, meta.BinaryDataID)
	}

	for i := 0; i < len(pending); i += binaryDataIDBatchSize {
		end := min(i+binaryDataIDBatchSize, len(pending))
		ids := pending[i:end]

		resp, err := client.BinaryDataByIDs(ctx, &datapb.BinaryDataByIDsRequest{
			IncludeBinary: true,
			BinaryDataIds: ids,
		})
		if err != nil {
			return nil, fmt.Errorf("BinaryDataByIDs: %w", err)
		}

		got := make(map[string]bool, len(ids))
		for _, item := range resp.Data {
			id := item.GetMetadata().GetBinaryDataId()
			path, ok := pathByID[id]
			if !ok {
				log.Printf("warning: BinaryDataByIDs returned unrequested id %s", id)
				continue
			}
			if err := os.WriteFile(path, item.Binary, 0644); err != nil {
				return nil, fmt.Errorf("write %s: %w", path, err)
			}
			meta := metaByID[id]
			pageEntries = append(pageEntries, ManifestEntry{
				Type:          "binary",
				Path:          path,
				TimeCaptured:  meta.TimeRequested,
				ComponentName: meta.CaptureMetadata.ComponentName,
			})
			got[id] = true
		}
		for _, id := range ids {
			if !got[id] {
				log.Printf("warning: BinaryDataByIDs did not return id %s", id)
			}
		}
	}

	return pageEntries, nil
}
