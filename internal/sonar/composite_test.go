package sonar

import (
	"fmt"
	"image"
	"math"
	"testing"
	"time"
)

func mustStemTime(t *testing.T, stem string) time.Time {
	t.Helper()
	ts, err := parseStemTime(stem)
	if err != nil {
		t.Fatalf("parseStemTime(%q): %v", stem, err)
	}
	return ts
}

func TestParseStemTime(t *testing.T) {
	ts := mustStemTime(t, "2026-07-08T19-13-42-831Z")
	want := time.Date(2026, 7, 8, 19, 13, 42, 831e6, time.UTC)
	if !ts.Equal(want) {
		t.Fatalf("got %v, want %v", ts, want)
	}
	if _, err := parseStemTime("not-a-timestamp"); err == nil {
		t.Fatal("expected error for garbage stem")
	}
}

func tf(t *testing.T, stream, stem string) timedFile {
	t.Helper()
	return timedFile{t: mustStemTime(t, stem), stream: stream, stem: stem}
}

func TestGroupCompositeFrames(t *testing.T) {
	// Two full pings ~1s apart with ~10ms member skew, then a ping with a
	// missing member, then a lone member.
	files := []timedFile{
		tf(t, "h3-1", "2026-07-08T19-13-42-831Z"),
		tf(t, "h3-2", "2026-07-08T19-13-42-838Z"),
		tf(t, "h3-3", "2026-07-08T19-13-42-838Z"),
		tf(t, "h3-1", "2026-07-08T19-13-43-830Z"),
		tf(t, "h3-2", "2026-07-08T19-13-43-836Z"),
		tf(t, "h3-3", "2026-07-08T19-13-43-841Z"),
		tf(t, "h3-1", "2026-07-08T19-13-44-830Z"),
		tf(t, "h3-3", "2026-07-08T19-13-44-838Z"),
		tf(t, "h3-2", "2026-07-08T19-13-45-835Z"),
	}
	frames := groupCompositeFrames(files, 250*time.Millisecond)
	if len(frames) != 4 {
		t.Fatalf("got %d frames, want 4: %+v", len(frames), frames)
	}
	wantSizes := []int{3, 3, 2, 1}
	wantStems := []string{
		"2026-07-08T19-13-42-831Z",
		"2026-07-08T19-13-43-830Z",
		"2026-07-08T19-13-44-830Z",
		"2026-07-08T19-13-45-835Z",
	}
	for i, frame := range frames {
		if len(frame.members) != wantSizes[i] {
			t.Errorf("frame %d: got %d members, want %d", i, len(frame.members), wantSizes[i])
		}
		if frame.stem != wantStems[i] {
			t.Errorf("frame %d: got stem %q, want %q", i, frame.stem, wantStems[i])
		}
	}
}

func TestGroupCompositeFramesDuplicateStreamStartsNewFrame(t *testing.T) {
	// Same stream twice within tolerance: the second file must open a new
	// frame rather than joining (one file per stream per frame).
	files := []timedFile{
		tf(t, "h3-1", "2026-07-08T19-13-42-831Z"),
		tf(t, "h3-1", "2026-07-08T19-13-42-900Z"),
	}
	frames := groupCompositeFrames(files, 250*time.Millisecond)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
}

func TestMaxCombineGray(t *testing.T) {
	dst := image.NewGray(image.Rect(0, 0, 2, 1))
	src := image.NewGray(image.Rect(0, 0, 2, 1))
	dst.Pix[0], dst.Pix[1] = 10, 200
	src.Pix[0], src.Pix[1] = 50, 100
	maxCombineGray(dst, src)
	if dst.Pix[0] != 50 || dst.Pix[1] != 200 {
		t.Fatalf("got %v, want [50 200]", dst.Pix[:2])
	}
}

// syntheticGrid builds a small fan with a few hot cells so render output is
// non-trivial.
func syntheticGrid() *FanSampleGrid {
	nBeams, nSamples := 8, 16
	az := make([]float64, nBeams)
	for i := range az {
		az[i] = -160 + float64(i)*45 // ~full circle
	}
	amps := map[string]float32{}
	for b := 0; b < nBeams; b += 2 {
		for s := 4; s < 12; s += 3 {
			// well above the [-100,-64] window bottom
			amps[fmt.Sprintf("%d_%d", b, s)] = 1500
		}
	}
	return &FanSampleGrid{
		NBeams:         nBeams,
		NSamples:       nSamples,
		RangePerSample: 2.0,
		CosTilt:        0.95,
		AZSorted:       az,
		Amps:           amps,
	}
}

func TestExtOverrideMatchesFanExtent(t *testing.T) {
	grid := syntheticGrid()
	params := DefaultRenderParams()
	params.SplatKernel = "bilinear"
	params.RadialPeakWindow = 2

	base, err := RenderFanSampleGridGray(grid, 128, &params)
	if err != nil {
		t.Fatal(err)
	}
	minH, spanH, maxV, spanV := fanExtent(grid)
	ext := &FanExtent{MinH: minH, SpanH: spanH, MaxV: maxV, SpanV: spanV}
	over, err := renderFanSampleGridGrayExt(grid, 128, &params, ext)
	if err != nil {
		t.Fatal(err)
	}
	for i := range base.Pix {
		if base.Pix[i] != over.Pix[i] {
			t.Fatalf("pixel %d differs: %d vs %d", i, base.Pix[i], over.Pix[i])
		}
	}
}

