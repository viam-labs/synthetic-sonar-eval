#!/bin/bash
# Sweep splat-geometry render params (heatmap sigma factors, min threshold,
# ping-ping level) for the left view (horizontal-h) and score each config
# against the screenshot targets with tools/compare_1d.py.
#
# Renders only the horizontal-h stream (the sensor dir is copied with its
# folder name intact: the ping-ping filter groups streams by top-level dir).
# Ping-ping "off" is not sweepable: the renderer only writes the grayscale
# signal images when the filter is on.
#
# Usage: bash splat_sweep.sh <sequence_dir>   (expects <sequence_dir>_targets/)
set -euo pipefail

CLIP=$1
T=${CLIP}_targets
SWEEP=$T/splat_sweep
TOOLS="$(cd "$(dirname "$0")" && pwd)"
PY=/Users/robin/kongsberg/kongsberg-training-utils/.venv/bin/python
RENDER=${RENDER_BIN:-/tmp/sonar-render}
JOBS=${JOBS:-6}

TAB=$SWEEP/tabular-h-only
mkdir -p "$TAB"
[ -d "$TAB/horizontal-h-sensor" ] || cp -R "$CLIP/tabular/horizontal-h-sensor" "$TAB/"

mkdir -p "$SWEEP/configs" "$SWEEP/runs"
CONFIGS=$SWEEP/configs/list.txt
: > "$CONFIGS"
RANGES=${RANGES:-"0.5 0.75 1.0 1.5"}
ARCS=${ARCS:-"0.25 0.5"}
THRS=${THRS:-"0.03 0.1"}
for R in $RANGES; do for A in $ARCS; do for TH in $THRS; do
  NAME="r${R}_a${A}_t${TH}_ppmedium"
  printf '{"heatmapRangeSigmaFactor": %s, "heatmapArcSigmaFactor": %s, "heatmapMinThreshold": %s}\n' \
    "$R" "$A" "$TH" > "$SWEEP/configs/$NAME.json"
  echo "$NAME medium" >> "$CONFIGS"
done; done; done
if [ -z "${SKIP_PP:-}" ]; then
  for PP in weak strong; do
    NAME="r1.5_a0.5_t0.03_pp${PP}"
    printf '{"heatmapRangeSigmaFactor": 1.5, "heatmapArcSigmaFactor": 0.5, "heatmapMinThreshold": 0.03}\n' \
      > "$SWEEP/configs/$NAME.json"
    echo "$NAME $PP" >> "$CONFIGS"
  done
fi

render_one() {
  NAME=$1; PP=$2
  OUT=$SWEEP/runs/$NAME
  mkdir -p "$OUT"
  "$RENDER" --output "$OUT" --tabular "$TAB" \
    --params "$SWEEP/configs/$NAME.json" --pingpingfilter "$PP" --fps 3 \
    > "$OUT/render.log" 2>&1
  "$PY" "$TOOLS/compare_1d.py" "$T/views_signal" "$OUT/sonar-signal/horizontal-h-sensor" \
    "$T/palette/palette.json" "$OUT/metric" --side left > "$OUT/metric.log" 2>&1
  echo "done: $NAME"
}
export -f render_one
export SWEEP T TOOLS PY RENDER TAB

xargs -P "$JOBS" -n 2 bash -c 'set -eu; render_one "$@"' _ < "$CONFIGS"

"$PY" "$TOOLS/collect_sweep.py" "$SWEEP"
