// Command compare runs the OmniDetector on real screen1 screenshots and on
// synthetic sonar renders from the same output directory, then reports how
// fish-blob counts compare between them: screen1 (a quad-ping composite) vs.
// fan_sum (the sum of the 4 single-fan renders, an over-count upper bound
// since a fish visible in multiple fans is counted twice).
//
// It aligns the five streams by timestamp (nearest-in-time), forming one
// frame-group per screen1 frame, runs detection once per distinct image,
// and writes counts.json + counts.csv plus (unless --no-visualize) annotated
// images, a per-group montage, and an MP4 of the montage sequence.
//
// This is a fidelity/sanity signal, not an accuracy metric: there is no
// ground truth, so count agreement does not imply either source is correct.
//
// A Go port of kongsberg-training-utils'
// src/omni_detector/compare_synthetic_vs_screenshot.py.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"synthetic-sonar-eval/internal/compare"
	"synthetic-sonar-eval/internal/detector"
	"synthetic-sonar-eval/internal/fetch"
)

const defaultFishClass = "human_annotated_positive_fish_blob"

func main() {
	outputDir := flag.String("output", "", "a synthetic-sonar-eval output directory (with manifest.json, images/screen1/, sonar-images/<fan>/) (required)")
	modelDir := flag.String("model-dir", "omni-detector-fcos-0_0_4", "directory containing model.onnx + labels.txt")
	confidence := flag.Float64("confidence", 0.6, "minimum detection confidence")
	fishClass := flag.String("fish-class", defaultFishClass, "class name counted as a fish blob")
	screenshotStripDist := flag.Float64("screenshot-strip-dist", 150.0, "background-strip distance (8-bit RGB) applied to screen1 screenshots only, matching the model's training/serving pipeline. Synthetic renders are never stripped. Pass 0 to disable stripping on screenshots.")
	resultsDirname := flag.String("results-dirname", "detector-eval", "subdirectory of --output for results")
	noVisualize := flag.Bool("no-visualize", false, "skip drawing annotated images/montages/video (counts only)")
	fps := flag.Int("fps", 3, "frame rate for the montage video")
	libPath := flag.String("onnxruntime-lib", "", "path to libonnxruntime.{dylib,so}; defaults to $ONNXRUNTIME_LIB_PATH or common install locations")
	flag.Parse()

	if *outputDir == "" {
		fmt.Fprintln(os.Stderr, "error: --output is required")
		flag.Usage()
		os.Exit(2)
	}

	resultsDir := filepath.Join(*outputDir, *resultsDirname)
	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", resultsDir, err)
	}

	manifest, err := loadManifest(*outputDir)
	if err != nil {
		log.Fatalf("%v", err)
	}

	screen1, err := compare.CollectScreen1Frames(*outputDir, manifest)
	if err != nil {
		log.Fatalf("collect screen1 frames: %v", err)
	}
	if len(screen1) == 0 {
		log.Fatalf("no screen1 frames found in manifest")
	}
	renders, err := compare.CollectRenderFrames(*outputDir, manifest)
	if err != nil {
		log.Fatalf("collect render frames: %v", err)
	}

	renderCounts := make(map[string]int, len(renders))
	for _, fan := range compare.FanResources {
		renderCounts[fan] = len(renders[fan])
	}
	fmt.Printf("Found %d screen1 frames; renders per fan: %v\n", len(screen1), renderCounts)

	lib, err := detector.ResolveLibPath(*libPath)
	if err != nil {
		log.Fatalf("%v", err)
	}
	det, err := detector.New(*modelDir, lib)
	if err != nil {
		log.Fatalf("load model from %s: %v", *modelDir, err)
	}
	defer det.Close()

	// Screenshots are stripped to match the model's training/serving
	// pipeline; renders are never stripped — they're expected to be
	// rendered with a matching (black) background instead.
	var stripDist *float64
	if *screenshotStripDist > 0 {
		stripDist = screenshotStripDist
	}

	if stripDist != nil {
		det.SetBackgroundStripDist(*stripDist)
	}
	fmt.Println("Running detection on screen1 frames...")
	if err := compare.RunDetectorOnFrames(det, screen1, float32(*confidence), *fishClass, "screen1"); err != nil {
		log.Fatalf("%v", err)
	}

	det.SetBackgroundStripDist(0)
	var renderFrames []*compare.Frame
	for _, fan := range compare.FanResources {
		renderFrames = append(renderFrames, renders[fan]...)
	}
	fmt.Println("Running detection on sonar renders...")
	if err := compare.RunDetectorOnFrames(det, renderFrames, float32(*confidence), *fishClass, "renders"); err != nil {
		log.Fatalf("%v", err)
	}

	skewWarn := 1.5 * compare.MedianIntervalSeconds(screen1)
	annotatedDir := filepath.Join(resultsDir, "annotated")
	montageDir := filepath.Join(resultsDir, "montages")

	if !*noVisualize {
		annotateTotal := len(screen1)
		for _, fan := range compare.FanResources {
			annotateTotal += len(renders[fan])
		}
		fmt.Printf("Drawing annotated images (%d total)...\n", annotateTotal)
		drawn := 0
		reportDraw := func() {
			drawn++
			if drawn%25 == 0 || drawn == annotateTotal {
				fmt.Printf("  [%d/%d] annotated\n", drawn, annotateTotal)
			}
		}
		for _, fr := range screen1 {
			out := annotatedPathFor(annotatedDir, compare.Screen1Component, fr)
			if err := compare.DrawAndSave(fr, out, "screen1", *fishClass); err != nil {
				log.Printf("warning: %v", err)
			}
			reportDraw()
		}
		for _, fan := range compare.FanResources {
			for _, fr := range renders[fan] {
				out := annotatedPathFor(annotatedDir, fan, fr)
				if err := compare.DrawAndSave(fr, out, fan, *fishClass); err != nil {
					log.Printf("warning: %v", err)
				}
				reportDraw()
			}
		}
	}

	// Fixed per-column widths so every montage frame in this run has
	// identical pixel dimensions (required to encode them into one video).
	const montageHeight = 600
	colWidths := make([]int, 1+len(compare.FanResources))
	if !*noVisualize {
		if len(screen1) > 0 {
			if w, err := compare.ColumnWidth(annotatedPathFor(annotatedDir, compare.Screen1Component, screen1[0]), montageHeight); err == nil {
				colWidths[0] = w
			}
		}
		for i, fan := range compare.FanResources {
			if frs := renders[fan]; len(frs) > 0 {
				if w, err := compare.ColumnWidth(annotatedPathFor(annotatedDir, fan, frs[0]), montageHeight); err == nil {
					colWidths[i+1] = w
				}
			}
		}
	}

	var groups []compare.Group
	totalScreen1, totalFanSum := 0, 0

	fmt.Printf("Building %d frame-group(s)...\n", len(screen1))
	for idx, s := range screen1 {
		if idx%25 == 0 || idx == len(screen1)-1 {
			fmt.Printf("  [%d/%d] group\n", idx+1, len(screen1))
		}
		fans := make(map[string]compare.FanResult, len(compare.FanResources))
		columns := make([]compare.MontageColumn, 1+len(compare.FanResources))
		columns[0] = compare.MontageColumn{Path: annotatedPathFor(annotatedDir, compare.Screen1Component, s)}
		fanSum := 0

		for fi, fan := range compare.FanResources {
			match := compare.NearestFrame(renders[fan], s.Timestamp)
			if match == nil {
				fans[fan] = compare.FanResult{Present: false}
				continue
			}
			delta := absSeconds(match.Timestamp.Sub(s.Timestamp))
			if delta > skewWarn {
				log.Printf("group %d fan %s: nearest render is %.2fs away (>%.2fs)", idx, fan, delta, skewWarn)
			}
			fanSum += match.FishCount
			rel, _ := filepath.Rel(*outputDir, match.Path)
			fans[fan] = compare.FanResult{
				Present:      true,
				Path:         rel,
				Timestamp:    match.Timestamp.Format(time.RFC3339Nano),
				DeltaSeconds: round3(delta),
				FishCount:    match.FishCount,
				Detections:   match.Detections,
			}
			columns[fi+1] = compare.MontageColumn{Path: annotatedPathFor(annotatedDir, fan, match)}
		}

		totalScreen1 += s.FishCount
		totalFanSum += fanSum

		relScreen1, _ := filepath.Rel(*outputDir, s.Path)
		groups = append(groups, compare.Group{
			GroupIndex: idx,
			Screen1: compare.FrameResult{
				Path:       relScreen1,
				Timestamp:  s.Timestamp.Format(time.RFC3339Nano),
				FishCount:  s.FishCount,
				Detections: s.Detections,
			},
			Fans:       fans,
			FanSumFish: fanSum,
		})

		if !*noVisualize {
			montagePath := filepath.Join(montageDir, fmt.Sprintf("group_%04d.png", idx))
			if err := compare.MakeMontage(columns, colWidths, montageHeight, montagePath); err != nil {
				log.Printf("warning: montage %d: %v", idx, err)
			}
		}
	}

	summary := compare.Summary{
		ModelDir:            *modelDir,
		Confidence:          *confidence,
		FishClass:           *fishClass,
		ScreenshotStripDist: stripDist,
		RenderStripDist:     nil,
		NGroups:             len(groups),
		TotalScreen1Fish:    totalScreen1,
		TotalFanSumFish:     totalFanSum,
		RendersPerFan:       renderCounts,
	}

	if err := compare.WriteCountsJSON(filepath.Join(resultsDir, "counts.json"), compare.CountsReport{Summary: summary, Groups: groups}); err != nil {
		log.Fatalf("write counts.json: %v", err)
	}
	if err := compare.WriteCountsCSV(filepath.Join(resultsDir, "counts.csv"), groups); err != nil {
		log.Fatalf("write counts.csv: %v", err)
	}

	if !*noVisualize {
		fmt.Println("Encoding montage video...")
		videoPath := filepath.Join(resultsDir, "montage.mp4")
		if err := compare.MakeMontageVideo(montageDir, videoPath, *fps); err != nil {
			log.Printf("warning: montage video: %v", err)
		} else {
			fmt.Printf("  wrote %s\n", videoPath)
		}
	}

	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
	fmt.Printf("Summary: %s\n", summaryJSON)
	fmt.Printf("Wrote results to %s\n", resultsDir)
}

func loadManifest(outputDir string) ([]fetch.ManifestEntry, error) {
	m, err := fetch.LoadManifest(filepath.Join(outputDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	entries := m.Entries()
	if len(entries) == 0 {
		return nil, fmt.Errorf("manifest.json not found or empty in %s", outputDir)
	}
	return entries, nil
}

func annotatedPathFor(annotatedDir, subdir string, fr *compare.Frame) string {
	return filepath.Join(annotatedDir, subdir, stem(fr.Path)+".png")
}

func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func absSeconds(d time.Duration) float64 {
	s := d.Seconds()
	if s < 0 {
		return -s
	}
	return s
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
