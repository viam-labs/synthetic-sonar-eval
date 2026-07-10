package sonar

import (
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// RawRecord is the on-disk shape of a single downloaded tabular reading —
// shared by every download mode (sequence-based and time-range-based) so
// RenderDirectory can render either source uniformly.
type RawRecord struct {
	ResourceName string          `json:"resourceName"`
	TimeCaptured string          `json:"timeCaptured"`
	Payload      json.RawMessage `json:"payload"`
}

// CountTabularFiles counts the .json tabular files under tabularDir, so
// RenderDirectory can report progress as "x/total" rather than just a
// running count.
func CountTabularFiles(tabularDir string) (int, error) {
	total := 0
	err := filepath.WalkDir(tabularDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			total++
		}
		return nil
	})
	return total, err
}

// RenderDirectory walks tabularDir for RawRecord JSON files and renders each
// to a heatmap PNG under sonarImagesDir (mirroring the relative path, with
// ".json" swapped for ".png"), skipping files that already have a rendered
// PNG. Progress is logged to stdout as it goes.
//
// When pingPingLevel != PingPingOff, each top-level subdirectory of
// tabularDir (i.e. each sensor/resource stream) gets its own PingPingRenderer,
// fed pings in the filesystem walk order — which relies on filenames sorting
// chronologically within a stream. Pre-existing PNGs are still skipped for
// resumability, but a skipped ping does not feed the filter's history, so
// resuming a partially-rendered, ping-ping-filtered directory will restart
// history at the first re-rendered ping rather than being frame-perfect.
//
// If signalImagesDir is non-empty and pingPingLevel != PingPingOff, the
// blended grayscale signal image the filter ran on (before colorizing) is
// also written there, mirroring the same relative path.
func RenderDirectory(tabularDir, sonarImagesDir, signalImagesDir string, size int, params *RenderParams, pingPingLevel PingPingLevel) (rendered, skipped int, err error) {
	total, err := CountTabularFiles(tabularDir)
	if err != nil {
		return 0, 0, err
	}
	fmt.Printf("  found %d tabular file(s) to render\n", total)

	renderers := map[string]*PingPingRenderer{}

	walkErr := filepath.WalkDir(tabularDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}

		rel, err := filepath.Rel(tabularDir, path)
		if err != nil {
			return err
		}
		pngPath := filepath.Join(sonarImagesDir, strings.TrimSuffix(rel, ".json")+".png")

		if _, err := os.Stat(pngPath); err == nil {
			skipped++
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("warning: read %s: %v", path, err)
			return nil
		}

		var dp RawRecord
		if err := json.Unmarshal(data, &dp); err != nil {
			log.Printf("warning: parse %s: %v", path, err)
			return nil
		}

		var payload struct {
			Readings FanSampleGrid `json:"readings"`
		}
		if err := json.Unmarshal(dp.Payload, &payload); err != nil {
			log.Printf("warning: parse payload %s: %v", path, err)
			return nil
		}
		grid := &payload.Readings

		if grid.NBeams == 0 || grid.NSamples == 0 {
			log.Printf("warning: empty grid in %s, skipping", path)
			return nil
		}

		var img image.Image
		var signal *image.Gray
		if pingPingLevel == PingPingOff {
			img, err = RenderFanSampleGrid(grid, size, params)
		} else {
			stream := rel
			if idx := strings.IndexRune(rel, filepath.Separator); idx >= 0 {
				stream = rel[:idx]
			}
			pr, ok := renderers[stream]
			if !ok {
				pr = &PingPingRenderer{Level: pingPingLevel, Params: params}
				renderers[stream] = pr
			}
			img, signal, err = pr.Render(grid, size)
		}
		if err != nil {
			log.Printf("warning: render %s: %v", path, err)
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(pngPath), 0755); err != nil {
			return err
		}
		f, err := os.Create(pngPath)
		if err != nil {
			return err
		}
		if encErr := png.Encode(f, img); encErr != nil {
			f.Close()
			return encErr
		}
		f.Close()

		if signal != nil && signalImagesDir != "" {
			signalPath := filepath.Join(signalImagesDir, strings.TrimSuffix(rel, ".json")+".png")
			if err := os.MkdirAll(filepath.Dir(signalPath), 0755); err != nil {
				return err
			}
			sf, err := os.Create(signalPath)
			if err != nil {
				return err
			}
			if encErr := png.Encode(sf, signal); encErr != nil {
				sf.Close()
				return encErr
			}
			sf.Close()
		}

		rendered++
		if rendered%100 == 0 {
			fmt.Printf("  rendered %d/%d images (%d skipped)\n", rendered, total, skipped)
		}
		return nil
	})
	return rendered, skipped, walkErr
}
