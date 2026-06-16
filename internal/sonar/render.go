package sonar

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

const (
	DefaultRenderSize = 1500

	// dbPerCount converts raw amplitude counts to dB. Adjust to match the
	// sonar hardware's TVG scaling (e.g. 0.5 dB/count is common).
	dbPerCount = 0.5

	// dbWindow is the minimum dB display range anchored at the noise floor.
	// Wide enough that weak echoes above noise are visible without saturation.
	dbWindow = 6000.0

	// dbDisplayHeadroom keeps the frame peak below the colormap top so the
	// strongest returns aren't clipped to the same colour.
	dbDisplayHeadroom = 300.0

	heatmapRangeSigmaFactor = 0.5
	heatmapArcSigmaFactor   = 0.7
	heatmapMinThreshold     = 0.01

	gaussLUTN   = 4096
	gaussLUTMax = 9.0

	cmapLUTN = 4096
)

var gaussLUT [gaussLUTN + 1]float64
var cmapLUT [cmapLUTN + 1][4]byte

func init() {
	for i := 0; i <= gaussLUTN; i++ {
		u := float64(i) / float64(gaussLUTN) * gaussLUTMax
		gaussLUT[i] = math.Exp(-u / 2)
	}
	for i := 0; i <= cmapLUTN; i++ {
		t := float64(i) / float64(cmapLUTN)
		c := colormapSonarAmp(t)
		cmapLUT[i] = [4]byte{c.R, c.G, c.B, c.A}
	}
}

func gaussLookup(x2 float64) float64 {
	if x2 >= gaussLUTMax {
		return 0
	}
	idx := x2 * float64(gaussLUTN) / gaussLUTMax
	i := int(idx)
	return gaussLUT[i] + (idx-float64(i))*(gaussLUT[i+1]-gaussLUT[i])
}

func cmapLookup(t float64) [4]byte {
	i := int(t * float64(cmapLUTN))
	if i < 0 {
		return cmapLUT[0]
	}
	if i > cmapLUTN {
		return cmapLUT[cmapLUTN]
	}
	return cmapLUT[i]
}

var sonarColormapStops = []struct {
	t       float64
	r, g, b float64
}{
	{0.00, 0, 0, 255},
	{0.12, 0, 200, 255},
	{0.18, 0, 255, 64},
	{0.24, 255, 255, 0},
	{0.32, 255, 128, 0},
	{0.40, 255, 0, 0},
	{0.55, 255, 0, 0},
	{0.70, 140, 0, 0},
	{0.85, 55, 0, 0},
	{1.00, 18, 0, 0},
}

func colormapSonarAmp(t float64) color.RGBA {
	t = math.Max(0, math.Min(1, t))
	stops := sonarColormapStops
	for i := 1; i < len(stops); i++ {
		if t <= stops[i].t {
			u := (t - stops[i-1].t) / (stops[i].t - stops[i-1].t)
			r := stops[i-1].r + u*(stops[i].r-stops[i-1].r)
			g := stops[i-1].g + u*(stops[i].g-stops[i-1].g)
			b := stops[i-1].b + u*(stops[i].b-stops[i-1].b)
			return color.RGBA{
				R: uint8(math.Round(r)),
				G: uint8(math.Round(g)),
				B: uint8(math.Round(b)),
				A: 255,
			}
		}
	}
	last := stops[len(stops)-1]
	return color.RGBA{
		R: uint8(math.Round(last.r)),
		G: uint8(math.Round(last.g)),
		B: uint8(math.Round(last.b)),
		A: 255,
	}
}

