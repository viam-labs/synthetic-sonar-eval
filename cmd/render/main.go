package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"synthetic-sonar-eval/internal/sonar"
)

func main() {
	outputDir := flag.String("output", "output", "output directory (same as used for download)")
	tabularDir := flag.String("tabular", "", "tabular JSON directory (default: <output>/tabular)")
	size := flag.Int("size", 0, "image size in pixels (0 = default 1500)")
	fps := flag.Int("fps", 3, "video frame rate")
	paramsFile := flag.String("params", "", "optional JSON file with render params (dbPerCount, dbWindow, dbDisplayHeadroom, heatmapRangeSigmaFactor, heatmapArcSigmaFactor, heatmapMinThreshold, colorStops)")
	flag.Parse()

	var renderParams *sonar.RenderParams
	if *paramsFile != "" {
		p := sonar.DefaultRenderParams()
		data, err := os.ReadFile(*paramsFile)
		if err != nil {
			log.Fatalf("read params file: %v", err)
		}
		if err := json.Unmarshal(data, &p); err != nil {
			log.Fatalf("parse params file: %v", err)
		}
		renderParams = &p
	}

	tabularRoot := *tabularDir
	if tabularRoot == "" {
		tabularRoot = filepath.Join(*outputDir, "tabular")
	}
	sonarImagesDir := filepath.Join(*outputDir, "sonar-images")
	binaryImagesDir := filepath.Join(*outputDir, "images")

	if err := os.RemoveAll(sonarImagesDir); err != nil {
		log.Fatalf("clear %s: %v", sonarImagesDir, err)
	}
	if err := os.MkdirAll(sonarImagesDir, 0755); err != nil {
		log.Fatalf("mkdir %s: %v", sonarImagesDir, err)
	}

	fmt.Println("Rendering sonar images...")
	rendered, skipped, err := renderSonarImages(tabularRoot, sonarImagesDir, *size, renderParams)
	if err != nil {
		log.Fatalf("render: %v", err)
	}
	fmt.Printf("  %d rendered, %d skipped\n\n", rendered, skipped)

	fmt.Println("Creating videos...")
	videos, err := createVideos(sonarImagesDir, binaryImagesDir, *fps)
	if err != nil {
		log.Fatalf("video: %v", err)
	}

	fmt.Println("Creating paired videos...")
	if err := createPairedVideos(videos, *outputDir); err != nil {
		log.Printf("warning: pair: %v", err)
	}
	fmt.Println("Done.")
}

// tabularDataPoint is the minimal shape of each tabular JSON file.
type tabularDataPoint struct {
	ResourceName string          `json:"resourceName"`
	TimeCaptured string          `json:"timeCaptured"`
	Payload      json.RawMessage `json:"payload"`
}

// countTabularFiles counts the .json tabular files under tabularDir, so
// renderSonarImages can report progress as "x/total" rather than just a
// running count.
func countTabularFiles(tabularDir string) (int, error) {
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

func renderSonarImages(tabularDir, sonarImagesDir string, size int, params *sonar.RenderParams) (rendered, skipped int, err error) {
	total, err := countTabularFiles(tabularDir)
	if err != nil {
		return 0, 0, err
	}
	fmt.Printf("  found %d tabular file(s) to render\n", total)

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

		var dp tabularDataPoint
		if err := json.Unmarshal(data, &dp); err != nil {
			log.Printf("warning: parse %s: %v", path, err)
			return nil
		}

		var payload struct {
			Readings sonar.FanSampleGrid `json:"readings"`
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

		img, err := sonar.RenderFanSampleGrid(grid, size, params)
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

		rendered++
		if rendered%100 == 0 {
			fmt.Printf("  rendered %d/%d images (%d skipped)\n", rendered, total, skipped)
		}
		return nil
	})
	return rendered, skipped, walkErr
}

// createVideos makes an MP4 for every image subdirectory under sonarImagesDir
// and binaryImagesDir, returning the paths of successfully created videos.
func createVideos(sonarImagesDir, binaryImagesDir string, fps int) ([]string, error) {
	var videos []string
	for _, base := range []string{sonarImagesDir, binaryImagesDir} {
		entries, err := os.ReadDir(base)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			inputDir := filepath.Join(base, e.Name())
			outputPath := filepath.Join(base, e.Name()+".mp4")
			fmt.Printf("  %s → %s\n", inputDir, outputPath)
			if err := makeVideo(inputDir, outputPath, fps); err != nil {
				log.Printf("warning: video %s: %v", outputPath, err)
			} else {
				videos = append(videos, outputPath)
			}
		}
	}
	return videos, nil
}

