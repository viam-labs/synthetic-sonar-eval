package sonar

import (
	"image"
	"math"
	"strings"
)

// PingPingLevel selects the ping-ping filter blend strength, mirroring the
// sonar head's own "Ping Ping Filter" menu setting.
type PingPingLevel int

const (
	PingPingOff PingPingLevel = iota
	PingPingWeak
	PingPingMedium
	PingPingStrong
)

// ParsePingPingLevel parses "off"/"weak"/"medium"/"strong" (case-insensitive),
// defaulting to PingPingOff on no match.
func ParsePingPingLevel(s string) PingPingLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "weak":
		return PingPingWeak
	case "medium":
		return PingPingMedium
	case "strong":
		return PingPingStrong
	default:
		return PingPingOff
	}
}

// pingPingBlendFactor is the weight given to the *current* frame; the
// remainder (1-factor) comes from the re-projected history:
//
//	Off: 1.0, Weak: 0.5, Medium: 0.25, Strong: 0.12
func pingPingBlendFactor(level PingPingLevel) float64 {
	switch level {
	case PingPingWeak:
		return 0.5
	case PingPingMedium:
		return 0.25
	case PingPingStrong:
		return 0.12
	default:
		return 1.0
	}
}

const metersPerDegLat = 111320.0

func metersPerDegLon(latDeg float64) float64 {
	return metersPerDegLat * math.Cos(latDeg*math.Pi/180)
}

// pingPose anchors a rendered frame's pixel window to the sensor's
// position/heading at the moment of that ping, so a frame rendered for one
// ping can be re-projected into another ping's pixel window.
//
// Assumes AZSorted azimuth (as used by fanExtent/RenderFanSampleGrid) is
// degrees clockwise from the sensor boresight, and HeadingDeg is the
// boresight's compass bearing (degrees clockwise from north) — i.e. the
// same "forward = 0°, clockwise-positive" convention for both.
type pingPose struct {
	lat, lon    float64
	headingRad  float64
	minH, spanH float64
	maxV, spanV float64
	size        int
}

func poseFromGrid(grid *FanSampleGrid, size int) pingPose {
	minH, spanH, maxV, spanV := fanExtent(grid)
	return pingPose{
		lat:        grid.Latitude,
		lon:        grid.Longitude,
		headingRad: grid.HeadingDeg * math.Pi / 180,
		minH:       minH,
		spanH:      spanH,
		maxV:       maxV,
		spanV:      spanV,
		size:       size,
	}
}

// toWorld converts a point in this pose's vessel-relative frame (yVessel
// lateral, xVessel forward-along-boresight) to meters east/north of this
// pose's own sensor position.
func (p pingPose) toWorld(yVessel, xVessel float64) (east, north float64) {
	s, c := math.Sin(p.headingRad), math.Cos(p.headingRad)
	east = xVessel*s + yVessel*c
	north = xVessel*c - yVessel*s
	return east, north
}

// fromWorld is toWorld's inverse.
func (p pingPose) fromWorld(east, north float64) (yVessel, xVessel float64) {
	s, c := math.Sin(p.headingRad), math.Cos(p.headingRad)
	xVessel = east*s + north*c
	yVessel = east*c - north*s
	return yVessel, xVessel
}

func (p pingPose) toPixel(yVessel, xVessel float64) (px, py float64) {
	px = (yVessel - p.minH) / p.spanH * float64(p.size)
	py = (p.maxV - xVessel) / p.spanV * float64(p.size)
	return px, py
}

func (p pingPose) fromPixel(px, py float64) (yVessel, xVessel float64) {
	yVessel = p.minH + px/float64(p.size)*p.spanH
	xVessel = p.maxV - py/float64(p.size)*p.spanV
	return yVessel, xVessel
}

// sensorDeltaMeters returns the east/north offset, in meters, of to's sensor
// position relative to from's sensor position (equirectangular approximation,
// fine at the sub-kilometer scale two consecutive pings move across).
func sensorDeltaMeters(from, to pingPose) (east, north float64) {
	mLon := metersPerDegLon((from.lat + to.lat) / 2)
	east = (to.lon - from.lon) * mLon
	north = (to.lat - from.lat) * metersPerDegLat
	return east, north
}

