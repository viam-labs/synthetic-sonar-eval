"""Estimate the rotation offset between target views and rendered signal.

For each target/render pair (both 8-bit signal-space grayscale), computes the
angular echo-mass profile around the disk center and finds the circular
cross-correlation peak: the rotation that best aligns the render onto the
target. If the offset is constant across frames it is a fixed angular
convention mismatch; if it tracks the per-ping heading_deg (read from the
matching tabular record), the render is world-referenced while the display is
bow-up.

Usage:
    python angle_check.py <targets_signal_tree> <render_signal_dir>
                          <tabular_dir> <palette_json> <out_dir> [--side left]
"""

import argparse
import csv
import json
import re
import sys
from datetime import datetime
from pathlib import Path

import cv2
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

N_THETA = 256
MIN_PIXELS = 200


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


def angular_profile(
    img: np.ndarray, u_min: float, u_max: float, rim_frac: float = 0.97
) -> tuple[np.ndarray, int]:
    """Echo-mass profile over theta (N_THETA bins), and the pixel count."""
    h, w = img.shape
    cy, cx = (h - 1) / 2, (w - 1) / 2
    ys, xs = np.nonzero(img)
    u = img[ys, xs].astype(np.float64) / 255.0
    r = np.sqrt((ys - cy) ** 2 + (xs - cx) ** 2)
    r_max = min(h, w) / 2 * rim_frac
    keep = (u >= u_min) & (u <= u_max) & (r < r_max) & (r > 0.05 * r_max)
    if keep.sum() == 0:
        return np.zeros(N_THETA), 0
    # Screen convention: theta measured clockwise from "up" (bow / north).
    theta = np.arctan2(xs[keep] - cx, -(ys[keep] - cy))
    bins = ((theta + np.pi) / (2 * np.pi) * N_THETA).astype(int) % N_THETA
    prof = np.bincount(bins, weights=u[keep], minlength=N_THETA)
    return prof, int(keep.sum())


def circular_xcorr_peak(a: np.ndarray, b: np.ndarray) -> tuple[float, float]:
    """Rotation of b (deg, clockwise) maximizing correlation with a."""
    a = a - a.mean()
    b = b - b.mean()
    denom = np.sqrt((a**2).sum() * (b**2).sum())
    if denom == 0:
        return np.nan, 0.0
    xc = np.fft.irfft(np.fft.rfft(a) * np.conj(np.fft.rfft(b)), n=N_THETA)
    k = int(xc.argmax())
    offset = k if k <= N_THETA // 2 else k - N_THETA
    return offset * 360.0 / N_THETA, float(xc[k] / denom)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("targets_signal_tree", type=Path)
    parser.add_argument("render_signal_dir", type=Path)
    parser.add_argument("tabular_dir", type=Path)
    parser.add_argument("palette_json", type=Path)
    parser.add_argument("out_dir", type=Path)
    parser.add_argument("--side", choices=["left", "right"], default="left")
    parser.add_argument("--tolerance", type=float, default=0.6)
    args = parser.parse_args()

    palette = json.loads(args.palette_json.read_text())
    u_min, u_max = palette["gate_u_min"], palette["gate_u_max"]

    targets = []
    for d in sorted(args.targets_signal_tree.iterdir()):
        if d.is_dir() and not d.name.startswith("_"):
            ts = parse_target_ts(d.name)
            if ts is not None and (d / f"{args.side}.png").exists():
                targets.append((ts, d / f"{args.side}.png"))
    renders = []
    for p in sorted(args.render_signal_dir.glob("*.png")):
        ts = parse_render_ts(p.stem)
        if ts is not None:
            renders.append((ts, p))
    if not targets or not renders:
        sys.exit("no inputs")
    render_ts = np.array([t.timestamp() for t, _ in renders])

    rows = []
    for ts, tpath in targets:
        i = int(np.abs(render_ts - ts.timestamp()).argmin())
        if abs(render_ts[i] - ts.timestamp()) > args.tolerance:
            continue
        t_img = cv2.imread(str(tpath), cv2.IMREAD_GRAYSCALE)
        r_img = cv2.imread(str(renders[i][1]), cv2.IMREAD_GRAYSCALE)
        if t_img is None or r_img is None:
            continue
        prof_t, n_t = angular_profile(t_img, u_min, u_max)
        prof_r, n_r = angular_profile(r_img, u_min, u_max)
        if n_t < MIN_PIXELS or n_r < MIN_PIXELS:
            continue
        offset_deg, peak = circular_xcorr_peak(prof_t, prof_r)
        heading = np.nan
        tab = args.tabular_dir / f"{renders[i][1].stem}.json"
        if tab.exists():
            heading = (
                json.loads(tab.read_text())
                .get("payload", {})
                .get("readings", {})
                .get("heading_deg", np.nan)
            )
        rows.append(
            {
                "ts": ts.isoformat(),
                "offset_deg": round(offset_deg, 1),
                "peak_corr": round(peak, 3),
                "heading_deg": heading,
                "n_target_px": n_t,
                "n_render_px": n_r,
            }
        )

    if not rows:
        sys.exit("no usable pairs (all below MIN_PIXELS)")
    args.out_dir.mkdir(parents=True, exist_ok=True)
    with open(args.out_dir / "angle_check.csv", "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)

    offsets = np.array([r["offset_deg"] for r in rows])
    headings = np.array([r["heading_deg"] for r in rows], dtype=float)
    peaks = np.array([r["peak_corr"] for r in rows])

    fig, ax = plt.subplots(figsize=(7, 6))
    sc = ax.scatter(headings, offsets, c=peaks, cmap="viridis", s=30)
    ax.plot([headings.min(), headings.max()], [headings.min(), headings.max()],
            color="#9CA3AF", linewidth=1, linestyle="--", label="offset = heading")
    ax.plot([headings.min(), headings.max()], [-headings.min(), -headings.max()],
            color="#9CA3AF", linewidth=1, linestyle=":", label="offset = -heading")
    ax.set_xlabel("heading (deg)")
    ax.set_ylabel("best rotation of render onto target (deg, cw)")
    ax.set_title(f"Rotation offset vs heading, {args.side} view (n={len(rows)})")
    fig.colorbar(sc, label="peak correlation")
    ax.legend(frameon=False)
    ax.grid(alpha=0.25, linewidth=0.5)
    fig.tight_layout()
    fig.savefig(args.out_dir / "angle_check.png", dpi=150)

    good = peaks >= np.median(peaks)
    print(
        json.dumps(
            {
                "n_pairs": len(rows),
                "median_offset_deg": float(np.median(offsets)),
                "offset_std_deg": round(float(offsets.std()), 1),
                "median_peak_corr": round(float(np.median(peaks)), 3),
                "corr_offset_vs_heading": round(
                    float(np.corrcoef(headings[good], offsets[good])[0, 1]), 3
                )
                if good.sum() > 2
                else None,
            },
            indent=1,
        )
    )


if __name__ == "__main__":
    main()
