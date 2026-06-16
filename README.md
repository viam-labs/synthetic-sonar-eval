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

| Flag | Default | Description |
|---|---|---|
| `--output` | `output` | Output directory (must match download) |
| `--fps` | `3` | Video frame rate |
| `--size` | `1500` | Sonar image size in pixels |

### Full run

```
make setup
make download SEQUENCE_ID=<id>
make render
```