func TestSquareExtentCropsAndCenters(t *testing.T) {
	ext := SquareExtent(100)
	if ext.MinH != -100 || ext.SpanH != 200 || ext.MaxV != 100 || ext.SpanV != 200 {
		t.Fatalf("unexpected extent: %+v", ext)
	}
	// Rendering into a window smaller than the fan must not error and must
	// still paint something near the center.
	grid := syntheticGrid()
	params := DefaultRenderParams()
	params.SplatKernel = "bilinear"
	small := SquareExtent(10)
	img, err := renderFanSampleGridGrayExt(grid, 64, &params, &small)
	if err != nil {
		t.Fatal(err)
	}
	nonzero := 0
	for _, v := range img.Pix {
		if v > 0 {
			nonzero++
		}
	}
	if nonzero == 0 {
		t.Fatal("cropped window rendered fully black")
	}
}

func TestCompositeRendererPrePostOffEquivalence(t *testing.T) {
	// With the ping-ping filter off, "pre" and "post" placements must
	// produce identical composites (both degenerate to plain combine).
	grid1, grid2 := syntheticGrid(), syntheticGrid()
	grid2.CosTilt = 0.85 // second "fan tier"

	members := map[string]*FanSampleGrid{
		"horizontal-h3-1-sensor": grid1,
		"horizontal-h3-2-sensor": grid2,
	}
	params := DefaultRenderParams()
	params.SplatKernel = "bilinear"
	params.CompositeMode = "max"
	ext := SquareExtent(30)

	render := func(placement string) *image.Gray {
		p := params
		p.CompositeEMAPlacement = placement
		cr, err := newCompositeRenderer(&p, PingPingOff, 0, ext, 64)
		if err != nil {
			t.Fatal(err)
		}
		_, signal, err := cr.renderFrame(members)
		if err != nil {
			t.Fatal(err)
		}
		return signal
	}

	pre, post := render("pre"), render("post")
	for i := range pre.Pix {
		if pre.Pix[i] != post.Pix[i] {
			t.Fatalf("pixel %d differs between placements: %d vs %d", i, pre.Pix[i], post.Pix[i])
		}
	}
}

func TestNewCompositeRendererRejectsUnknownConfig(t *testing.T) {
	params := DefaultRenderParams()
	params.CompositeMode = "sum"
	if _, err := newCompositeRenderer(&params, PingPingOff, 0, SquareExtent(10), 64); err == nil {
		t.Fatal("expected error for unknown compositeMode")
	}
	params.CompositeMode = "max"
	params.CompositeEMAPlacement = "sideways"
	if _, err := newCompositeRenderer(&params, PingPingOff, 0, SquareExtent(10), 64); err == nil {
		t.Fatal("expected error for unknown compositeEmaPlacement")
	}
}

func TestOffCenterSquareExtent(t *testing.T) {
	// Vessel at (0.443, 0.489) of a 182 m window: the window must place
	// vessel-relative (0,0) at exactly that pixel fraction.
	ext := OffCenterSquareExtent(182, 0.443, 0.489)
	fx := (0 - ext.MinH) / ext.SpanH
	fy := (ext.MaxV - 0) / ext.SpanV
	if math.Abs(fx-0.443) > 1e-12 || math.Abs(fy-0.489) > 1e-12 {
		t.Fatalf("vessel lands at (%v, %v), want (0.443, 0.489)", fx, fy)
	}
	if c := SquareExtent(50); c != OffCenterSquareExtent(50, 0.5, 0.5) {
		t.Fatalf("SquareExtent != centered OffCenterSquareExtent: %+v vs %+v", c, OffCenterSquareExtent(50, 0.5, 0.5))
	}
	if d := OffCenterSquareExtent(50, 0, 0); d != SquareExtent(50) {
		t.Fatalf("zero fractions must default to centered, got %+v", d)
	}
}

func TestCompositePingPingFilterOverride(t *testing.T) {
	params := DefaultRenderParams()
	params.CompositeMode = "max"
	params.CompositePingPingFilter = "weak"
	cr, err := newCompositeRenderer(&params, PingPingMedium, 0, SquareExtent(10), 64)
	if err != nil {
		t.Fatal(err)
	}
	if cr.level != PingPingWeak || cr.pre.Level != PingPingWeak {
		t.Fatalf("override not applied: level %v, pre %v", cr.level, cr.pre.Level)
	}
	params.CompositePingPingFilter = "mediumish"
	if _, err := newCompositeRenderer(&params, PingPingMedium, 0, SquareExtent(10), 64); err == nil {
		t.Fatal("expected error for unknown compositePingPingFilter")
	}
}
