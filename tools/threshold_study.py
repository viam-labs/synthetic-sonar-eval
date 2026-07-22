"""Visual study: suppressing low-intensity render signal at multiple cutoffs.

The renders carry weak content (sub-window and low-window arcs, noise rings)
that every metric ignores — below the palette's valid window it counts zero —
but that dominates visually. This writes, per frame:

    [ screenshot | target (blobs) | render raw | render >= t1 | >= t2 | ... ]

where each threshold panel zeroes signal pixels below the cutoff (in display
dB, u = (dB+100)/36) before colorizing through the measured palette. Also
assembles composite.mp4 for flipping through. Pick the floor by eye.

Usage:
    python threshold_study.py <targets_root> <render_signal_dir> <out_dir>
                              [--side left] [--thresholds-db -96 -93 -90]
                              [--label render] [--fps 3]
"""

import argparse
import json
from pathlib import Path

import cv2
import numpy as np

from visualize_result import (
    add_header,
    colorize_signal,
    parse_render_ts,
    parse_target_ts,
)

SEP_W = 6
HEADER_H = 44


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("targets_root", type=Path)
    parser.add_argument("render_signal_dir", type=Path)
    parser.add_argument("out_dir", type=Path)
    parser.add_argument("--side", choices=["left", "right"], default="left")
    parser.add_argument(
        "--thresholds-db", type=float, nargs="+", default=[-96.0, -93.0, -90.0]
    )
    parser.add_argument("--label", default="render", help="raw render panel title")
    parser.add_argument("--fps", type=int, default=3)
    parser.add_argument("--tolerance", type=float, default=0.6)
    args = parser.parse_args()

    palette = json.loads((args.targets_root / "palette" / "palette.json").read_text())
    curve_bgr = np.array(palette["colors_rgb"], np.float64)[:, ::-1].astype(np.uint8)

    targets = []
    for d in sorted((args.targets_root / "views").iterdir()):
        if d.is_dir() and not d.name.startswith("_"):
            ts = parse_target_ts(d.name)
            if ts is not None and (d / f"{args.side}.png").exists():
                targets.append((ts, d))
    renders = []
    for p in sorted(args.render_signal_dir.glob("*.png")):
        ts = parse_render_ts(p.stem)
        if ts is not None:
            renders.append((ts, p))
    if not targets or not renders:
        raise SystemExit(f"no inputs: {len(targets)} targets, {len(renders)} renders")
    render_ts = np.array([t.timestamp() for t, _ in renders])

    cut_gray = [
        max(0, int(np.ceil((db + 100.0) / 36.0 * 255))) for db in args.thresholds_db
    ]

    args.out_dir.mkdir(parents=True, exist_ok=True)
    video = None
    n_done = 0
    for ts, tdir in targets:
        i = int(np.abs(render_ts - ts.timestamp()).argmin())
        if abs(render_ts[i] - ts.timestamp()) > args.tolerance:
            continue
        screenshot = cv2.imread(str(tdir / f"{args.side}.png"))
        blobs = cv2.imread(
            str(args.targets_root / "views_blobs" / tdir.name / f"{args.side}.png")
        )
        signal = cv2.imread(str(renders[i][1]), cv2.IMREAD_GRAYSCALE)
        if screenshot is None or blobs is None or signal is None:
            continue
        h = screenshot.shape[0]
        sep = np.full((h, SEP_W, 3), 90, np.uint8)

        def render_panel(sig: np.ndarray, title: str) -> np.ndarray:
            color = cv2.resize(
                colorize_signal(sig, curve_bgr), (h, h), interpolation=cv2.INTER_AREA
            )
            return add_header(np.hstack([sep, color]), title)

        panels = [
            add_header(screenshot, f"screenshot {args.side} view"),
            add_header(np.hstack([sep, blobs]), "target (blobs)"),
            render_panel(signal, f"{args.label} (raw)"),
        ]
        for db, cut in zip(args.thresholds_db, cut_gray):
            sig_t = signal.copy()
            sig_t[sig_t < cut] = 0
            panels.append(render_panel(sig_t, f">= {db:g} dB"))
        frame = np.hstack(panels)
        stamp = ts.strftime("%H:%M:%S")
        cv2.putText(
            frame, stamp, (frame.shape[1] - 150, HEADER_H - 14),
            cv2.FONT_HERSHEY_SIMPLEX, 0.9, (150, 150, 150), 2, cv2.LINE_AA,
        )
        cv2.imwrite(str(args.out_dir / f"{tdir.name.split('+')[0]}.png"), frame)
        if video is None:
            video = cv2.VideoWriter(
                str(args.out_dir / "composite.mp4"),
                cv2.VideoWriter_fourcc(*"mp4v"), args.fps,
                (frame.shape[1], frame.shape[0]),
            )
        video.write(frame)
        n_done += 1
    if video is not None:
        video.release()
    print(f"done: {n_done} composites + composite.mp4 -> {args.out_dir}")


if __name__ == "__main__":
    main()
