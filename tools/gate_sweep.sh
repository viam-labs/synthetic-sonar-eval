#!/bin/bash
# Sweep the gate/gain knobs of the bilinear kernel — heatmapMinThreshold,
# dbOffset, radialPeakWindow, ping-ping level — for the left view
# (horizontal-h) and score each config against the screenshot targets with
# tools/compare_components.py (spatial matching) plus tools/compare_1d.py
# (legacy aggregate numbers, for continuity).
#
# Completed runs (metric/summary.json present) are skipped, so the grid can
# be extended across invocations without re-rendering.
#
# Usage: bash gate_sweep.sh <sequence_dir>   (expects <sequence_dir>_targets/)
# Grid via env vars, e.g.:
#   THRS="0.125 0.05" OFFSETS="0 1 2 3" PPS="weak medium" NMSS="2" \
#     JOBS=6 bash tools/gate_sweep.sh <sequence_dir>
set -euo pipefail

CLIP=$1
T=${CLIP}_targets
SWEEP=$T/gate_sweep
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
THRS=${THRS:-"0.125"}
OFFSETS=${OFFSETS:-"0"}
PPS=${PPS:-"medium"}
NMSS=${NMSS:-"2"}
for TH in $THRS; do for OFF in $OFFSETS; do for NMS in $NMSS; do for PP in $PPS; do
  NAME="thr${TH}_off${OFF}_nms${NMS}_pp${PP}"
  printf '{"splatKernel": "bilinear", "radialPeakWindow": %s, "heatmapMinThreshold": %s, "dbOffset": %s}\n' \
    "$NMS" "$TH" "$OFF" > "$SWEEP/configs/$NAME.json"
  echo "$NAME $PP" >> "$CONFIGS"
done; done; done; done

render_one() {
  NAME=$1; PP=$2
  OUT=$SWEEP/runs/$NAME
  if [ ! -f "$OUT/metric/summary.json" ]; then
    mkdir -p "$OUT"
    "$RENDER" --output "$OUT" --tabular "$TAB" \
      --params "$SWEEP/configs/$NAME.json" --pingpingfilter "$PP" --fps 3 \
      > "$OUT/render.log" 2>&1
    "$PY" "$TOOLS/compare_components.py" "$T/views_signal" "$OUT/sonar-signal/horizontal-h-sensor" \
      "$T/palette/palette.json" "$OUT/metric" --side left > "$OUT/metric.log" 2>&1
    "$PY" "$TOOLS/compare_1d.py" "$T/views_signal" "$OUT/sonar-signal/horizontal-h-sensor" \
      "$T/palette/palette.json" "$OUT/metric1d" --side left > "$OUT/metric1d.log" 2>&1
  fi
  echo "done: $NAME"
}
export -f render_one
export SWEEP T TOOLS PY RENDER TAB

xargs -P "$JOBS" -n 2 bash -c 'set -eu; render_one "$@"' _ < "$CONFIGS"

"$PY" "$TOOLS/collect_gate_sweep.py" "$SWEEP"
