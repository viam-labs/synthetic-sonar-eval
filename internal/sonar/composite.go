package sonar

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CompositeStreamName is the output stream directory of the multi-fan
// composite (the display's right view: the horizontal-h3-* fans on one disk).
const CompositeStreamName = "horizontal-h3-composite"

// compositeGroupTolerance clusters member pings into one composite frame.
// Measured member skew within a ping is ~10 ms against a ~1 s ping cadence,
// so anything well between the two works.
const compositeGroupTolerance = 250 * time.Millisecond

// isCompositeMember reports whether a tabular stream belongs to the
// composite (same prefix convention as cmd/render's screen1 pairing).
func isCompositeMember(stream string) bool {
	return strings.HasPrefix(stream, "horizontal-h3-")
}

// compositeMemberParams returns the params used to render composite member
// fans: p with the composite-specific overrides applied.
func (p RenderParams) compositeMemberParams() RenderParams {
	m := p
	if p.CompositeRadialPeakWindow != nil {
		m.RadialPeakWindow = *p.CompositeRadialPeakWindow
	}
	if p.CompositeDBOffset != nil {
		m.DBOffset = *p.CompositeDBOffset
	}
	return m
}

// timedFile is one member stream's tabular file with its ping timestamp
// (parsed from the filename stem).
type timedFile struct {
	t      time.Time
	stream string
	path   string
	stem   string
}

// compositeFrame is one composite output frame: at most one tabular file per
// member stream, pinged within compositeGroupTolerance of each other.
type compositeFrame struct {
	stem    string // earliest member's stem -> output filename
	members []timedFile
}

// parseStemTime parses a tabular filename stem like
// "2026-07-08T19-13-42-831Z" — RFC3339 with ':' and '.' made filename-safe
// as '-'.
func parseStemTime(stem string) (time.Time, error) {
	i := strings.IndexByte(stem, 'T')
	if i < 0 {
		return time.Time{}, fmt.Errorf("no 'T' in timestamp %q", stem)
	}
	clock := strings.Replace(stem[i+1:], "-", ":", 2)
	clock = strings.Replace(clock, "-", ".", 1)
	return time.Parse(time.RFC3339, stem[:i+1]+clock)
}

// groupCompositeFrames clusters member-stream tabular files into composite
// frames: walking files in timestamp order, a file joins the current frame
// while it is within tol of the frame's earliest member and its stream is
// not already present; otherwise it starts a new frame. Frames therefore
// tolerate missing members (a stream absent for a ping simply isn't in that
// frame).
func groupCompositeFrames(files []timedFile, tol time.Duration) []compositeFrame {
	sorted := make([]timedFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].t.Before(sorted[j].t) })

	var frames []compositeFrame
	for _, f := range sorted {
		if n := len(frames); n > 0 {
			cur := &frames[n-1]
			if f.t.Sub(cur.members[0].t) <= tol && !frameHasStream(cur, f.stream) {
				cur.members = append(cur.members, f)
				continue
			}
		}
		frames = append(frames, compositeFrame{stem: f.stem, members: []timedFile{f}})
	}
	return frames
}

func frameHasStream(frame *compositeFrame, stream string) bool {
	for _, m := range frame.members {
		if m.stream == stream {
			return true
		}
	}
	return false
}

// compositeOp combines src into dst in place, per pixel, in gray signal
// space.
type compositeOp func(dst, src *image.Gray)

func compositeOpFor(mode string) (compositeOp, error) {
	switch mode {
	case "max":
		return maxCombineGray, nil
	default:
		return nil, fmt.Errorf("unknown compositeMode %q (want \"max\")", mode)
	}
}

// maxCombineGray keeps the per-pixel maximum: an echo is as bright as its
// brightest fan, so overlapping fans don't brighten each other.
func maxCombineGray(dst, src *image.Gray) {
	for i, v := range src.Pix {
		if v > dst.Pix[i] {
			dst.Pix[i] = v
		}
	}
}

