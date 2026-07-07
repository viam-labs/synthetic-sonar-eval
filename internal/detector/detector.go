// Package detector runs the OmniDetector ONNX model on a single image.
//
// It mirrors the preprocessing/decoding of the Python reference
// (omni_inference.OmniOnnxInference / kongsberg-training-utils'
// OmniDetector): PIL-equivalent bilinear resize to the model's fixed input
// size, uint8 NCHW input (no pixel normalization), and outputs unpacked by
// position (not name) since the ONNX export's output names are swapped:
// position 0 = boxes, position 1 = class indices (1-indexed), position 2 =
// confidence scores.
package detector

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	ort "github.com/yalue/onnxruntime_go"
	xdraw "golang.org/x/image/draw"
)

// Detection is a single detection with box coordinates normalized to [0, 1].
// Field names/JSON tags match the Detection shape already used by the
// Python eval pipeline's counts.json, so results are consistent wherever
// they're consumed (the placement-playback viewer, ad-hoc analysis, etc).
type Detection struct {
	ClassName  string  `json:"class_name"`
	Confidence float32 `json:"confidence"`
	XMin       float32 `json:"x_min"`
	YMin       float32 `json:"y_min"`
	XMax       float32 `json:"x_max"`
	YMax       float32 `json:"y_max"`
}

// Detector holds a loaded ONNX session and label set for repeated inference.
type Detector struct {
	session     *ort.DynamicAdvancedSession
	labels      []string
	inputWidth  int64
	inputHeight int64
}

// New loads model.onnx + labels.txt from modelDir and starts an ONNX
// runtime session. onnxLibPath is the path to the onnxruntime shared library
// (e.g. libonnxruntime.dylib); pass "" if the environment was already
// initialized (e.g. via ort.SetSharedLibraryPath) by the caller.
func New(modelDir, onnxLibPath string) (*Detector, error) {
	if onnxLibPath != "" {
		ort.SetSharedLibraryPath(onnxLibPath)
	}
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("initialize onnxruntime: %w", err)
		}
	}

	modelPath := filepath.Join(modelDir, "model.onnx")
	labels, err := loadLabels(filepath.Join(modelDir, "labels.txt"))
	if err != nil {
		return nil, err
	}

	inputs, outputs, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("inspect model %s: %w", modelPath, err)
	}
	if len(inputs) != 1 || len(inputs[0].Dimensions) != 4 {
		return nil, fmt.Errorf("expected a single 4D [N,C,H,W] model input, got %v", inputs)
	}
	if len(outputs) != 3 {
		return nil, fmt.Errorf("expected exactly 3 model outputs (boxes, labels, scores), got %d", len(outputs))
	}

	inputName := inputs[0].Name
	outputNames := []string{outputs[0].Name, outputs[1].Name, outputs[2].Name}

	session, err := ort.NewDynamicAdvancedSession(modelPath, []string{inputName}, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("create onnx session: %w", err)
	}

	return &Detector{
		session:     session,
		labels:      labels,
		inputWidth:  inputs[0].Dimensions[3],
		inputHeight: inputs[0].Dimensions[2],
	}, nil
}

// Close releases the underlying ONNX session.
func (d *Detector) Close() error {
	return d.session.Destroy()
}

// Detect runs the full pipeline (resize -> uint8 NCHW -> inference -> decode)
// on a single image file, keeping only detections at or above minConfidence.
func (d *Detector) Detect(imagePath string, minConfidence float32) ([]Detection, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", err)
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("decode image %s: %w", imagePath, err)
	}
	return d.DetectImage(src, minConfidence)
}

