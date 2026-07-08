package compare

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

var (
	boxColor = color.RGBA{0, 255, 0, 255} // green, matching the Python tool's BOX_COLOR
	headerBG = color.RGBA{0, 0, 0, 255}
	headerFG = color.RGBA{255, 255, 255, 255}
)

// DrawAndSave draws fish-blob boxes plus a "<label>: N fish" header onto
// frame's image and writes the annotated copy to outPath.
func DrawAndSave(frame *Frame, outPath, label, fishClass string) error {
	src, err := loadImage(frame.Path)
	if err != nil {
		return fmt.Errorf("read %s for visualisation: %w", frame.Path, err)
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), src, b.Min, draw.Src)

	for _, det := range frame.Detections {
		if det.ClassName != fishClass {
			continue
		}
		x1 := int(det.XMin * float32(w))
		y1 := int(det.YMin * float32(h))
		x2 := int(det.XMax * float32(w))
		y2 := int(det.YMax * float32(h))
		drawRectOutline(img, x1, y1, x2, y2, boxColor, 2)
		drawText(img, fmt.Sprintf("%.2f", det.Confidence), x1, max(0, y1-4), boxColor)
	}

	fillRect(img, 0, 0, w, 34, headerBG)
	drawText(img, fmt.Sprintf("%s: %d fish", label, frame.FishCount), 8, 24, headerFG)

	return saveImage(img, outPath)
}

// MontageColumn is one column of a montage: its annotated panel path, or ""
// if this group has no frame for this column (rendered as black instead of
// narrowing the montage, so every montage in a run has identical pixel
// dimensions — required to encode them into a single video).
type MontageColumn struct {
	Path string
}

// ColumnWidth returns the width a panel at path would occupy once scaled to
// height, preserving aspect ratio.
func ColumnWidth(path string, height int) (int, error) {
	img, err := loadImage(path)
	if err != nil {
		return 0, err
	}
	b := img.Bounds()
	return max(1, b.Dx()*height/b.Dy()), nil
}

// MakeMontage stitches columns horizontally, each at its fixed colWidths[i]
// (see ColumnWidth) and a common height, and writes the result to outPath.
// A column with an empty Path is left black.
func MakeMontage(columns []MontageColumn, colWidths []int, height int, outPath string) error {
	totalWidth := 0
	for _, w := range colWidths {
		totalWidth += w
	}
	if totalWidth == 0 {
		return nil
	}

	out := image.NewRGBA(image.Rect(0, 0, totalWidth, height))
	xOff := 0
	for i, col := range columns {
		w := colWidths[i]
		if w > 0 && col.Path != "" {
			if panel, err := loadImage(col.Path); err == nil {
				resized := image.NewRGBA(image.Rect(0, 0, w, height))
				xdraw.BiLinear.Scale(resized, resized.Bounds(), panel, panel.Bounds(), xdraw.Src, nil)
				draw.Draw(out, image.Rect(xOff, 0, xOff+w, height), resized, image.Point{}, draw.Src)
			}
		}
		xOff += w
	}

	return saveImage(out, outPath)
}

func drawRectOutline(img *image.RGBA, x1, y1, x2, y2 int, c color.Color, thickness int) {
	for t := 0; t < thickness; t++ {
		hLine(img, x1-t, x2+t, y1-t, c)
		hLine(img, x1-t, x2+t, y2+t, c)
		vLine(img, y1-t, y2+t, x1-t, c)
		vLine(img, y1-t, y2+t, x2+t, c)
	}
}

func hLine(img *image.RGBA, x1, x2, y int, c color.Color) {
	b := img.Bounds()
	if y < b.Min.Y || y >= b.Max.Y {
		return
	}
	for x := max(x1, b.Min.X); x <= x2 && x < b.Max.X; x++ {
		img.Set(x, y, c)
	}
}

func vLine(img *image.RGBA, y1, y2, x int, c color.Color) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X {
		return
	}
	for y := max(y1, b.Min.Y); y <= y2 && y < b.Max.Y; y++ {
		img.Set(x, y, c)
	}
}

func fillRect(img *image.RGBA, x1, y1, x2, y2 int, c color.Color) {
	b := img.Bounds()
	rect := image.Rect(max(x1, b.Min.X), max(y1, b.Min.Y), min(x2, b.Max.X), min(y2, b.Max.Y))
	draw.Draw(img, rect, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func drawText(img *image.RGBA, text string, x, y int, c color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(c),
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func saveImage(img image.Image, outPath string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
