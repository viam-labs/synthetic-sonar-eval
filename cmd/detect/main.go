// Command detect runs the OmniDetector ONNX model on a single image and
// prints the detections found (fish blobs by default).
//
// Requires the onnxruntime shared library. Install it with
// `brew install onnxruntime` or point --onnxruntime-lib / ONNXRUNTIME_LIB_PATH
// at a libonnxruntime.{dylib,so} you already have.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"synthetic-sonar-eval/internal/detector"
)

func main() {
	modelDir := flag.String("model-dir", "omni-detector-fcos-0_0_4", "directory containing model.onnx + labels.txt")
	image := flag.String("image", "", "path to the image to run detection on (required)")
	confidence := flag.Float64("confidence", 0.6, "minimum detection confidence")
	classFilter := flag.String("class", "", "if set, only print detections of this class name")
	libPath := flag.String("onnxruntime-lib", "", "path to libonnxruntime.{dylib,so}; defaults to $ONNXRUNTIME_LIB_PATH or common install locations")
	flag.Parse()

	if *image == "" {
		fmt.Fprintln(os.Stderr, "error: --image is required")
		flag.Usage()
		os.Exit(2)
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

	dets, err := d.Detect(*image, float32(*confidence))
	if err != nil {
		log.Fatalf("detect: %v", err)
	}

	count := 0
	for _, det := range dets {
		if *classFilter != "" && det.ClassName != *classFilter {
			continue
		}
		count++
		fmt.Printf("%-40s conf=%.3f box=[%.4f, %.4f, %.4f, %.4f]\n",
			det.ClassName, det.Confidence, det.XMin, det.YMin, det.XMax, det.YMax)
	}
	fmt.Printf("%d detection(s)\n", count)
}