// reprojectToPixel maps a destination pixel in curPose's frame to the
// corresponding source pixel in histPose's frame: it walks the destination
// pixel out to a world position (via curPose's own sensor position/heading),
// then back down into histPose's frame (via histPose's sensor
// position/heading). This is what lets a stationary target land in the same
// place across frames whose pixel windows are centered on different fan
// extents/vessel poses.
func reprojectToPixel(histPose, curPose pingPose, px, py float64) (srcPx, srcPy float64) {
	yVessel, xVessel := curPose.fromPixel(px, py)
	east, north := curPose.toWorld(yVessel, xVessel)
	dEast, dNorth := sensorDeltaMeters(histPose, curPose)
	histYVessel, histXVessel := histPose.fromWorld(east+dEast, north+dNorth)
	return histPose.toPixel(histYVessel, histXVessel)
}

// sampleBilinearGray reads a grayscale img at fractional coordinates (x,y),
// returning ok=false if the point falls outside the image bounds.
func sampleBilinearGray(img *image.Gray, x, y float64) (v float64, ok bool) {
	size := img.Bounds().Dx()
	if x < 0 || y < 0 || x > float64(size-1) || y > float64(size-1) {
		return 0, false
	}
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	x1, y1 := x0+1, y0+1
	if x1 > size-1 {
		x1 = size - 1
	}
	if y1 > size-1 {
		y1 = size - 1
	}
	fx, fy := x-float64(x0), y-float64(y0)

	get := func(px, py int) float64 { return float64(img.Pix[img.PixOffset(px, py)]) }
	lerp := func(a, b, t float64) float64 { return a + (b-a)*t }

	top := lerp(get(x0, y0), get(x1, y0), fx)
	bot := lerp(get(x0, y1), get(x1, y1), fx)
	return lerp(top, bot, fy), true
}

// PingPingRenderer wraps RenderFanSampleGridGray with a geographic ping-ping
// blend filter. Each new ping is rendered on its own pixel window (as usual
// — RenderFanSampleGridGray re-centers on whatever fan extent the current
// ping covers), then blended with the previous filtered signal image after
// re-projecting that history frame into the new frame's window using each
// ping's lat/lon/heading. Without this re-projection, blending directly
// pixel-on-pixel "morphs" the fan geometry across frames (the reported
// "blobs") instead of tracking real-world position. The blended grayscale
// signal image is what gets carried forward as history; ColorizeGray is
// applied only for display.
//
// Not safe for concurrent use. Create one PingPingRenderer per independent
// ping sequence (e.g. per sensor/resource stream) and feed it pings in
// chronological order — mixing sequences or ping order into one renderer
// will blend unrelated frames together.
type PingPingRenderer struct {
	Level  PingPingLevel
	Params *RenderParams

	histImg  *image.Gray
	histPose pingPose
	have     bool
}

// Reset clears the filter's history, e.g. when starting a new ping sequence.
func (r *PingPingRenderer) Reset() {
	r.histImg = nil
	r.have = false
}

// Render renders grid, blends it into the filter's running grayscale signal
// history, and colorizes the result. It returns both the colorized frame and
// the blended grayscale signal image (for inspection/debugging), and updates
// the history for the next call.
func (r *PingPingRenderer) Render(grid *FanSampleGrid, size int) (rendered image.Image, signal *image.Gray, err error) {
	if size <= 0 {
		size = DefaultRenderSize
	}

	cur, err := RenderFanSampleGridGray(grid, size, r.Params)
	if err != nil {
		return nil, nil, err
	}
	curPose := poseFromGrid(grid, size)

	factor := pingPingBlendFactor(r.Level)
	if !r.have || factor >= 1.0 {
		r.histImg, r.histPose, r.have = cur, curPose, true
		return ColorizeGray(cur, r.Params), cur, nil
	}

	out := image.NewGray(cur.Bounds())
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			o := cur.PixOffset(px, py)
			cv := float64(cur.Pix[o])

			// Default to the current pixel where history has no data for
			// this location (e.g. newly revealed ground after moving).
			hv := cv
			srcPx, srcPy := reprojectToPixel(r.histPose, curPose, float64(px)+0.5, float64(py)+0.5)
			if sv, ok := sampleBilinearGray(r.histImg, srcPx, srcPy); ok {
				hv = sv
			}

			out.Pix[o] = uint8(math.Round(factor*cv + (1-factor)*hv))
		}
	}

	r.histImg, r.histPose, r.have = out, curPose, true
	return ColorizeGray(out, r.Params), out, nil
}