// RenderFanSampleGrid renders a sonar fan into a square RGBA image via Gaussian
// splatting. The dB display window is computed automatically: anchored at the
// frame noise floor, at least dbWindow wide, and extended to dbDisplayHeadroom
// above the frame peak so echoes are never saturated.
func RenderFanSampleGrid(grid *FanSampleGrid, size int) (image.Image, error) {
	if grid == nil {
		return nil, fmt.Errorf("fan sample grid is required")
	}
	if size <= 0 {
		size = DefaultRenderSize
	}

	nBeams := grid.NBeams
	nSamples := grid.NSamples
	cosTilt := grid.CosTilt

	noiseFloor := float64(grid.MinAmp)
	dbMin := noiseFloor * dbPerCount
	dbMax := dbMin + dbWindow
	signalPeakDb := dbMin
	for _, v := range grid.Amps {
		if db := float64(v) * dbPerCount; db > signalPeakDb {
			signalPeakDb = db
		}
	}
	if signalPeakDb > dbMin {
		dbMax = math.Max(dbMax, signalPeakDb+dbDisplayHeadroom)
	}
	dbSpan := dbMax - dbMin
	if dbSpan < 1e-9 {
		dbSpan = 1
	}

	azEdges := make([]float64, nBeams+1)
	if nBeams > 1 {
		daz := make([]float64, nBeams-1)
		for i := 0; i < nBeams-1; i++ {
			daz[i] = grid.AZSorted[i+1] - grid.AZSorted[i]
		}
		azEdges[0] = grid.AZSorted[0] - daz[0]/2
		for i := 0; i < nBeams-1; i++ {
			azEdges[i+1] = grid.AZSorted[i] + daz[i]/2
		}
		azEdges[nBeams] = grid.AZSorted[nBeams-1] + daz[nBeams-2]/2
	} else {
		azEdges[0] = grid.AZSorted[0] - 1
		azEdges[1] = grid.AZSorted[0] + 1
	}

	rangeEdges := make([]float64, nSamples+1)
	for i := 0; i <= nSamples; i++ {
		rangeEdges[i] = float64(i) * grid.RangePerSample
	}

	xCorners := make([][]float64, nBeams+1)
	yCorners := make([][]float64, nBeams+1)
	for i := 0; i <= nBeams; i++ {
		xCorners[i] = make([]float64, nSamples+1)
		yCorners[i] = make([]float64, nSamples+1)
		azRad := azEdges[i] * math.Pi / 180
		cosAZ := math.Cos(azRad)
		sinAZ := math.Sin(azRad)
		for j := 0; j <= nSamples; j++ {
			r := rangeEdges[j]
			xCorners[i][j] = r * cosAZ * cosTilt
			yCorners[i][j] = r * sinAZ * cosTilt
		}
	}

	minH, maxH := yCorners[0][0], yCorners[0][0]
	minV, maxV := xCorners[0][0], xCorners[0][0]
	for i := range xCorners {
		for j := range xCorners[i] {
			if yCorners[i][j] < minH {
				minH = yCorners[i][j]
			}
			if yCorners[i][j] > maxH {
				maxH = yCorners[i][j]
			}
			if xCorners[i][j] < minV {
				minV = xCorners[i][j]
			}
			if xCorners[i][j] > maxV {
				maxV = xCorners[i][j]
			}
		}
	}
	spanH := maxH - minH
	spanV := maxV - minV
	if spanH < 1e-6 {
		spanH = 1
	}
	if spanV < 1e-6 {
		spanV = 1
	}

	metersPerPixH := spanH / float64(size)
	metersPerPixV := spanV / float64(size)

	toPixelF := func(yVessel, xVessel float64) (float64, float64) {
		px := (yVessel - minH) / spanH * float64(size)
		py := (maxV - xVessel) / spanV * float64(size)
		return px, py
	}

	type beamParams struct {
		cosAZ, sinAZ, dazRad   float64
		rangePerDx, rangePerDy float64
		arcPerDx, arcPerDy     float64
	}
	bp := make([]beamParams, nBeams)
	for b := 0; b < nBeams; b++ {
		azRad := grid.AZSorted[b] * math.Pi / 180
		cosAZ := math.Cos(azRad)
		sinAZ := math.Sin(azRad)
		bp[b] = beamParams{
			cosAZ:      cosAZ,
			sinAZ:      sinAZ,
			dazRad:     math.Abs(azEdges[b+1]-azEdges[b]) * math.Pi / 180,
			rangePerDx: metersPerPixH * sinAZ,
			rangePerDy: -metersPerPixV * cosAZ,
			arcPerDx:   metersPerPixH * cosAZ,
			arcPerDy:   metersPerPixV * sinAZ,
		}
	}

	heat := make([]float64, size*size)
	rangeWidthM := grid.RangePerSample * cosTilt

	for key, v := range grid.Amps {
		b, s, ok := ParseAmpKey(key)
		if !ok {
			continue
		}
		db := float64(v) * dbPerCount
		norm := (db - dbMin) / dbSpan
		if norm <= 0 {
			continue
		}
		if norm > 1 {
			norm = 1
		}

		p := bp[b]
		groundRange := float64(s+1) * grid.RangePerSample * cosTilt
		cx := groundRange * p.cosAZ
		cy := groundRange * p.sinAZ

		arcWidthM := groundRange * p.dazRad
		sigmaRangeM := rangeWidthM * heatmapRangeSigmaFactor
		sigmaArcM := math.Max(arcWidthM, rangeWidthM) * heatmapArcSigmaFactor

		invSigmaRange := 1.0 / sigmaRangeM
		invSigmaArc := 1.0 / sigmaArcM

		maxSigmaM := math.Max(sigmaRangeM, sigmaArcM)
		radius := int(math.Ceil(3 * maxSigmaM / math.Min(metersPerPixH, metersPerPixV)))

		pcx, pcy := toPixelF(cy, cx)
		ix := int(math.Round(pcx))
		iy := int(math.Round(pcy))

		for dy := -radius; dy <= radius; dy++ {
			py := iy + dy
			if py < 0 || py >= size {
				continue
			}
			rangeDy := float64(dy) * p.rangePerDy
			arcDy := float64(dy) * p.arcPerDy
			for dx := -radius; dx <= radius; dx++ {
				px := ix + dx
				if px < 0 || px >= size {
					continue
				}
				dRange := float64(dx)*p.rangePerDx + rangeDy
				dArc := float64(dx)*p.arcPerDx + arcDy
				rr := dRange * invSigmaRange
				ra := dArc * invSigmaArc
				heat[py*size+px] += norm * gaussLookup(rr*rr+ra*ra)
			}
		}
	}

	maxHeat := 0.0
	for _, v := range heat {
		if v > maxHeat {
			maxHeat = v
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))

	bg := cmapLookup(0)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = bg[0]
		img.Pix[i+1] = bg[1]
		img.Pix[i+2] = bg[2]
		img.Pix[i+3] = bg[3]
	}

	if maxHeat > 1e-9 {
		invMaxHeat := 1.0 / maxHeat
		for py := 0; py < size; py++ {
			rowOff := py * img.Stride
			heatRow := py * size
			for px := 0; px < size; px++ {
				v := heat[heatRow+px] * invMaxHeat
				if v > heatmapMinThreshold {
					c := cmapLookup(v)
					off := rowOff + px*4
					img.Pix[off] = c[0]
					img.Pix[off+1] = c[1]
					img.Pix[off+2] = c[2]
					img.Pix[off+3] = c[3]
				}
			}
		}
	}

	pcx, pcy := toPixelF(0, 0)
	drawTriangleUp(img, int(pcx), int(pcy), 8, color.RGBA{255, 255, 255, 255})

	return img, nil
}

func drawTriangleUp(img *image.RGBA, cx, cy, size int, c color.Color) {
	for dy := 0; dy <= size; dy++ {
		half := dy
		for dx := -half; dx <= half; dx++ {
			x, y := cx+dx, cy-dy
			if x >= img.Bounds().Min.X && x < img.Bounds().Max.X &&
				y >= img.Bounds().Min.Y && y < img.Bounds().Max.Y {
				img.Set(x, y, c)
			}
		}
	}
}