func copyGray(img *image.Gray) *image.Gray {
	out := image.NewGray(img.Bounds())
	copy(out.Pix, img.Pix)
	return out
}

// CompositeWindow pins the composite pixel window to the display's on-screen
// right view: RangeM is the view's range setting (window half-size in
// ground-range meters; <= 0 derives the members' full data disk), and
// VesselX/VesselY place the vessel at that fraction of the window (the
// display's off-center mode; <= 0 means centered). All three are operator
// display settings not present in the data, so matching the on-screen view
// requires passing them explicitly (cmd/render's --composite-* flags).
type CompositeWindow struct {
	RangeM  float64
	VesselX float64
	VesselY float64
}

// compositeExtentFromMembers resolves the shared pixel window of the
// composite from the window spec, deriving the full data disk when no range
// is pinned.
func compositeExtentFromMembers(members map[string]*FanSampleGrid, window CompositeWindow) FanExtent {
	rangeM := window.RangeM
	if rangeM <= 0 {
		for _, g := range members {
			if r := float64(g.NSamples) * g.RangePerSample * g.CosTilt; r > rangeM {
				rangeM = r
			}
		}
		log.Printf("composite: derived window radius %.1f m from data (pass --composite-range-m to pin the display's right-view range)", rangeM)
	}
	return OffCenterSquareExtent(rangeM, window.VesselX, window.VesselY)
}

// compositeRenderer renders composite frames: the member fans rendered on a
// shared fixed pixel window and combined per pixel, with the ping-ping
// filter placed per CompositeEMAPlacement. Like PingPingRenderer, it is
// stateful and must be fed frames in chronological order.
type compositeRenderer struct {
	memberParams RenderParams // effective per-fan params (composite overrides applied)
	op           compositeOp
	placement    string
	level        PingPingLevel
	floorGray    uint8
	ext          FanExtent
	size         int

	pre  *PingPingRenderer            // "pre": one history for the combined stream
	post map[string]*PingPingRenderer // "post": one history per member stream
}

func newCompositeRenderer(params *RenderParams, level PingPingLevel, floorGray uint8, ext FanExtent, size int) (*compositeRenderer, error) {
	op, err := compositeOpFor(params.CompositeMode)
	if err != nil {
		return nil, err
	}
	placement := params.CompositeEMAPlacement
	if placement == "" {
		placement = "pre"
	}
	if placement != "pre" && placement != "post" {
		return nil, fmt.Errorf("unknown compositeEmaPlacement %q (want \"pre\" or \"post\")", placement)
	}
	if f := params.CompositePingPingFilter; f != "" {
		switch strings.ToLower(strings.TrimSpace(f)) {
		case "off", "weak", "medium", "strong":
			level = ParsePingPingLevel(f)
		default:
			return nil, fmt.Errorf("unknown compositePingPingFilter %q (want \"off\", \"weak\", \"medium\" or \"strong\")", f)
		}
	}
	c := &compositeRenderer{
		memberParams: params.compositeMemberParams(),
		op:           op,
		placement:    placement,
		level:        level,
		floorGray:    floorGray,
		ext:          ext,
		size:         size,
	}
	if placement == "pre" {
		c.pre = &PingPingRenderer{Level: level}
	} else {
		c.post = map[string]*PingPingRenderer{}
	}
	return c, nil
}

