"""Visualize a render config against the screenshot targets, in color.

For each frame writes a three-panel composite:

    [ cropped screenshot view | blob target | render ]

The render panel is built from the grayscale signal image and colorized
through the palette measured from the screen's legend bar
(extract_palette.py), so all three panels speak the same color language
regardless of the renderer's colorStops. Also assembles the frames into an
MP4 for flipping through.

With --match a fourth panel shows the component-matching view (same masks
and tolerance as compare_components.py, whose code it imports): matched
echo mass white, target-only blue, render-only orange. Requires
views_signal/ under <targets_root> (invert_targets.py output).

Usage:
    python visualize_result.py <targets_root> <render_signal_dir> <out_dir>
                               [--side left] [--label render] [--fps 3]
                               [--match] [--match-tol-px 6]

<targets_root> is the *_targets directory (expects views/, views_blobs/,
palette/palette.json inside).
"""

import argparse
import json
import re
import sys
from datetime import datetime
from pathlib import Path

import cv2
import numpy as np

from compare_components import BGR_MATCHED, BGR_RENDER, BGR_TARGET, disk_mask, echo_masks

HEADER_H = 44
SEP_W = 6


def parse_target_ts(dirname: str) -> datetime | None:
    iso = dirname.split("+")[0]
    try:
        return datetime.fromisoformat(iso.replace("Z", "+00:00"))
    except ValueError:
        return None


def parse_render_ts(stem: str) -> datetime | None:
    m = re.match(r"(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-(\d{3})Z", stem)
    if not m:
        return None
    d, hh, mm, ss, ms = m.groups()
    return datetime.fromisoformat(f"{d}T{hh}:{mm}:{ss}.{ms}+00:00")


def colorize_signal(gray: np.ndarray, curve_bgr: np.ndarray) -> np.ndarray:
    """Map a signal image through the measured palette; 0 stays black."""
    out = curve_bgr[gray]
    out[gray == 0] = 0
    return out


def add_header(panel: np.ndarray, text: str) -> np.ndarray:
    header = np.zeros((HEADER_H, panel.shape[1], 3), np.uint8)
    cv2.putText(
        header, text, (12, HEADER_H - 14), cv2.FONT_HERSHEY_SIMPLEX,
        0.9, (220, 220, 220), 2, cv2.LINE_AA,
    )
    return np.vstack([header, panel])


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("targets_root", type=Path)
    parser.add_argument("render_signal_dir", type=Path)
    parser.add_argument("out_dir", type=Path)
    parser.add_argument("--side", choices=["left", "right"], default="left")
    parser.add_argument("--label", default="render", help="render panel title")
    parser.add_argument("--fps", type=int, default=3)
    parser.add_argument("--tolerance", type=float, default=0.6)
    parser.add_argument("--match", action="store_true", help="add component-matching panel")
    parser.add_argument("--match-tol-px", type=int, default=6)
    parser.add_argument("--rim-frac", type=float, default=0.97)
    args = parser.parse_args()

    palette = json.loads((args.targets_root / "palette" / "palette.json").read_text())
    curve_bgr = np.array(palette["colors_rgb"], np.float64)[:, ::-1].astype(np.uint8)
    u_min, u_max = palette["gate_u_min"], palette["gate_u_max"]
    kernel = cv2.getStructuringElement(
        cv2.MORPH_ELLIPSE, (2 * args.match_tol_px + 1, 2 * args.match_tol_px + 1)
    )
    disks: dict[tuple[int, int], np.ndarray] = {}

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
        sys.exit(f"no inputs: {len(targets)} targets, {len(renders)} renders")
    render_ts = np.array([t.timestamp() for t, _ in renders])

    args.out_dir.mkdir(parents=True, exist_ok=True)
    video = None
    n_done = 0
    for ts, tdir in targets:
        i = int(np.abs(render_ts - ts.timestamp()).argmin())
        if abs(render_ts[i] - ts.timestamp()) > args.tolerance:
            continue
        screenshot = cv2.imread(str(tdir / f"{args.side}.png"))
        blobs_path = (
            args.targets_root / "views_blobs" / tdir.name / f"{args.side}.png"
        )
        blobs = cv2.imread(str(blobs_path))
        signal = cv2.imread(str(renders[i][1]), cv2.IMREAD_GRAYSCALE)
        if screenshot is None or blobs is None or signal is None:
            continue
        h = screenshot.shape[0]
        render = cv2.resize(
            colorize_signal(signal, curve_bgr), (h, h),
            interpolation=cv2.INTER_AREA,
        )
        sep = np.full((h, SEP_W, 3), 90, np.uint8)
        panels = [
            add_header(screenshot, f"screenshot {args.side} view"),
            add_header(np.hstack([sep, blobs]), "target (blobs)"),
            add_header(np.hstack([sep, render]), args.label),
        ]
        if args.match:
            sig_path = (
                args.targets_root / "views_signal" / tdir.name / f"{args.side}.png"
            )
            t_sig = cv2.imread(str(sig_path), cv2.IMREAD_GRAYSCALE)
            if t_sig is None:
                continue
            r_sig = signal
            if r_sig.shape != t_sig.shape:
                r_sig = cv2.resize(
                    r_sig, t_sig.shape[::-1], interpolation=cv2.INTER_AREA
                )
            if t_sig.shape not in disks:
                disks[t_sig.shape] = disk_mask(t_sig.shape, args.rim_frac)
            mm = echo_masks(t_sig, r_sig, disks[t_sig.shape], u_min, u_max, kernel)
            overlay = np.zeros((*t_sig.shape, 3), np.uint8)
            overlay[mm["matched_t"] | mm["matched_r"]] = BGR_MATCHED
            overlay[mm["t_mask"] & ~mm["dil_r"]] = BGR_TARGET
            overlay[mm["r_mask"] & ~mm["dil_t"]] = BGR_RENDER
            overlay = cv2.resize(overlay, (h, h), interpolation=cv2.INTER_NEAREST)
            panels.append(
                add_header(
                    np.hstack([sep, overlay]),
                    "matched:white  target-only:blue  render-only:orange",
                )
            )
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