// createPairedVideos finds screen1.mp4 among the generated videos and creates
// a side-by-side MP4 for each other video paired with it.
func createPairedVideos(videos []string, outputDir string) error {
	var screen1 string
	var others []string
	for _, v := range videos {
		if strings.TrimSuffix(filepath.Base(v), ".mp4") == "screen1" {
			screen1 = v
		} else {
			others = append(others, v)
		}
	}
	if screen1 == "" {
		return fmt.Errorf("no screen1 video found among generated videos")
	}
	if len(others) == 0 {
		return nil
	}

	_, h, err := probeVideoSize(screen1)
	if err != nil {
		return fmt.Errorf("probe screen1: %w", err)
	}

	pairedDir := filepath.Join(outputDir, "paired")
	if err := os.MkdirAll(pairedDir, 0755); err != nil {
		return err
	}

	for _, v := range others {
		outPath := filepath.Join(pairedDir, filepath.Base(v))
		useLHS := screen1UsesLHS(v)
		side := "RHS"
		if useLHS {
			side = "LHS"
		}
		fmt.Printf("  screen1 (%s) + %s → %s\n", side, filepath.Base(v), outPath)
		if err := makeSideBySide(screen1, v, outPath, h, useLHS); err != nil {
			log.Printf("warning: pair %s: %v", filepath.Base(v), err)
		}
	}
	return nil
}

// screen1UsesLHS reports whether a sonar video should be paired with the left
// half of the screen1 feed. horizontal-h-sensor uses LHS; horizontal-h3-* use RHS.
func screen1UsesLHS(sonarVideoPath string) bool {
	base := strings.TrimSuffix(filepath.Base(sonarVideoPath), ".mp4")
	return strings.HasPrefix(base, "horizontal-h") && !strings.HasPrefix(base, "horizontal-h3")
}

// makeSideBySide places a cropped screen1 feed and sonarPath side by side, both
// scaled to the given height (even-rounded), and encodes to outputPath.
// useLHS selects the left half of screen1; otherwise the right half is used.
func makeSideBySide(screenPath, sonarPath, outputPath string, height int, useLHS bool) error {
	h := height &^ 1 // round down to even for x264
	crop := "crop=iw/2:ih:iw/2:0"
	if useLHS {
		crop = "crop=iw/2:ih:0:0"
	}
	filter := fmt.Sprintf("[0:v]%s,scale=-2:%d[v0];[1:v]scale=-2:%d[v1];[v0][v1]hstack=inputs=2[v]", crop, h, h)
	cmd := exec.Command("ffmpeg", "-y",
		"-i", screenPath,
		"-i", sonarPath,
		"-filter_complex", filter,
		"-map", "[v]",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-crf", "18",
		outputPath,
	)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func probeVideoSize(path string) (w, h int, err error) {
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0",
		path,
	).Output()
	if err != nil {
		return 0, 0, err
	}
	parts := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected ffprobe output: %q", out)
	}
	w, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err = strconv.Atoi(parts[1])
	return w, h, err
}


// makeVideo encodes all images in inputDir into an MP4 at the given frame rate.
// It uses sequential symlinks to handle filenames with special characters.
func makeVideo(inputDir, outputPath string, fps int) error {
	absInputDir, err := filepath.Abs(inputDir)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(absInputDir)
	if err != nil {
		return err
	}

	var files []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && isImageFile(name) {
			files = append(files, filepath.Join(absInputDir, name))
		}
	}
	sort.Strings(files)

	if len(files) == 0 {
		return fmt.Errorf("no image files in %s", inputDir)
	}

	tmpDir, err := os.MkdirTemp("", "sonar-video-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	ext := filepath.Ext(files[0])
	for i, f := range files {
		if err := os.Symlink(f, filepath.Join(tmpDir, fmt.Sprintf("%06d%s", i, ext))); err != nil {
			return err
		}
	}

	cmd := exec.Command("ffmpeg", "-y",
		"-framerate", fmt.Sprintf("%d", fps),
		"-i", filepath.Join(tmpDir, fmt.Sprintf("%%06d%s", ext)),
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-crf", "18",
		outputPath,
	)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func isImageFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}
