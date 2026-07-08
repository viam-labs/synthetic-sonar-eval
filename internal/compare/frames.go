// Package compare aligns screen1 screenshots with their nearest synthetic
// sonar renders by timestamp, runs the OmniDetector on both, and reports how
// fish-blob counts compare — a Go port of kongsberg-training-utils'
// compare_synthetic_vs_screenshot.py.
package compare

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"synthetic-sonar-eval/internal/detector"
	"synthetic-sonar-eval/internal/fetch"
)

// Screen1Component is the manifest componentName for the captain's-display
// screenshot stream.
const Screen1Component = "screen1"

// FanResources lists the four sonar fans, in the order they appear in
// montage panels (after screen1).
var FanResources = []string{
	"horizontal-h-sensor",
	"horizontal-h3-1-sensor",
	"horizontal-h3-2-sensor",
	"horizontal-h3-3-sensor",
}

// Frame is a single captured image (screenshot or render) with its capture
// time and, once detection has run, its fish-blob count.
type Frame struct {
	Path       string
	Timestamp  time.Time
	FishCount  int
	Detections []detector.Detection
}

// ParseTimeCaptured parses a manifest timeCaptured value such as
// "2026-06-16T12:21:24.999Z".
func ParseTimeCaptured(ts string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, ts)
}

// CollectScreen1Frames returns screen1 screenshots, derived from the
// manifest's binary entries, sorted by timestamp.
func CollectScreen1Frames(outputDir string, manifest []fetch.ManifestEntry) ([]*Frame, error) {
	var frames []*Frame
	for _, e := range manifest {
		if e.Type != "binary" || e.ComponentName != Screen1Component {
			continue
		}
		ts, err := ParseTimeCaptured(e.TimeCaptured)
		if err != nil {
			return nil, fmt.Errorf("parse timeCaptured %q: %w", e.TimeCaptured, err)
		}
		path := filepath.Join(outputDir, "images", Screen1Component, filepath.Base(e.Path))
		if _, err := os.Stat(path); err != nil {
			log.Printf("warning: screen1 frame missing on disk: %s", path)
			continue
		}
		frames = append(frames, &Frame{Path: path, Timestamp: ts})
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i].Timestamp.Before(frames[j].Timestamp) })
	return frames, nil
}

// CollectRenderFrames maps each tabular manifest record for one of
// FanResources to its rendered PNG (render writes
// sonar-images/<sensor>/<name>.png for each tabular/<sensor>/<name>.json,
// skipping empty grids, so a missing PNG here is expected, not an error).
func CollectRenderFrames(outputDir string, manifest []fetch.ManifestEntry) (map[string][]*Frame, error) {
	byFan := make(map[string][]*Frame, len(FanResources))
	fanSet := make(map[string]bool, len(FanResources))
	for _, fan := range FanResources {
		byFan[fan] = nil
		fanSet[fan] = true
	}

	for _, e := range manifest {
		if e.Type != "tabular" || !fanSet[e.ResourceName] {
			continue
		}
		base := filepath.Base(e.Path)
		pngName := strings.TrimSuffix(base, filepath.Ext(base)) + ".png"
		path := filepath.Join(outputDir, "sonar-images", e.ResourceName, pngName)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ts, err := ParseTimeCaptured(e.TimeCaptured)
		if err != nil {
			return nil, fmt.Errorf("parse timeCaptured %q: %w", e.TimeCaptured, err)
		}
		byFan[e.ResourceName] = append(byFan[e.ResourceName], &Frame{Path: path, Timestamp: ts})
	}
	for fan := range byFan {
		frs := byFan[fan]
		sort.Slice(frs, func(i, j int) bool { return frs[i].Timestamp.Before(frs[j].Timestamp) })
	}
	return byFan, nil
}

// NearestFrame returns the frame in frames whose timestamp is closest to
// target, or nil if frames is empty.
func NearestFrame(frames []*Frame, target time.Time) *Frame {
	if len(frames) == 0 {
		return nil
	}
	i := sort.Search(len(frames), func(i int) bool { return !frames[i].Timestamp.Before(target) })
	best := frames[min(i, len(frames)-1)]
	if i > 0 {
		if prev := frames[i-1]; absDuration(prev.Timestamp.Sub(target)) < absDuration(best.Timestamp.Sub(target)) {
			best = prev
		}
	}
	return best
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// MedianIntervalSeconds returns the median spacing between consecutive
// frames (used for skew warnings), defaulting to 1s when there are fewer
// than 2 frames.
func MedianIntervalSeconds(frames []*Frame) float64 {
	if len(frames) < 2 {
		return 1.0
	}
	deltas := make([]float64, 0, len(frames)-1)
	for i := 0; i < len(frames)-1; i++ {
		deltas = append(deltas, frames[i+1].Timestamp.Sub(frames[i].Timestamp).Seconds())
	}
	sort.Float64s(deltas)
	return deltas[len(deltas)/2]
}
