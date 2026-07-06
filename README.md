# synthetic-sonar-eval

Downloads sonar and camera data from a Viam sequence, renders the sonar pings as heatmap images, and produces side-by-side videos pairing each sonar sensor with the screen1 camera feed.

## Prerequisites

- Go 1.21+
- [ffmpeg](https://ffmpeg.org/) with libx264 (`brew install ffmpeg`)
- [Viam CLI](https://docs.viam.com/cli/) (`brew install viam`)

## Usage

### 1. Setup

Logs in via the Viam CLI and writes your auth token to `.env`:

```
make setup
```

This creates a `.env` file containing `VIAM_AUTH_TOKEN`. Re-run any time your token expires.

### 2. Download

Downloads tabular sonar readings and binary camera images for a sequence:

```
make download SEQUENCE_ID=<id>
```

Downloads are checkpointed — if interrupted, re-running the same command resumes from where it left off.

**Output layout:**

```
output/
  tabular/
    horizontal-h-sensor/       # sonar readings per sensor
    horizontal-h3-1-sensor/
    horizontal-h3-2-sensor/
    horizontal-h3-3-sensor/
  images/
    screen1/                   # camera frames
  manifest.json
  progress.json
```

Optional flags (passed via `go run` directly if needed):

| Flag | Default | Description |
|---|---|---|
| `--output` | `output` | Output directory |
| `--page-size` | `100` | Page size for tabular pagination |

### 3. Render

Renders sonar pings as heatmap PNGs, encodes per-sensor MP4s, and creates side-by-side videos:

```
make render
```

To use custom render tuning, pass a params JSON file via `PARAMS`:

```
make render PARAMS=golden_params.json
```

`golden_params.json` in the repo root is the checked-in preset for heatmap colors, dB scaling, and sigma factors. Omit `PARAMS` to use the built-in defaults.

**Output layout:**

```
output/
  sonar-images/
    horizontal-h-sensor/       # rendered PNG frames
    horizontal-h-sensor.mp4
    horizontal-h3-1-sensor/
    horizontal-h3-1-sensor.mp4
    ...
  paired/
    horizontal-h-sensor.mp4    # sonar + screen1 side by side
    horizontal-h3-1-sensor.mp4
    ...
```

Optional flags:

| Flag / Make var | Default | Description |
|---|---|---|
| `--output` / `OUTPUT` | `output` | Output directory (must match download) |
| `--params` / `PARAMS` | _(none)_ | JSON file with render params (e.g. `golden_params.json`) |
| `--fps` / `FPS` | `3` | Video frame rate |
| `--size` | `1500` | Sonar image size in pixels |
| `--tabular` / `TABULAR` | `<output>/tabular` | Tabular JSON input directory |

### 4. Marker playback data

Pulls marker-placement sensor readings for a single part via Viam's `TabularDataByMQL` API,
for use with the [placement-playback](placement-playback) viewer:

```
make markers PART_ID=<part-id>
```

Optionally scope the pull to a time range (RFC3339 `time_received` bounds):

```
make markers PART_ID=<part-id> START=2026-07-05T00:00:00Z END=2026-07-06T00:00:00Z
```

Requires `ORG_ID` (or `VIAM_ORG_ID` in `.env`) in addition to the usual `VIAM_AUTH_TOKEN`.

Also pulls camera frames for the same part (via `BinaryDataByFilter` against the
`camera-save-predictions` component) and embeds them alongside the readings, so the viewer can show
the frame that matches the scrubber's current position. Pass `--skip-images` to skip this.

Also pulls and renders sonar ping data for the same part/window (using the same heatmap renderer as
`make render`) and embeds those frames too, so the viewer can show a synced sonar panel next to the
camera feed. Pass `--skip-sonar` to skip this. Sonar queries match on `capture_day` (an indexed
day-bucket field) rather than a `time_received` range, since a `$expr`/`$toDate` range comparison
isn't index-backed and reliably times out once a sensor has more than a few thousand documents —
some sonar sensors here have 250k+ pings across just a few days. `cmd/mqlquery` is the standalone
tool used to discover and confirm this.

**Output layout:**

```
output/
  marker-playback/
    <part-id>/
      readings.json            # { "readings": [...], "images": [...], "sonarFrames": [...] } — load directly into placement-playback
      images/                  # raw camera frames, one file per capture (for archival)
      sonar-images/<sensor>/   # raw rendered sonar heatmaps, one file per ping (for archival)
```

Optional flags (passed via `go run` directly if needed):

| Flag | Default | Description |
|---|---|---|
| `--org-id` / `ORG_ID` | _(from `VIAM_ORG_ID`)_ | Organization ID (required) |
| `--component-name` | `placemarker-synth-ai` | Component name to match for readings |
| `--component-type` | `rdk:component:sensor` | Component type to match for readings |
| `--method-name` | `Readings` | Method name to match for readings |
| `--start` / `START` | _(none)_ | Only data at/after this RFC3339 time |
| `--end` / `END` | _(none)_ | Only data at/before this RFC3339 time |
| `--limit` | `0` (no cap) | Caps matched reading documents via an MQL `$limit` stage |
| `--image-component-name` | `camera-save-predictions` | Component name to match for images |
| `--image-component-type` | `rdk:component:camera` | Component type to match for images |
| `--image-method-name` | _(none)_ | Method name to match for images |
| `--image-page-size` | `50` | Page size for image pagination |
| `--image-limit` | `1000` (no cap if `0`) | Caps images fetched |
| `--skip-images` | `false` | Skip fetching camera images |
| `--sonar-component-names` | `horizontal-h-sensor,horizontal-h3-1-sensor,horizontal-h3-2-sensor,horizontal-h3-3-sensor` | Comma-separated sonar sensor component names to render |
| `--sonar-component-type` | `rdk:component:sensor` | Component type to match for sonar |
| `--sonar-method-name` | `Readings` | Method name to match for sonar |
| `--sonar-limit` | `60` | Number of pings to render per sonar sensor, spread evenly across the days in the window |
| `--sonar-size` | `500` | Rendered sonar heatmap size in pixels |
| `--sonar-params` | _(none)_ | Optional JSON file with sonar render params (same shape as `cmd/render --params`) |
| `--skip-sonar` | `false` | Skip fetching and rendering sonar data |
| `--output` / `OUTPUT` | `output` | Output directory |

### 5. Ad-hoc MQL queries

`cmd/mqlquery` is a diagnostic tool for exploring what data exists for a part/component/day
combination, auto-resolving the owning organization/robot/location from a part ID:

```
go run ./cmd/mqlquery --part-id <id> --component-name <name> --capture-day 2026-07-05 --limit 10 --output out_test.txt
```

### Full run

```
make setup
make download SEQUENCE_ID=<id>
make render PARAMS=golden_params.json
```