// DetectImage runs the full pipeline (resize -> uint8 NCHW -> inference ->
// decode) on an already-decoded image, keeping only detections at or above
// minConfidence. Useful for callers that already have an image.Image in hand
// (e.g. a freshly rendered sonar frame) and want to skip an encode/decode
// round-trip through disk.
func (d *Detector) DetectImage(src image.Image, minConfidence float32) ([]Detection, error) {
	input, err := d.preprocess(src)
	if err != nil {
		return nil, fmt.Errorf("preprocess image: %w", err)
	}
	defer input.Destroy()

	outVals := make([]ort.Value, 3)
	if err := d.session.Run([]ort.Value{input}, outVals); err != nil {
		return nil, fmt.Errorf("run inference: %w", err)
	}
	defer func() {
		for _, v := range outVals {
			if v != nil {
				v.Destroy()
			}
		}
	}()

	boxesT, ok := outVals[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected type for boxes output: %T", outVals[0])
	}
	labelIdxT, ok := outVals[1].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected type for label-index output: %T", outVals[1])
	}
	scoresT, ok := outVals[2].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected type for scores output: %T", outVals[2])
	}

	return d.decode(boxesT.GetData(), labelIdxT.GetData(), scoresT.GetData(), minConfidence), nil
}

// preprocess resizes src to the model's fixed input size with bilinear
// interpolation and packs it into a uint8 [1, 3, H, W] tensor (RGB, CHW).
func (d *Detector) preprocess(src image.Image) (*ort.Tensor[uint8], error) {
	w, h := int(d.inputWidth), int(d.inputHeight)
	resized := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(resized, resized.Bounds(), src, src.Bounds(), xdraw.Src, nil)

	chw := make([]uint8, 3*h*w)
	planeSize := h * w
	i := 0
	for y := 0; y < h; y++ {
		rowOff := resized.PixOffset(0, y)
		row := resized.Pix[rowOff : rowOff+4*w]
		for x := 0; x < w; x++ {
			chw[0*planeSize+i] = row[4*x+0]
			chw[1*planeSize+i] = row[4*x+1]
			chw[2*planeSize+i] = row[4*x+2]
			i++
		}
	}

	return ort.NewTensor(ort.NewShape(1, 3, d.inputHeight, d.inputWidth), chw)
}

// decode mirrors OmniOnnxInference._decode: class indices are 1-indexed
// (index 0 is reserved for background), and boxes come out in the model's
// input coordinate space, so they're normalized to [0, 1] by input size.
func (d *Detector) decode(boxes, labelIdx, scores []float32, minConfidence float32) []Detection {
	normW := float32(d.inputWidth)
	normH := float32(d.inputHeight)

	dets := make([]Detection, 0, len(scores))
	for i, score := range scores {
		if score < minConfidence {
			continue
		}

		catIdx := int(labelIdx[i]+0.5) - 1
		className := fmt.Sprintf("%d", catIdx+1)
		if catIdx >= 0 && catIdx < len(d.labels) {
			className = d.labels[catIdx]
		}

		dets = append(dets, Detection{
			ClassName:  className,
			Confidence: score,
			XMin:       boxes[4*i+0] / normW,
			YMin:       boxes[4*i+1] / normH,
			XMax:       boxes[4*i+2] / normW,
			YMax:       boxes[4*i+3] / normH,
		})
	}
	return dets
}

func loadLabels(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read labels: %w", err)
	}
	var labels []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			labels = append(labels, line)
		}
	}
	return labels, nil
}

// candidateLibPaths are checked (in order) by ResolveLibPath when no explicit
// path or environment variable is given.
var candidateLibPaths = []string{
	"/opt/homebrew/lib/libonnxruntime.dylib",
	"/usr/local/lib/libonnxruntime.dylib",
	"/opt/homebrew/lib/libonnxruntime.so",
	"/usr/local/lib/libonnxruntime.so",
	"/usr/lib/libonnxruntime.so",
}

// ResolveLibPath returns the onnxruntime shared library path to use,
// checking (in order) explicitPath, the ONNXRUNTIME_LIB_PATH env var, and a
// list of common install locations.
func ResolveLibPath(explicitPath string) (string, error) {
	if explicitPath != "" {
		return explicitPath, nil
	}
	if env := os.Getenv("ONNXRUNTIME_LIB_PATH"); env != "" {
		return env, nil
	}
	for _, p := range candidateLibPaths {
		if _, err := os.Stat(p); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs, nil
			}
			return p, nil
		}
	}
	return "", fmt.Errorf(
		"could not find libonnxruntime; install it with `brew install onnxruntime` " +
			"or pass an explicit path (or set ONNXRUNTIME_LIB_PATH)",
	)
}
