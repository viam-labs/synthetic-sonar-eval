package sonar

import (
	"fmt"
	"image"
	"math"
)

const (
	DefaultRenderSize = 1024

	gaussLUTN   = 4096
	gaussLUTMax = 9.0

	cmapLUTN = 4096
)

// ColorStop is a single point in a colormap gradient (t in [0,1], r/g/b in [0,255]).
type ColorStop struct {
	T float64 `json:"t"`
	R float64 `json:"r"`
	G float64 `json:"g"`
	B float64 `json:"b"`
}

// RenderParams controls the visual output of RenderFanSampleGrid.
// Use DefaultRenderParams to get a fully populated value, then override as needed.
type RenderParams struct {
	DBPerCount              float64     `json:"dbPerCount"`
	DBWindow                float64     `json:"dbWindow"`
	DBDisplayHeadroom       float64     `json:"dbDisplayHeadroom"`
	HeatmapRangeSigmaFactor float64     `json:"heatmapRangeSigmaFactor"`
	HeatmapArcSigmaFactor   float64     `json:"heatmapArcSigmaFactor"`
	HeatmapMinThreshold     float64     `json:"heatmapMinThreshold"`
	ColorStops              []ColorStop `json:"colorStops"`
}

// DefaultRenderParams returns the default rendering parameters.
func DefaultRenderParams() RenderParams {
	return RenderParams{
		DBPerCount:              0.5,
		DBWindow:                6000.0,
		DBDisplayHeadroom:       300.0,
		HeatmapRangeSigmaFactor: 0.5,
		HeatmapArcSigmaFactor:   0.7,
		HeatmapMinThreshold:     0.01,
		ColorStops: []ColorStop{
			{0.00, 2, 0, 127},
			{0.12, 0, 200, 255},
			{0.18, 0, 255, 64},
			{0.24, 255, 255, 0},
			{0.32, 255, 128, 0},
			{0.40, 255, 0, 0},
			{0.55, 255, 0, 0},
			{0.70, 140, 0, 0},
			{0.85, 55, 0, 0},
			{1.00, 18, 0, 0},
		},
	}
}

var gaussLUT [gaussLUTN + 1]float64

func init() {
	for i := 0; i <= gaussLUTN; i++ {
		u := float64(i) / float64(gaussLUTN) * gaussLUTMax
		gaussLUT[i] = math.Exp(-u / 2)
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

func buildCmapLUT(stops []ColorStop) [cmapLUTN + 1][4]byte {
	var lut [cmapLUTN + 1][4]byte
	for i := 0; i <= cmapLUTN; i++ {
		t := math.Max(0, math.Min(1, float64(i)/float64(cmapLUTN)))
		var r, g, b float64
		found := false
		for j := 1; j < len(stops); j++ {
			if t <= stops[j].T {
				u := (t - stops[j-1].T) / (stops[j].T - stops[j-1].T)
				r = stops[j-1].R + u*(stops[j].R-stops[j-1].R)
				g = stops[j-1].G + u*(stops[j].G-stops[j-1].G)
				b = stops[j-1].B + u*(stops[j].B-stops[j-1].B)
				found = true
				break
			}
		}
		if !found {
			last := stops[len(stops)-1]
			r, g, b = last.R, last.G, last.B
		}
		lut[i] = [4]byte{uint8(math.Round(r)), uint8(math.Round(g)), uint8(math.Round(b)), 255}
	}
	return lut
}

func cmapLookup(lut *[cmapLUTN + 1][4]byte, t float64) [4]byte {
	i := int(t * float64(cmapLUTN))
	if i < 0 {
		return lut[0]
	}
	if i > cmapLUTN {
		return lut[cmapLUTN]
	}
	return lut[i]
}

// RenderFanSampleGrid renders a sonar fan into a square RGBA image via Gaussian
// splatting. The dB display window is computed automatically: anchored at the
// frame noise floor, at least DBWindow wide, and extended to DBDisplayHeadroom
// above the frame peak so echoes are never saturated. Pass nil for params to
// use DefaultRenderParams.
func RenderFanSampleGrid(grid *FanSampleGrid, size int, params *RenderParams) (image.Image, error) {
	if grid == nil {
		return nil, fmt.Errorf("fan sample grid is required")
	}
	if size <= 0 {
		size = DefaultRenderSize
	}

	p := DefaultRenderParams()
	if params != nil {
		p = *params
		if len(p.ColorStops) < 2 {
			p.ColorStops = DefaultRenderParams().ColorStops
		}
	}

	cmap := buildCmapLUT(p.ColorStops)

	nBeams := grid.NBeams
	nSamples := grid.NSamples
	cosTilt := grid.CosTilt

	noiseFloor := float64(grid.MinAmp)
	dbMin := noiseFloor * p.DBPerCount
	dbMax := dbMin + p.DBWindow
	signalPeakDb := dbMin
	for _, v := range grid.Amps {
		if db := float64(v) * p.DBPerCount; db > signalPeakDb {
			signalPeakDb = db
		}
	}
	if signalPeakDb > dbMin {
		dbMax = math.Max(dbMax, signalPeakDb+p.DBDisplayHeadroom)
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
		db := float64(v) * p.DBPerCount
		norm := (db - dbMin) / dbSpan
		if norm <= 0 {
			continue
		}
		if norm > 1 {
			norm = 1
		}

		bp_ := bp[b]
		groundRange := float64(s+1) * grid.RangePerSample * cosTilt
		cx := groundRange * bp_.cosAZ
		cy := groundRange * bp_.sinAZ

		arcWidthM := groundRange * bp_.dazRad
		sigmaRangeM := rangeWidthM * p.HeatmapRangeSigmaFactor
		sigmaArcM := math.Max(arcWidthM, rangeWidthM) * p.HeatmapArcSigmaFactor

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
			rangeDy := float64(dy) * bp_.rangePerDy
			arcDy := float64(dy) * bp_.arcPerDy
			for dx := -radius; dx <= radius; dx++ {
				px := ix + dx
				if px < 0 || px >= size {
					continue
				}
				dRange := float64(dx)*bp_.rangePerDx + rangeDy
				dArc := float64(dx)*bp_.arcPerDx + arcDy
				rr := dRange * invSigmaRange
				ra := dArc * invSigmaArc
				heat[py*size+px] += norm * gaussLookup(rr*rr+ra*ra)
			}
		}
	}

	img := image.NewRGBA(image.Rect(0, 0, size, size))

	bg := cmapLookup(&cmap, 0)
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = bg[0]
		img.Pix[i+1] = bg[1]
		img.Pix[i+2] = bg[2]
		img.Pix[i+3] = bg[3]
	}

	// Absolute intensity: no per-frame max-normalization. `heat` is already in
	// dB-window-normalized units (per-cell `norm` in [0,1], Gaussian peak 1), so
	// it maps straight onto the colormap. This keeps intensity comparable across
	// frames (weak frames stay weak) and makes dbWindow / dbPerCount the real
	// gain knobs. Overlapping splats can sum past 1, so clamp.
	for py := 0; py < size; py++ {
		rowOff := py * img.Stride
		heatRow := py * size
		for px := 0; px < size; px++ {
			v := heat[heatRow+px]
			if v > 1 {
				v = 1
			}
			if v > p.HeatmapMinThreshold {
				c := cmapLookup(&cmap, v)
				off := rowOff + px*4
				img.Pix[off] = c[0]
				img.Pix[off+1] = c[1]
				img.Pix[off+2] = c[2]
				img.Pix[off+3] = c[3]
			}
		}
	}

	return img, nil
}
