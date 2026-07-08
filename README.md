# synthetic-sonar-eval

Tools for pulling sonar/camera data from Viam and evaluating the fish-detection model against it.
The repo supports two independent workflows that share the same download/render/detect building
blocks:

- **Sequence eval** — download a whole recorded sequence (or a raw time range), render the sonar
  pings as heatmap video, run the ONNX detector over the results, and (optionally) compare
  detections on the real screen1 screenshots against the synthetic sonar renders.
- **Marker playback** — pull marker-placement ground truth for a part/time-window, sync it with
  camera + sonar frames from that same window, optionally run detection, and hand it all to the
  external `placement-playback` viewer as one JSON file.

```mermaid
flowchart TB
    subgraph pathA["Path A — Sequence eval"]
        direction TB
        A1["make download\nSEQUENCE_ID=&lt;id&gt;  (or START/END)"]
        A2["make render"]
        A3["make detect-single / detect-dir"]
        A4["make compare\n(screen1 vs. synthetic renders)"]
        A1 --> A2 --> A3
        A2 --> A4
    end

    subgraph pathB["Path B — Marker playback"]
        direction TB
        B1["make markers\nPART_ID=&lt;id&gt; START=&lt;t0&gt; END=&lt;t1&gt;"]
        B2["fetch marker readings\n(placemarker-synth-ai)"]
        B3["reuse/download camera + sonar\nfor the padded marker window"]
        B4["render sonar frames"]
        B5["optional: DETECT=1\nrun ONNX detector"]
        B6["readings.json\n(→ placement-playback viewer)"]
        B1 --> B2 --> B3 --> B4 --> B5 --> B6
    end
```

