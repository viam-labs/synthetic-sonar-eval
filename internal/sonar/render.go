package sonar

import (
	"fmt"
	"image"
	"math"
	"sort"
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

// dBPerCountCalibrated converts a raw sonar amplitude count to dB, per the
// sensor's fixed-point encoding: 10*log10(2)/256 (~0.01176 dB/count).
var dBPerCountCalibrated = 10.0 * math.Log10(2) / 256.0
var dbMax = -64.0
var dbMin = -100.0

// RenderParams controls the visual output of RenderFanSampleGrid.
// Use DefaultRenderParams to get a fully populated value, then override as needed.
type RenderParams struct {
	HeatmapRangeSigmaFactor float64 `json:"heatmapRangeSigmaFactor"`
	HeatmapArcSigmaFactor   float64 `json:"heatmapArcSigmaFactor"`
	HeatmapMinThreshold     float64 `json:"heatmapMinThreshold"`
	// SplatKernel selects how each amp sample is painted: "gauss" (default)
	// splats an anisotropic Gaussian (sigma factors above apply); "cell"
	// fills the sample's hard-edged beam×sample polar cell with its value,
	// max-combined; "bilinear" scan-converts the polar amp grid to the
	// cartesian image with bilinear interpolation in (beam, sample) space —
	// tapered stroke edges, the classic sonar-display drawing style.
	SplatKernel string      `json:"splatKernel"`
	ColorStops  []ColorStop `json:"colorStops"`
}

// DefaultRenderParams returns the default rendering parameters.
func DefaultRenderParams() RenderParams {
	return RenderParams{
		HeatmapRangeSigmaFactor: 1.5,
		HeatmapArcSigmaFactor:   0.5,
		HeatmapMinThreshold:     0.03,
		SplatKernel:             "gauss",
		ColorStops: []ColorStop{
			{0.00, 0, 0, 0},
			{0.11, 0, 0, 200},
			{0.22, 0, 100, 255},
			{0.33, 0, 220, 220},
			{0.44, 0, 220, 80},
			{0.56, 170, 230, 0},
			{0.67, 255, 220, 0},
			{0.78, 255, 130, 0},
			{0.89, 230, 30, 0},
			{1.00, 110, 0, 0},
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

// fanAzEdges returns the nBeams+1 azimuth bin edges (degrees) bracketing
// each beam's AZSorted center, extrapolated at the ends.
func fanAzEdges(grid *FanSampleGrid) []float64 {
	nBeams := grid.NBeams
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
	return azEdges
}

// fanExtent computes the vessel-relative pixel-mapping window for a fan. A
// square image of pixel size `size` maps pixel (px,py) to vessel-relative
// coordinates (yVessel, xVessel) via:
//
//	yVessel = minH + px/size*spanH   (lateral, "H")
//	xVessel = maxV - py/size*spanV   (forward along boresight, "V")
//
// This is the same geometry RenderFanSampleGrid uses internally, exposed so
// the ping-ping filter can re-project one frame's pixels into another
// frame's window.
func fanExtent(grid *FanSampleGrid) (minH, spanH, maxV, spanV float64) {
	nSamples := grid.NSamples
	cosTilt := grid.CosTilt
	azEdges := fanAzEdges(grid)

	rangeEdges := make([]float64, nSamples+1)
	for i := 0; i <= nSamples; i++ {
		rangeEdges[i] = float64(i) * grid.RangePerSample
	}

	minH, maxH := math.Inf(1), math.Inf(-1)
	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, az := range azEdges {
		azRad := az * math.Pi / 180
		cosAZ := math.Cos(azRad)
		sinAZ := math.Sin(azRad)
		for _, r := range rangeEdges {
			x := r * cosAZ * cosTilt
			y := r * sinAZ * cosTilt
			minH = math.Min(minH, y)
			maxH = math.Max(maxH, y)
			minV = math.Min(minV, x)
			maxV = math.Max(maxV, x)
		}
	}
	spanH = maxH - minH
	spanV = maxV - minV
	if spanH < 1e-6 {
		spanH = 1
	}
	if spanV < 1e-6 {
		spanV = 1
	}
	return minH, spanH, maxV, spanV
}

// RenderFanSampleGridGray renders a sonar fan into a square 8-bit grayscale
// "signal image" via the configured SplatKernel (Gaussian splatting or hard
// beam×sample cell fill): white (255) is the top of the
// [DBMin, DBMax] display window, black (0) is at/below DBMin or below
// HeatmapMinThreshold. This is the image the ping-ping filter blends on —
// ColorizeGray then maps it through the color ramp for display. Pass nil for
// params to use DefaultRenderParams.
func RenderFanSampleGridGray(grid *FanSampleGrid, size int, params *RenderParams) (*image.Gray, error) {
	if grid == nil {
		return nil, fmt.Errorf("fan sample grid is required")
	}
	if size <= 0 {
		size = DefaultRenderSize
	}

	p := DefaultRenderParams()
	if params != nil {
		p = *params
	}

	nBeams := grid.NBeams
	cosTilt := grid.CosTilt

	dbSpan := dbMax - dbMin

	azEdges := fanAzEdges(grid)
	minH, spanH, maxV, spanV := fanExtent(grid)

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

	kernel := p.SplatKernel
	if kernel == "" {
		kernel = "gauss"
	}
	splatAmps := grid.Amps
	switch kernel {
	case "gauss", "cell":
	case "bilinear":
		scanConvertBilinear(grid, size, heat, minH, spanH, maxV, spanV)
		splatAmps = nil // heat fully painted; skip the per-sample splat loop
	default:
		return nil, fmt.Errorf("unknown splatKernel %q (want \"gauss\", \"cell\" or \"bilinear\")", kernel)
	}

	for key, v := range splatAmps {
		b, s, ok := ParseAmpKey(key)
		if !ok {
			continue
		}
		db := float64(v) * dBPerCountCalibrated
		norm := (db - dbMin) / dbSpan
		if norm <= 0 {
			continue
		}
		if norm > 1 {
			norm = 1
		}

		if kernel == "cell" {
			// Radial bounds centered on the same range the Gaussian kernel
			// centers its splat at, (s+1)*RangePerSample.
			r0 := (float64(s) + 0.5) * grid.RangePerSample * cosTilt
			r1 := (float64(s) + 1.5) * grid.RangePerSample * cosTilt
			az0, az1 := azEdges[b], azEdges[b+1]
			if az1 < az0 {
				az0, az1 = az1, az0
			}
			azc := (az0 + az1) / 2

			// Pixel bbox from the four cell corners, one pixel of margin
			// for the arc bowing outside the corner chords.
			minPx, maxPx := math.Inf(1), math.Inf(-1)
			minPy, maxPy := math.Inf(1), math.Inf(-1)
			for _, r := range [2]float64{r0, r1} {
				for _, az := range [2]float64{az0, az1} {
					azRad := az * math.Pi / 180
					px, py := toPixelF(r*math.Sin(azRad), r*math.Cos(azRad))
					minPx = math.Min(minPx, px)
					maxPx = math.Max(maxPx, px)
					minPy = math.Min(minPy, py)
					maxPy = math.Max(maxPy, py)
				}
			}
			x0 := max(int(math.Floor(minPx))-1, 0)
			x1 := min(int(math.Ceil(maxPx))+1, size-1)
			y0 := max(int(math.Floor(minPy))-1, 0)
			y1 := min(int(math.Ceil(maxPy))+1, size-1)

			for py := y0; py <= y1; py++ {
				xV := maxV - float64(py)/float64(size)*spanV
				for px := x0; px <= x1; px++ {
					yV := minH + float64(px)/float64(size)*spanH
					r := math.Hypot(xV, yV)
					if r < r0 || r >= r1 {
						continue
					}
					az := math.Atan2(yV, xV) * 180 / math.Pi
					az -= 360 * math.Round((az-azc)/360)
					if az < az0 || az >= az1 {
						continue
					}
					if idx := py*size + px; heat[idx] < norm {
						heat[idx] = norm
					}
				}
			}
			continue
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

	img := image.NewGray(image.Rect(0, 0, size, size))

	// Absolute intensity: no per-frame max-normalization. `heat` is already in
	// dB-window-normalized units (per-cell `norm` in [0,1], Gaussian peak 1), so
	// it maps straight onto the 0-255 gray range. This keeps intensity
	// comparable across frames (weak frames stay weak) and makes
	// DBMin/DBMax/DBPerCount the real gain knobs. Overlapping splats can sum
	// past 1, so clamp.
	for py := 0; py < size; py++ {
		rowOff := py * img.Stride
		heatRow := py * size
		for px := 0; px < size; px++ {
			v := heat[heatRow+px]
			if v > 1 {
				v = 1
			}
			if v > p.HeatmapMinThreshold {
				img.Pix[rowOff+px] = uint8(math.Round(v * 255))
			}
		}
	}

	return img, nil
}

// scanConvertBilinear paints the fan into heat by classic polar->cartesian
// scan conversion: every pixel inside the fan looks up its fractional
// (beam, sample) coordinate and bilinearly interpolates the four surrounding
// cells' dB-window-normalized values. Cells absent from Amps (at/below the
// frame noise floor) interpolate as 0, so stroke edges taper instead of
// ending on hard cell boundaries.
func scanConvertBilinear(grid *FanSampleGrid, size int, heat []float64, minH, spanH, maxV, spanV float64) {
	nBeams, nSamples := grid.NBeams, grid.NSamples
	dbSpan := dbMax - dbMin
	normGrid := make([]float64, nBeams*nSamples)
	for key, v := range grid.Amps {
		b, s, ok := ParseAmpKey(key)
		if !ok || b < 0 || b >= nBeams || s < 0 || s >= nSamples {
			continue
		}
		db := float64(v) * dBPerCountCalibrated
		norm := (db - dbMin) / dbSpan
		if norm <= 0 {
			continue
		}
		if norm > 1 {
			norm = 1
		}
		normGrid[b*nSamples+s] = norm
	}

	az := grid.AZSorted
	rps := grid.RangePerSample * grid.CosTilt
	// A fan whose beams span (nearly) the full circle wraps around: pixels
	// between the last and first beam interpolate across the seam.
	fullCircle := az[nBeams-1]-az[0] > 300

	for py := 0; py < size; py++ {
		xV := maxV - float64(py)/float64(size)*spanV
		rowOff := py * size
		for px := 0; px < size; px++ {
			yV := minH + float64(px)/float64(size)*spanH
			// Sample s is centered at ground range (s+1)*rps (matching the
			// other kernels), so the fractional sample coordinate is:
			sf := math.Hypot(xV, yV)/rps - 1
			if sf < 0 || sf > float64(nSamples-1) {
				continue
			}
			a := math.Atan2(yV, xV) * 180 / math.Pi
			for a < az[0] {
				a += 360
			}
			for a >= az[0]+360 {
				a -= 360
			}
			var b0, b1 int
			var fb float64
			if a <= az[nBeams-1] {
				j := sort.SearchFloat64s(az, a)
				if j == 0 {
					b0, b1, fb = 0, 0, 0
				} else {
					b0, b1 = j-1, j
					if az[b1] > az[b0] {
						fb = (a - az[b0]) / (az[b1] - az[b0])
					}
				}
			} else if fullCircle {
				b0, b1 = nBeams-1, 0
				fb = (a - az[nBeams-1]) / (az[0] + 360 - az[nBeams-1])
			} else {
				continue
			}
			s0 := int(sf)
			fs := sf - float64(s0)
			s1 := s0 + 1
			if s1 >= nSamples {
				s1 = nSamples - 1
			}
			v := (normGrid[b0*nSamples+s0]*(1-fs)+normGrid[b0*nSamples+s1]*fs)*(1-fb) +
				(normGrid[b1*nSamples+s0]*(1-fs)+normGrid[b1*nSamples+s1]*fs)*fb
			if v > heat[rowOff+px] {
				heat[rowOff+px] = v
			}
		}
	}
}

// ColorizeGray maps a grayscale signal image (see RenderFanSampleGridGray)
// through the RenderParams color ramp, producing the final display image.
// Pass nil for params to use DefaultRenderParams.
func ColorizeGray(gray *image.Gray, params *RenderParams) image.Image {
	p := DefaultRenderParams()
	if params != nil {
		p = *params
		if len(p.ColorStops) < 2 {
			p.ColorStops = DefaultRenderParams().ColorStops
		}
	}
	cmap := buildCmapLUT(p.ColorStops)

	bounds := gray.Bounds()
	img := image.NewRGBA(bounds)
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		grayOff := gray.PixOffset(bounds.Min.X, py)
		rgbaOff := img.PixOffset(bounds.Min.X, py)
		for px := bounds.Min.X; px < bounds.Max.X; px++ {
			t := float64(gray.Pix[grayOff]) / 255
			c := cmapLookup(&cmap, t)
			img.Pix[rgbaOff] = c[0]
			img.Pix[rgbaOff+1] = c[1]
			img.Pix[rgbaOff+2] = c[2]
			img.Pix[rgbaOff+3] = c[3]
			grayOff++
			rgbaOff += 4
		}
	}
	return img
}

// RenderFanSampleGrid renders a sonar fan straight to a colorized RGBA image
// (RenderFanSampleGridGray followed by ColorizeGray). Pass nil for params to
// use DefaultRenderParams.
func RenderFanSampleGrid(grid *FanSampleGrid, size int, params *RenderParams) (image.Image, error) {
	gray, err := RenderFanSampleGridGray(grid, size, params)
	if err != nil {
		return nil, err
	}
	return ColorizeGray(gray, params), nil
}
