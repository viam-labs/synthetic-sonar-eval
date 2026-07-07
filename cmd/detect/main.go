// Command detect runs the OmniDetector ONNX model on a single image, or on
// every image found (recursively) under a directory, and prints the
// detections found (fish blobs by default).
//
// Requires the onnxruntime shared library. Install it with
// `brew install onnxruntime` or point --onnxruntime-lib / ONNXRUNTIME_LIB_PATH
// at a libonnxruntime.{dylib,so} you already have.
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"synthetic-sonar-eval/internal/detector"
)

func main() {
	modelDir := flag.String("model-dir", "omni-detector-fcos-0_0_4", "directory containing model.onnx + labels.txt")
	imagePath := flag.String("image", "", "path to an image, or a directory of images, to run detection on (required)")
	confidence := flag.Float64("confidence", 0.6, "minimum detection confidence")
	classFilter := flag.String("class", "", "if set, only print/count detections of this class name")
	libPath := flag.String("onnxruntime-lib", "", "path to libonnxruntime.{dylib,so}; defaults to $ONNXRUNTIME_LIB_PATH or common install locations")
	tensorBatchSize := flag.Int("tensor-batch-size", 1, "EXPERIMENTAL (directory input only): stack this many images into a "+
		"single real ONNX batch tensor per Run() call instead of one image at a time. This model's input has its batch "+
		"dimension fixed at 1, so any value >1 is expected to fail — this flag exists to reproduce/inspect that failure "+
		"directly (see internal/detector.Detector.DetectBatch)")
	flag.Parse()

	if *imagePath == "" {
		fmt.Fprintln(os.Stderr, "error: --image is required")
		flag.Usage()
		os.Exit(2)
	}

	info, err := os.Stat(*imagePath)
	if err != nil {
		log.Fatalf("stat %s: %v", *imagePath, err)
	}

	var paths []string
	isDir := info.IsDir()
	if isDir {
		paths, err = collectImagePaths(*imagePath)
		if err != nil {
			log.Fatalf("walk %s: %v", *imagePath, err)
		}
		if len(paths) == 0 {
			log.Fatalf("no image files found under %s", *imagePath)
		}
		fmt.Printf("found %d image(s) under %s\n", len(paths), *imagePath)
	} else {
		paths = []string{*imagePath}
	}

	lib, err := detector.ResolveLibPath(*libPath)
	if err != nil {
		log.Fatalf("%v", err)
	}

	d, err := detector.New(*modelDir, lib)
	if err != nil {
		log.Fatalf("load model from %s: %v", *modelDir, err)
	}
	defer d.Close()

	if *tensorBatchSize > 1 {
		if !isDir {
			log.Fatalf("--tensor-batch-size requires a directory of images (got a single image)")
		}
		runTensorBatchExperiment(d, paths, *tensorBatchSize, float32(*confidence))
		return
	}

	start := time.Now()
	imagesWithDetections := 0
	totalDetections := 0

	for i, p := range paths {
		imgStart := time.Now()
		dets, err := d.Detect(p, float32(*confidence))
		elapsed := time.Since(imgStart)
		if err != nil {
			log.Printf("warning: detect on %s: %v", p, err)
			continue
		}

		var kept []detector.Detection
		for _, det := range dets {
			if *classFilter == "" || det.ClassName == *classFilter {
				kept = append(kept, det)
			}
		}

		if isDir {
			fmt.Printf("[%d/%d] %s (%.3fs) — %d detection(s)\n", i+1, len(paths), p, elapsed.Seconds(), len(kept))
		}
		for _, det := range kept {
			fmt.Printf("  %-40s conf=%.3f box=[%.4f, %.4f, %.4f, %.4f]\n",
				det.ClassName, det.Confidence, det.XMin, det.YMin, det.XMax, det.YMax)
		}
		if len(kept) > 0 {
			imagesWithDetections++
			totalDetections += len(kept)
		}
	}

	totalElapsed := time.Since(start)
	perImage := totalElapsed / time.Duration(len(paths))
	fmt.Printf("%d image(s) processed, %d with detection(s) (%d total detection(s)), in %s (%s/image)\n",
		len(paths), imagesWithDetections, totalDetections, totalElapsed.Round(time.Millisecond), perImage.Round(time.Millisecond))
}

// runTensorBatchExperiment groups paths into chunks of batchSize and attempts
// to run each chunk through the model as a single real batched ONNX tensor
// (shape [batchSize, 3, H, W]) via Detector.DetectBatch, instead of one image
// at a time. This model's ONNX input has its batch dimension fixed at 1, so
// this is expected to fail on the very first chunk — it exists to reproduce
// that failure directly from Go, not as a working batching path.
func runTensorBatchExperiment(d *detector.Detector, paths []string, batchSize int, minConfidence float32) {
	numBatches := (len(paths) + batchSize - 1) / batchSize
	fmt.Printf("EXPERIMENT: attempting real tensor batching with batch size %d across %d batch(es) "+
		"(expected to fail — this model's ONNX input has its batch dimension fixed at 1)\n", batchSize, numBatches)

	for i := 0; i < len(paths); i += batchSize {
		end := min(i+batchSize, len(paths))
		chunk := paths[i:end]

		imgs := make([]image.Image, 0, len(chunk))
		for _, p := range chunk {
			f, err := os.Open(p)
			if err != nil {
				log.Fatalf("open %s: %v", p, err)
			}
			img, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				log.Fatalf("decode %s: %v", p, err)
			}
			imgs = append(imgs, img)
		}

		fmt.Printf("[batch %d/%d] stacking %d image(s):\n", i/batchSize+1, numBatches, len(imgs))
		for _, p := range chunk {
			fmt.Printf("  - %s\n", p)
		}

		if _, err := d.DetectBatch(imgs, minConfidence); err != nil {
			log.Fatalf("batched detect failed: %v", err)
		}
	}
}

// collectImagePaths recursively finds image files (by extension) under dir,
// sorted for stable, reproducible progress output.
func collectImagePaths(dir string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && isImageFile(path) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func isImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}
