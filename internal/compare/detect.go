package compare

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"synthetic-sonar-eval/internal/detector"
)

// RunDetectorOnFrames runs det on every distinct frame path (deduplicating
// repeated paths, running each image exactly once) and populates
// Detections/FishCount on every frame in place. label is printed with each
// progress line (e.g. "screen1", "sonar renders") to distinguish concurrent
// passes in the CLI's output.
func RunDetectorOnFrames(det *detector.Detector, frames []*Frame, minConfidence float32, fishClass, label string) error {
	pathSet := make(map[string]bool)
	for _, f := range frames {
		pathSet[f.Path] = true
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	results := make(map[string][]detector.Detection, len(paths))
	start := time.Now()
	for i, p := range paths {
		imgStart := time.Now()
		dets, err := det.Detect(p, minConfidence)
		if err != nil {
			return fmt.Errorf("detect on %s: %w", p, err)
		}
		results[p] = dets

		kept := 0
		for _, d := range dets {
			if d.ClassName == fishClass {
				kept++
			}
		}
		elapsed := time.Since(imgStart)
		fmt.Printf("  [%s %d/%d] %s (%.3fs) — %d fish\n", label, i+1, len(paths), filepath.Base(p), elapsed.Seconds(), kept)
	}
	fmt.Printf("  %s: %d image(s) in %s\n", label, len(paths), time.Since(start).Round(time.Millisecond))

	for _, f := range frames {
		dets := results[f.Path]
		f.Detections = dets
		count := 0
		for _, d := range dets {
			if d.ClassName == fishClass {
				count++
			}
		}
		f.FishCount = count
	}
	return nil
}