// renderFrame renders one composite frame from the present member grids
// (stream -> grid), returning the colorized display image and the floored
// gray signal image. Members are combined in stream-name order for
// determinism; poses across members differ by <=10 ms of vessel motion, so
// the first present member anchors the frame.
func (c *compositeRenderer) renderFrame(members map[string]*FanSampleGrid) (image.Image, *image.Gray, error) {
	streams := make([]string, 0, len(members))
	for s := range members {
		streams = append(streams, s)
	}
	sort.Strings(streams)

	var combined *image.Gray
	switch c.placement {
	case "pre":
		for _, s := range streams {
			gray, err := renderFanSampleGridGrayExt(members[s], c.size, &c.memberParams, &c.ext)
			if err != nil {
				return nil, nil, fmt.Errorf("render member %s: %w", s, err)
			}
			if combined == nil {
				combined = gray
			} else {
				c.op(combined, gray)
			}
		}
		anchor := poseFromGridExt(members[streams[0]], c.size, &c.ext)
		combined = c.pre.blendGray(combined, anchor, c.size)
	case "post":
		for _, s := range streams {
			gray, err := renderFanSampleGridGrayExt(members[s], c.size, &c.memberParams, &c.ext)
			if err != nil {
				return nil, nil, fmt.Errorf("render member %s: %w", s, err)
			}
			pr, ok := c.post[s]
			if !ok {
				pr = &PingPingRenderer{Level: c.level}
				c.post[s] = pr
			}
			// blendGray's return is the stream's history image — copy
			// before combining into it.
			filtered := pr.blendGray(gray, poseFromGridExt(members[s], c.size, &c.ext), c.size)
			if combined == nil {
				combined = copyGray(filtered)
			} else {
				c.op(combined, filtered)
			}
		}
	}

	display := applySignalFloor(combined, c.floorGray)
	return ColorizeGray(display, &c.memberParams), display, nil
}

// renderCompositeFrames groups the collected member files into composite
// frames and renders each into sonarImagesDir/horizontal-h3-composite (plus
// the gray signal image under signalImagesDir when set — always produced,
// even with the ping-ping filter off). Existing PNGs are skipped for
// resumability with the same restarted-history caveat as RenderDirectory.
func renderCompositeFrames(files []timedFile, sonarImagesDir, signalImagesDir string, size int, params *RenderParams, level PingPingLevel, floorGray uint8, window CompositeWindow) (rendered, skipped int, err error) {
	if size <= 0 {
		size = DefaultRenderSize
	}
	frames := groupCompositeFrames(files, compositeGroupTolerance)
	nStreams := map[string]bool{}
	for _, f := range files {
		nStreams[f.stream] = true
	}
	fmt.Printf("  compositing %d %s frame(s) from %d member file(s) across %d stream(s)\n",
		len(frames), CompositeStreamName, len(files), len(nStreams))

	var cr *compositeRenderer
	for i := range frames {
		frame := &frames[i]
		pngPath := filepath.Join(sonarImagesDir, CompositeStreamName, frame.stem+".png")
		if _, statErr := os.Stat(pngPath); statErr == nil {
			skipped++
			continue
		}

		members := map[string]*FanSampleGrid{}
		for _, m := range frame.members {
			grid, loadErr := loadFanGrid(m.path)
			if loadErr != nil {
				log.Printf("warning: %v", loadErr)
				continue
			}
			if grid.NBeams == 0 || grid.NSamples == 0 {
				log.Printf("warning: empty grid in %s, skipping member", m.path)
				continue
			}
			members[m.stream] = grid
		}
		if len(members) == 0 {
			log.Printf("warning: composite frame %s: no valid members, skipping", frame.stem)
			continue
		}
		if len(members) < len(nStreams) {
			log.Printf("warning: composite frame %s: only %d/%d member fan(s) present", frame.stem, len(members), len(nStreams))
		}

		if cr == nil {
			ext := compositeExtentFromMembers(members, window)
			cr, err = newCompositeRenderer(params, level, floorGray, ext, size)
			if err != nil {
				return rendered, skipped, err
			}
		}

		img, signal, renderErr := cr.renderFrame(members)
		if renderErr != nil {
			log.Printf("warning: render composite %s: %v", frame.stem, renderErr)
			continue
		}
		if err = writePNG(pngPath, img); err != nil {
			return rendered, skipped, err
		}
		if signal != nil && signalImagesDir != "" {
			if err = writePNG(filepath.Join(signalImagesDir, CompositeStreamName, frame.stem+".png"), signal); err != nil {
				return rendered, skipped, err
			}
		}
		rendered++
	}
	return rendered, skipped, nil
}
