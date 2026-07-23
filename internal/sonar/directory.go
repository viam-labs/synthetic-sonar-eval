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
//
// signalFloorDB zeroes output signal below that display dB before writing
// and colorizing (display-style low-intensity suppression; the ping-ping
// blend history stays unfloored). Pass -100 or lower to disable.
//
// When params.CompositeMode is set, the horizontal-h3-* member streams are
// not rendered individually: their files are collected during the walk and
// rendered afterwards as one combined stream (horizontal-h3-composite) on
// the shared pixel window described by compositeWindow (see CompositeWindow).
// The composite always writes signal images (even with the ping-ping filter
// off).
func RenderDirectory(tabularDir, sonarImagesDir, signalImagesDir string, size int, params *RenderParams, pingPingLevel PingPingLevel, signalFloorDB float64, compositeWindow CompositeWindow) (rendered, skipped int, err error) {
	total, err := CountTabularFiles(tabularDir)
	if err != nil {
		return 0, 0, err
	}
	fmt.Printf("  found %d tabular file(s) to render\n", total)

	renderers := map[string]*PingPingRenderer{}
	signalFloor := SignalFloorGrayFromDB(signalFloorDB)
	compositeOn := params != nil && params.CompositeMode != ""
	var memberFiles []timedFile

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
		stream := rel
		if idx := strings.IndexRune(rel, filepath.Separator); idx >= 0 {
			stream = rel[:idx]
		}

		// Composite members are collected here and rendered as one combined
		// stream after the walk (the walk is lexicographic, so the member
		// streams' files never interleave and can't be grouped in-stream).
		if compositeOn && isCompositeMember(stream) {
			stem := strings.TrimSuffix(d.Name(), ".json")
			t, tsErr := parseStemTime(stem)
			if tsErr != nil {
				log.Printf("warning: composite: %s: %v", path, tsErr)
				return nil
			}
			memberFiles = append(memberFiles, timedFile{t: t, stream: stream, path: path, stem: stem})
			return nil
		}

		pngPath := filepath.Join(sonarImagesDir, strings.TrimSuffix(rel, ".json")+".png")

		if _, err := os.Stat(pngPath); err == nil {
			skipped++
			return nil
		}

		grid, loadErr := loadFanGrid(path)
		if loadErr != nil {
			log.Printf("warning: %v", loadErr)
			return nil
		}

		if grid.NBeams == 0 || grid.NSamples == 0 {
			log.Printf("warning: empty grid in %s, skipping", path)
			return nil
		}

		var img image.Image
		var signal *image.Gray
		if pingPingLevel == PingPingOff {
			var gray *image.Gray
			gray, err = RenderFanSampleGridGray(grid, size, params)
			if err == nil {
				img = ColorizeGray(applySignalFloor(gray, signalFloor), params)
			}
		} else {
			pr, ok := renderers[stream]
			if !ok {
				pr = &PingPingRenderer{Level: pingPingLevel, Params: params, SignalFloorGray: signalFloor}
				renderers[stream] = pr
			}
			img, signal, err = pr.Render(grid, size)
		}
		if err != nil {
			log.Printf("warning: render %s: %v", path, err)
			return nil
		}

		if err := writePNG(pngPath, img); err != nil {
			return err
		}

		if signal != nil && signalImagesDir != "" {
			signalPath := filepath.Join(signalImagesDir, strings.TrimSuffix(rel, ".json")+".png")
			if err := writePNG(signalPath, signal); err != nil {
				return err
			}
		}

		rendered++
		if rendered%100 == 0 {
			fmt.Printf("  rendered %d/%d images (%d skipped)\n", rendered, total, skipped)
		}
		return nil
	})
	if walkErr != nil {
		return rendered, skipped, walkErr
	}

	if compositeOn && len(memberFiles) > 0 {
		compRendered, compSkipped, compErr := renderCompositeFrames(
			memberFiles, sonarImagesDir, signalImagesDir, size, params, pingPingLevel, signalFloor, compositeWindow)
		rendered += compRendered
		skipped += compSkipped
		if compErr != nil {
			return rendered, skipped, compErr
		}
	}
	return rendered, skipped, nil
}

// loadFanGrid reads a RawRecord tabular JSON file and returns its
// FanSampleGrid payload.
func loadFanGrid(path string) (*FanSampleGrid, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var dp RawRecord
	if err := json.Unmarshal(data, &dp); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var payload struct {
		Readings FanSampleGrid `json:"readings"`
	}
	if err := json.Unmarshal(dp.Payload, &payload); err != nil {
		return nil, fmt.Errorf("parse payload %s: %w", path, err)
	}
	return &payload.Readings, nil
}

// writePNG writes img to path, creating parent directories as needed.
func writePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
