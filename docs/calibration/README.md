# Renderer calibration — pickup & review guide

This documents how to reproduce, extend, or review the renderer-calibration work: making
`synthetic-sonar-eval` renders match what the vessel's SIMRAD display draws, measured
directly against screenshot-derived targets.

**Status.** Calibrated on one 99-frame clip, left view (`horizontal-h` fan). Best config is
committed as [`params/display-calibrated.json`](../../params/display-calibrated.json):
echo-area coverage **1.08×** the display's, intensity-distribution distance (EMD)
**0.61 dB**, stroke thickness within **3%**. Starting point was 10.2× / 3.38 dB.

**The two PRs:**

- Renderer + metrics toolkit (this repo): [viam-labs/synthetic-sonar-eval#5](https://github.com/viam-labs/synthetic-sonar-eval/pull/5)
- Target-building pipeline: [viam-modules/kongsberg-training-utils](https://github.com/viam-modules/kongsberg-training-utils)

![before](before_gauss_default.png)
![after](after_calibrated.png)

*Panels: cropped screenshot view | blob-only target | render colorized through the measured
display palette. Top: old Gaussian default. Bottom: `display-calibrated.json`.*

---

## Prerequisites

| what | detail |
|---|---|
| this repo | branch `render-kernels` (PR #5), Go ≥ 1.26 |
| [`kongsberg-training-utils`](https://github.com/viam-modules/kongsberg-training-utils) | branch `crop-sonar-views` (until merged); its `.venv` is also used to run every Python tool in this repo's `tools/` (needs `opencv-python`, `numpy`, `matplotlib`, `torch`) |
| Viam access | `viam` CLI logged in (for downloading sequences) |

Below, `<clip>` is a downloaded sequence directory (e.g.
`output/b073f310-…/7931d1d96b8d`) and `<targets>` a **sibling** directory for derived data
(e.g. `output/b073f310-…/7931d1d96b8d_targets`). Keep targets *outside* the sequence
directory: re-downloads wipe the sequence directory.

## Step 1 — download the sequence

```bash
make setup                     # writes .env with a fresh viam auth token
make download PART_ID=<part-id> SEQUENCE_ID=<sequence-id>
```

Data lands under `output/<part-id>/<hash>/` with `images/screen1/` (display screenshots,
1 Hz), `tabular/<fan>-sensor/` (raw ping JSON per fan: `horizontal-h`, `horizontal-h3-1/2/3`),
`manifest.json`, `progress.json`. Verify `progress.json` says `"binaryDone": true` and that
`images/screen1` is non-empty — an interrupted download leaves a half-empty directory and
*will* silently skip on retry (delete the hash directory to force a re-download).

The clip everything in this guide was calibrated on:

```bash
make download PART_ID=b073f310-deca-434b-9f87-8cb388f10316 \
              SEQUENCE_ID=1b8e6ca3-e545-4d1d-aaec-79e7d7f0e5d5
# → output/b073f310-…/7931d1d96b8d   (99 frames, 2026-07-08, SIMRAD blue skin, dual layout)
```

## Step 2 — build the targets

A *target* is a clean, comparable sonar view extracted from a screenshot. The extraction
pipeline lives in
**[kongsberg-training-utils](https://github.com/viam-modules/kongsberg-training-utils)** —
see that repo's README and `docs/render-calibration-roadmap.md` for how each stage works
and was validated. From its repo root, with its venv, one
command runs the whole extraction (crop views → strip background → strip overlays) and
prints a CHECKPOINTS block telling you what to eyeball:

```bash
python src/sonar/build_targets.py  <clip>/images/screen1  <targets>
# writes <targets>/{views, views_stripped, views_blobs} (+ _debug/ trees)
```

Then two tools from **this repo** finish the job:

```bash
# measure the display's color palette from the on-screen legend bar
python tools/extract_palette.py  <clip>/images/screen1  <targets>/palette/palette.json \
    --debug-dir <targets>/palette

# invert the blob targets from display colors to grayscale signal space
python tools/invert_targets.py   <targets>/views_blobs  <targets>/palette/palette.json \
    <targets>/views_signal
```

**Checkpoints before trusting the targets:**

- `build_targets.py` streams each stage and ends with a CHECKPOINTS block: the crop stage
  should report `0 partial, 0 skipped` (spot-check `<targets>/views/_debug/` fitted-circle
  overlays), and `<targets>/views_blobs/_debug/` side-by-sides should show echo arcs
  intact with no UI remnants (compass letters, tilt wedge, legend bar, rim tick-ring).
- `extract_palette.py` prints the gate-valid window, e.g. `gate-valid u range:
  [0.125, 0.910] = [-95.5, -67.2] dB`, and writes `palette_ramp.png` — it should look like
  the legend bar on-screen (blue → green → yellow → red → dark tail). These two numbers
  are the comparison window used by every metric; if they differ wildly from the above,
  the legend detection failed.

## Step 3 — render and get the numbers

```bash
# render all fans: writes colorized sonar-images/ AND grayscale sonar-signal/ (the metric input)
make render OUTPUT=<clip> PARAMS=params/display-calibrated.json PINGPINGFILTER=medium

# score the left view against the targets
python tools/compare_1d.py <targets>/views_signal <clip>/sonar-signal/horizontal-h-sensor \
    <targets>/palette/palette.json <out_dir> --side left
```

`compare_1d.py` pairs frames by nearest timestamp (gaps here are ~10 ms) and prints/writes
`summary.json`. The fields that matter:

| field | meaning | value for this clip + preset |
|---|---|---|
| `median_cov_render / median_cov_target` | **coverage ratio** — echo pixels per disk area, render ÷ target. 1.0 = parity | **1.08** |
| `pooled_emd_db` | **EMD** — earth mover's distance between pooled intensity distributions, in dB | **0.61** |
| `pooled_shift_db` | mean intensity difference render − target (the first moment of EMD) | **−0.35** |
| `median_fill_radius_*` | how far out (fraction of disk radius) content reaches — a geometry sanity check | ~0.9–1.0 |

Also written: `per_frame.csv`, `pooled_hist.png` (target vs render intensity distributions
— compare envelopes, not the target's quantization spikes), `per_frame.png`,
`eyeball/*.png`.

Small drifts (±0.1 coverage, ±0.1 dB) across re-downloads are normal (JPEG decode,
timestamp pairing); order-of-magnitude changes are not.

![metric progression](metric_progression.png)

## Step 4 — visual check (do not skip)

The aggregate metrics can be gamed — a small-sigma Gaussian config reached coverage 1.15 as
disconnected dot-speckle. Every config change needs an eyeball pass:

```bash
python tools/visualize_result.py <targets> <clip>/sonar-signal/horizontal-h-sensor <viz_dir> \
    --label "my config"
```

Writes per-frame `[screenshot | target | render]` composites + `composite.mp4`. Checklist:

- echo clusters at the same bearing and range as the screenshot (geometry);
- contiguous strokes — no dot-speckle (sigma too small), no beam-cell blockiness;
- stroke thickness comparable to the display's thin arcs — no fat halos;
- no "blue carpet" of sub-threshold noise;
- radially thick schools not split into concentric "onion rings" (known NMS risk — check
  the dense-school frames).

## Optional — sweeps and diagnostics

| tool | use it when |
|---|---|
| `tools/splat_sweep.sh <clip>` | grid-sweep render params, each config scored with compare_1d. Grid via env vars: `RANGES="0.5 1.0" ARCS="0.25" THRS="0.1" SKIP_PP=1 JOBS=6 bash tools/splat_sweep.sh <clip>`. Results → `collect_sweep.py` table ranked by \|log cov-ratio\|, EMD tiebreak |
| `tools/angle_check.py` | suspected rotation/heading mismatch. Circular cross-correlation of angular echo profiles vs `heading_deg`. Verdict on this clip: median offset 1.4°, no heading dependence — no rotation correction |
| `tools/shift_sweep.py` | test whether a residual mismatch is a constant dB offset (sweeps an offset, reports EMD + coverage vs shift). Used to rule out gain as the explanation for the skirt excess |

Two implementation notes that will save you an afternoon: ping-ping `off` cannot be
scored — the renderer only writes `sonar-signal/` when the filter is on; and sweep renders
must go to separate output dirs (the renderer clears `sonar-images/`/`sonar-signal/` on
every run).

## Metric blind spots (for reviewers)

- **Coverage ratio** counts pixels; it cannot see *where* they are, and parity can be
  faked by fragmentation. Always pair with the visual check (and with stroke thickness:
  4×mean distance transform of the echo mask — display ≈ 6.3 px at 926², see below).
- **EMD** compares normalized distributions; it cannot see total area or geometry.
- **Everything** is restricted to the gate-valid window. Below it (the display's faint
  blue) the target carries no information — differences there are invisible to the metric
  and must be judged against the *screenshot*, not the target.

![stroke thickness](stroke_thickness.png)

## Known gaps / where to pick up

1. **Weak-band mapping** — the render places some weak echoes one intensity band above
   where the display draws them (teal vs faint blue). Not tunable with aggregate metrics:
   the target's legitimate weak-band mass (rims of strong arcs) is indistinguishable in a
   histogram from standalone weak arcs. Needs a **component-matching metric** (pair target
   arcs with render arcs; score matched/unmatched mass separately) — this is the next
   highest-value piece of work, and it also hardens the sweeps against speckle-gaming.
2. **Right view** — needs `h3-1/2/3` composited into one image in grayscale signal space
   (max-per-pixel is the first guess), then the same battery. Caution: on some frames the
   on-screen right view is *not* a composite; there is no marker for this yet, so expect
   (and tolerate) bad pairs.
3. **NMS onion rings** — per-sample radial NMS can split a radially thick school into
   concentric ridges. If it proves objectionable, switch to per-run peak bands (one ridge
   per contiguous echo run along range).
4. **Single-clip calibration** — everything above is one clip, one skin, one range
   setting (left R:304 m, G:15). Validating on a second clip (different range/gain/skin)
   is the cheapest way to find overfitting.

## Full history

The decision log — every experiment, rejected hypothesis, and measured number — lives in
[`kongsberg-training-utils`](https://github.com/viam-modules/kongsberg-training-utils) at `docs/render-calibration-roadmap.md`.
