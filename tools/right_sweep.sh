#!/bin/bash
# Sweep the right-view composite knobs — EMA placement, composite NMS window,
# composite dbOffset, ping-ping level — and score each config against the
# screenshot right-view targets with tools/compare_components.py (spatial
# matching) plus tools/compare_1d.py (aggregate numbers).
#
# The composite window radius is the display's right-view range setting
# (clip-specific, read it off a screenshot); pass RANGE_M to override the
# default 182 (the primary calibration clip's R). VESSEL_X/VESSEL_Y are the
# vessel's fractional position in the on-screen view (display off-center
# mode; measure via ring-center/phase correlation) — defaults are the
# primary clip's measured values.
#
# Completed runs (metric/summary.json present) are skipped, so the grid can
# be extended across invocations without re-rendering.
#
# Usage: bash right_sweep.sh <sequence_dir>   (expects <sequence_dir>_targets/)
# Grid via env vars, e.g.:
#   EMAS="pre post" NMSS="0 2 5" OFFSETS="0 2.5" PPS="weak medium" \
#     JOBS=6 bash tools/right_sweep.sh <sequence_dir>
set -euo pipefail

CLIP=$1
T=${CLIP}_targets
SWEEP=$T/right_sweep
TOOLS="$(cd "$(dirname "$0")" && pwd)"
PY=/Users/robin/kongsberg/kongsberg-training-utils/.venv/bin/python
RENDER=${RENDER_BIN:-/tmp/sonar-render}
JOBS=${JOBS:-6}
RANGE_M=${RANGE_M:-182}
VESSEL_X=${VESSEL_X:-0.443}
VESSEL_Y=${VESSEL_Y:-0.489}

TAB=$SWEEP/tabular-h3-only
mkdir -p "$TAB"
for S in horizontal-h3-1-sensor horizontal-h3-2-sensor horizontal-h3-3-sensor; do
  [ -d "$TAB/$S" ] || cp -R "$CLIP/tabular/$S" "$TAB/"
done

mkdir -p "$SWEEP/configs" "$SWEEP/runs"
CONFIGS=$SWEEP/configs/list.txt
: > "$CONFIGS"
EMAS=${EMAS:-"pre post"}
NMSS=${NMSS:-"0 2 5"}
OFFSETS=${OFFSETS:-"0 2.5"}
PPS=${PPS:-"weak medium"}
for EMA in $EMAS; do for NMS in $NMSS; do for OFF in $OFFSETS; do for PP in $PPS; do
  NAME="ema${EMA}_nms${NMS}_off${OFF}_pp${PP}"
  printf '{"splatKernel": "bilinear", "heatmapMinThreshold": 0.125, "signalFloorDB": -96, "compositeMode": "max", "compositeEmaPlacement": "%s", "compositeRadialPeakWindow": %s, "compositeDbOffset": %s}\n' \
    "$EMA" "$NMS" "$OFF" > "$SWEEP/configs/$NAME.json"
  echo "$NAME $PP" >> "$CONFIGS"
done; done; done; done

render_one() {
  NAME=$1; PP=$2
  OUT=$SWEEP/runs/$NAME
  if [ ! -f "$OUT/metric/summary.json" ]; then
    mkdir -p "$OUT"
    "$RENDER" --output "$OUT" --tabular "$TAB" \
      --params "$SWEEP/configs/$NAME.json" --pingpingfilter "$PP" \
      --composite-range-m "$RANGE_M" \
      --composite-vessel-x "$VESSEL_X" --composite-vessel-y "$VESSEL_Y" --fps 3 \
      > "$OUT/render.log" 2>&1
    "$PY" "$TOOLS/compare_components.py" "$T/views_signal" "$OUT/sonar-signal/horizontal-h3-composite" \
      "$T/palette/palette.json" "$OUT/metric" --side right > "$OUT/metric.log" 2>&1
    "$PY" "$TOOLS/compare_1d.py" "$T/views_signal" "$OUT/sonar-signal/horizontal-h3-composite" \
      "$T/palette/palette.json" "$OUT/metric1d" --side right > "$OUT/metric1d.log" 2>&1
  fi
  echo "done: $NAME"
}
export -f render_one
export SWEEP T TOOLS PY RENDER TAB RANGE_M VESSEL_X VESSEL_Y

xargs -P "$JOBS" -n 2 bash -c 'set -eu; render_one "$@"' _ < "$CONFIGS"

"$PY" "$TOOLS/collect_gate_sweep.py" "$SWEEP"