`make full` runs Path A's download → render → compare steps back to back as one command — see
[Full runs](#full-runs).

## Prerequisites

- Go (version pinned in `go.mod`)
- [ffmpeg](https://ffmpeg.org/) with libx264 (`brew install ffmpeg`) — used by `make render` and by
  `make compare` (to encode the montage video)
- [Viam CLI](https://docs.viam.com/cli/) (`brew install viam`) — used by `make setup`
- [onnxruntime](https://github.com/microsoft/onnxruntime) (`brew install onnxruntime`) — used by
  anything that runs detection (`make detect-single`, `make detect-dir`, `make compare`,
  `make markers DETECT=1`)

## Component names

Both workflows talk to a fixed set of Viam component names. These aren't configurable via `make`
(they're defaults baked into the Go flags), so a part must expose components with these names for
the tools to find data:

| Data | Component name(s) | Component type | Method |
|---|---|---|---|
| Sonar (4 fans) | `horizontal-h-sensor`, `horizontal-h3-1-sensor`, `horizontal-h3-2-sensor`, `horizontal-h3-3-sensor` | `rdk:component:sensor` | `Readings` |
| Screen camera — time-range download / `make markers` | `camera-save-predictions` | `rdk:component:camera` | `BinaryDataByFilter` |
| Screen camera — sequence download | whatever the sequence's binary data reports (in practice `screen1`) | — | — |
| Marker placements (real + synthetic) | `placemarker-synth-ai` | `rdk:component:sensor` | `Readings` |

## Repo layout

```mermaid
flowchart LR
    subgraph cmd["cmd/ (binaries)"]
        download["download"]
        render["render"]
        detect["detect"]
        compare["compare"]
        markerplayback["markerplayback"]
    end
    subgraph internal["internal/ (shared logic)"]
        fetch["fetch\n(gRPC download, caching, manifest)"]
        sonar["sonar\n(heatmap rendering)"]
        detector["detector\n(ONNX inference, background strip)"]
        cmp["compare\n(frame alignment, annotate, montage/video)"]
    end

    download --> fetch
    markerplayback --> fetch
    render --> sonar
    markerplayback --> sonar
    detect --> detector
    markerplayback -.->|"--detect"| detector
    compare --> cmp
    cmp --> detector
    cmp --> fetch
```

## Usage

### 1. Setup

Logs in via the Viam CLI and writes your auth token to `.env`:

```
make setup
```

This creates a `.env` file containing `VIAM_AUTH_TOKEN`. Re-run any time your token expires. Add
`VIAM_ORG_ID=<id>` to `.env` yourself (or pass `ORG_ID=<id>` on the command line) — it's required
by any command that queries by time range.

### 2. Download (Path A)

`cmd/download` supports two mutually exclusive modes, both keyed on `PART_ID`:

**Mode A — whole recorded sequence:**

```
make download PART_ID=<part-id> SEQUENCE_ID=<sequence-id>
```

Downloads tabular sonar readings and binary camera images for the sequence via Viam's internal
sequence API (gRPC reflection). Resumable — if interrupted, re-running the same command picks up
from `progress.json`.

**Mode B — raw time range:**

```
make download PART_ID=<part-id> START=2026-07-05T00:00:00Z END=2026-07-06T00:00:00Z ORG_ID=<org-id>
```

Downloads screen images (`camera-save-predictions`) and sonar readings (all 4 sensors, via
`TabularDataByMQL` bucketed by `capture_day`) for the window. Windows are capped at **3 days**
(`fetch.MaxQueryWindow`) since some sonar sensors log 250k+ pings across just a few days.

Both modes write into a cache keyed by part ID + a hash of the mode's parameters, so re-running
with the same arguments is a cheap no-op:

```
output/
  <part-id>/
    <hash>/                     # sha256(sequence-id) or sha256(org-id|start|end)
      tabular/
        horizontal-h-sensor/       # sonar readings per sensor
        horizontal-h3-1-sensor/
        horizontal-h3-2-sensor/
        horizontal-h3-3-sensor/
      images/                     # camera frames (screen1 / camera-save-predictions)
      manifest.json
      progress.json               # sequence mode only (checkpointing)
```

Optional flags (passed via `go run` directly if needed):

| Flag | Default | Description |
|---|---|---|
| `--output` | `output` | Output directory |
| `--page-size` | `100` | Page size for tabular pagination (sequence mode) |
| `--image-page-size` | `50` | Page size for image pagination (time-range mode) |

### 3. Render (Path A)

Point `OUTPUT` at the specific download to render, renders sonar pings as heatmap PNGs, encodes
per-sensor MP4s, and creates side-by-side videos:

```
make render OUTPUT=output/<part-id>/<hash>
```

To use custom render tuning, pass a params JSON file via `PARAMS`:

```
make render OUTPUT=output/<part-id>/<hash> PARAMS=params/blackbg.json
```

`params/blackbg.json` in the repo is the checked-in calibrated preset for heatmap colors, dB
scaling, and sigma factors. Omit `PARAMS` to use the built-in defaults.

**Output layout:**

```
output/<part-id>/<hash>/
  sonar-images/
    horizontal-h-sensor/       # rendered PNG frames
    horizontal-h-sensor.mp4
    horizontal-h3-1-sensor/
    horizontal-h3-1-sensor.mp4
    ...
  paired/
    horizontal-h-sensor.mp4    # sonar + screen camera side by side
    horizontal-h3-1-sensor.mp4
    ...
```

Optional flags:

| Flag / Make var | Default | Description |
|---|---|---|
| `--output` / `OUTPUT` | `output` | Output directory (must match download) |
| `--params` / `PARAMS` | _(none)_ | JSON file with render params (e.g. `params/blackbg.json`) |
| `--fps` / `FPS` | `3` | Video frame rate |
| `--size` | `1500` | Sonar image size in pixels |
| `--tabular` / `TABULAR` | `<output>/tabular` | Tabular JSON input directory |

### 4. Detect (Path A)

`cmd/detect` runs the `omni-detector-fcos-0_0_4` ONNX model directly on a single image or a
directory of images (e.g. the `sonar-images/` or `images/` a download/render just produced) — no
part ID or network access required:

```
make detect-single IMAGE=output/<part-id>/<hash>/sonar-images/horizontal-h-sensor/<frame>.png
make detect-dir    DIR=output/<part-id>/<hash>/sonar-images/horizontal-h-sensor
```

Optional flags: `MODEL_DIR` (default `omni-detector-fcos-0_0_4`) and `CONFIDENCE` (default `0.6`),
both settable as `make` variables.

### 5. Compare (Path A)

`cmd/compare` is a Go port of kongsberg-training-utils'
`compare_synthetic_vs_screenshot.py`: it runs the ONNX detector on the real `screen1` screenshots
(a quad-ping composite) and on the four single-fan synthetic renders from the same download/render
directory, aligns them by nearest timestamp into one frame-group per screen1 frame, and reports how
fish-blob counts compare — `screen1` count vs. `fan_sum` (sum of the 4 fans, an over-count upper
bound since a fish visible in multiple fans is counted twice). This is a fidelity/sanity signal, not
an accuracy metric — there's no ground truth, so count agreement doesn't imply either source is
correct.

```
make compare OUTPUT=output/<part-id>/<hash>
```

Screenshots are background-stripped before detection (k-means-estimated background color, pixels
within `STRIP_DIST` zeroed out) to match the model's training/serving pipeline; synthetic renders
are never stripped, since they're already rendered on a matching black background.

Progress streams to stdout per image (detection is the slow step — one ONNX inference per distinct
screen1/render image), and unless `NO_VISUALIZE=1`, `cmd/compare` also draws annotated boxes on
copies of every image, stitches each frame-group into a side-by-side montage, and encodes the
montage sequence into an MP4.

**Output layout:**

```
output/<part-id>/<hash>/<RESULTS_DIR>/     # RESULTS_DIR defaults to detector-eval
  counts.json                # full per-detection records + summary (screen1 vs. fan_sum totals)
  counts.csv                 # per-group summary (one row per screen1 frame)
  annotated/
    screen1/                 # annotated copies of every screen1 screenshot
    horizontal-h-sensor/     # annotated copies of every render, per fan
    ...
  montages/
    group_0000.png           # screen1 + its 4 nearest-in-time fan renders, side by side
    ...
  montage.mp4                # the montage sequence encoded as video (unless NO_VISUALIZE=1)
```

Optional flags:

| Flag / Make var | Default | Description |
|---|---|---|
| `--output` / `OUTPUT` | _(required)_ | Download+render directory (with `manifest.json`, `images/screen1/`, `sonar-images/<fan>/`) |
| `--model-dir` / `MODEL_DIR` | `omni-detector-fcos-0_0_4` | Directory containing `model.onnx` + `labels.txt` |
| `--confidence` / `CONFIDENCE` | `0.6` | Minimum detection confidence |
| `--fish-class` | `human_annotated_positive_fish_blob` | Class name counted as a fish blob |
| `--screenshot-strip-dist` / `STRIP_DIST` | `150` | Background-strip distance (8-bit RGB) applied to screen1 screenshots only; `0` disables it |
| `--results-dirname` / `RESULTS_DIR` | `detector-eval` | Subdirectory of `OUTPUT` for results |
| `--fps` / `FPS` | `3` | Frame rate for `montage.mp4` |
| `--no-visualize` / `NO_VISUALIZE=1` | `false` | Skip annotated images/montages/video (counts only — much faster) |
| `--onnxruntime-lib` | _(auto-detected)_ | Path to `libonnxruntime.{dylib,so}` |

### 6. Marker playback (Path B)

Pulls marker-placement sensor readings (`placemarker-synth-ai`) for a single part over
`[START, END]` via `TabularDataByMQL`, then automatically syncs in the camera + sonar data for that
same window and writes a single JSON file for the `placement-playback` viewer (now a separate
repo — load `readings.json` directly into it):

```
make markers PART_ID=<part-id> ORG_ID=<org-id> START=2026-07-05T00:00:00Z END=2026-07-06T00:00:00Z
```

`ORG_ID` is required (flag or `VIAM_ORG_ID` in `.env`). `START`/`END` are required and, like
download mode B, capped at a 3-day window.

What it does, step by step:

1. Queries `placemarker-synth-ai` readings for `PART_ID` in `[START, END]`.
2. Narrows to the actual span the returned readings cover (padded ±5m via `--window-pad`), so the
   camera/sonar pull below isn't wasted on the whole `START`/`END` window.
3. Ensures screen images (`camera-save-predictions`) and sonar readings (all 4 sensors) for that
   padded window are downloaded — reusing the cache from step 2's mode-B download if one already
   exists for the same part/org/window.
4. Renders the sonar readings to heatmap PNGs (same renderer as `make render`).
5. **Optional** (`DETECT=1`): runs the ONNX detector over every camera frame and sonar frame and
   attaches the detections. A missing onnxruntime lib/model is a soft failure — the rest of the
   pull still succeeds, just without detections.
6. Writes everything (readings + base64-embedded images/sonar frames + any detections) to one JSON
   file for the viewer.

```
make markers PART_ID=<part-id> ORG_ID=<org-id> START=... END=... DETECT=1
```

**Output layout:**

```
output/<part-id>/<hash>/          # same cache dir shape as download mode B
  marker-playback/
    readings.json            # { "readings": [...], "images": [...], "sonarFrames": [...] } — load directly into placement-playback
```

Optional flags (passed via `go run` directly if needed):

| Flag | Default | Description |
|---|---|---|
| `--org-id` / `ORG_ID` | _(from `VIAM_ORG_ID`)_ | Organization ID (required) |
| `--start` / `START` | _(none, required)_ | Only readings at/after this RFC3339 `time_received` |
| `--end` / `END` | _(none, required)_ | Only readings at/before this RFC3339 `time_received` |
| `--window-pad` | `5m` | Padding applied around the placed-marker span when scoping the camera/sonar download |
| `--image-page-size` | `50` | Page size for image pagination |
| `--detect` / `DETECT=1` | `false` | Run object detection on fetched images/sonar frames and attach results (opt-in) |
| `--model-dir` / `MODEL_DIR` | `omni-detector-fcos-0_0_4` | Directory containing `model.onnx` + `labels.txt` |
| `--confidence` / `CONFIDENCE` | `0.6` | Minimum detection confidence to record |
| `--onnxruntime-lib` | _(auto-detected)_ | Path to `libonnxruntime.{dylib,so}` |
| `--output` / `OUTPUT` | `output` | Output directory |

### 7. Build binaries

```
make build
```

Compiles `bin/download`, `bin/render`, `bin/markerplayback`, `bin/detect`, and `bin/compare`.

### Full runs

**Path A — sequence eval, one command:**

```
make setup
make full PART_ID=<part-id> SEQUENCE_ID=<sequence-id>
```

`make full` runs download → render → compare back to back: download's own progress streams live,
then (no network required) it resolves the same `output/<part-id>/<hash>` directory the download
just wrote to (via `download --print-dir`) and feeds it straight into `render` and `compare`. It
accepts the same variables as the individual steps (`PARAMS`, `FPS`, `MODEL_DIR`, `CONFIDENCE`,
`STRIP_DIST`, `RESULTS_DIR`, `NO_VISUALIZE`, or `ORG_ID`/`START`/`END` instead of `SEQUENCE_ID`):

```
make full PART_ID=<part-id> SEQUENCE_ID=<sequence-id> PARAMS=params/blackbg.json NO_VISUALIZE=1
make full PART_ID=<part-id> ORG_ID=<org-id> START=2026-07-05T00:00:00Z END=2026-07-06T00:00:00Z
```

**Path A — sequence eval, step by step** (same result, more control between steps):

```
make setup
make download PART_ID=<part-id> SEQUENCE_ID=<sequence-id>
make render OUTPUT=output/<part-id>/<hash> PARAMS=params/blackbg.json
make detect-dir DIR=output/<part-id>/<hash>/sonar-images
make compare OUTPUT=output/<part-id>/<hash>
```

**Path B — marker playback, with detection:**

```
make setup
make markers PART_ID=<part-id> ORG_ID=<org-id> START=2026-07-05T00:00:00Z END=2026-07-06T00:00:00Z DETECT=1
```
