"""1-D signal-space comparison: target views vs rendered sonar signal.

Pairs each target signal image (invert_targets.py output) with the
nearest-in-time render signal image (cmd/render's sonar-signal/ output) and
compares their intensity distributions inside the sonar disk, restricted to
the gate-valid u window from the palette JSON (the target can only contain
echo-gate colors, so the render histogram is cut to the same window).

Both inputs are 8-bit grayscale in the same unit: v/255 = u = (dB + 100)/36.

Outputs to <out_dir>:
    per_frame.csv        per-frame stats (EMD, mean shift, coverage, ...)
    pooled_hist.png      pooled intensity histogram overlay (dB axis)
    per_frame.png        EMD and coverage time series
    eyeball/*.png        [target | render] grayscale side-by-sides
    summary.json         pooled metrics

Usage:
    python compare_1d.py <targets_signal_tree> <render_signal_dir>
                         <palette_json> <out_dir> [--side left]
"""

import argparse
import csv
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path

import cv2
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

TARGET_COLOR = "#2563EB"  # blue
RENDER_COLOR = "#EA8600"  # orange

N_BINS = 72
N_EYEBALL = 6


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


def disk_mask(shape: tuple[int, int], rim_frac: float) -> np.ndarray:
    h, w = shape
    yy, xx = np.mgrid[:h, :w]
    cy, cx = (h - 1) / 2, (w - 1) / 2
    r = min(h, w) / 2 * rim_frac
    return (yy - cy) ** 2 + (xx - cx) ** 2 <= r * r


def emd_1d(hist_a: np.ndarray, hist_b: np.ndarray, bin_width: float) -> float:
    """Earth mover's distance between two normalized 1-D histograms."""
    cdf_a = np.cumsum(hist_a)
    cdf_b = np.cumsum(hist_b)
    return float(np.abs(cdf_a - cdf_b).sum() * bin_width)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("targets_signal_tree", type=Path)
    parser.add_argument("render_signal_dir", type=Path)
    parser.add_argument("palette_json", type=Path)
    parser.add_argument("out_dir", type=Path)
    parser.add_argument("--side", choices=["left", "right"], default="left")
    parser.add_argument("--rim-frac", type=float, default=0.97)
    parser.add_argument("--tolerance", type=float, default=0.6, help="max pairing gap (s)")
    args = parser.parse_args()

    palette = json.loads(args.palette_json.read_text())
    u_min, u_max = palette["gate_u_min"], palette["gate_u_max"]

    targets = []
    for d in sorted(args.targets_signal_tree.iterdir()):
        if not d.is_dir() or d.name.startswith("_"):
            continue
        ts = parse_target_ts(d.name)
        if ts is not None and (d / f"{args.side}.png").exists():
            targets.append((ts, d / f"{args.side}.png"))
    renders = []
    for p in sorted(args.render_signal_dir.glob("*.png")):
        ts = parse_render_ts(p.stem)
        if ts is not None:
            renders.append((ts, p))
    if not targets or not renders:
        sys.exit(f"no inputs: {len(targets)} targets, {len(renders)} renders")
    render_ts = np.array([t.timestamp() for t, _ in renders])

    bins = np.linspace(u_min, u_max, N_BINS + 1)
    bin_width_db = (bins[1] - bins[0]) * 36.0
    pooled_t = np.zeros(N_BINS)
    pooled_r = np.zeros(N_BINS)
    rows = []
    pairs = []
    masks: dict[tuple[int, int], np.ndarray] = {}

    for ts, tpath in targets:
        i = int(np.abs(render_ts - ts.timestamp()).argmin())
        gap = abs(render_ts[i] - ts.timestamp())
        if gap > args.tolerance:
            continue
        t_img = cv2.imread(str(tpath), cv2.IMREAD_GRAYSCALE)
        r_img = cv2.imread(str(renders[i][1]), cv2.IMREAD_GRAYSCALE)
        if t_img is None or r_img is None:
            continue
        stats = {"ts": ts.isoformat(), "gap_s": round(gap, 3)}
        for name, img in (("target", t_img), ("render", r_img)):
            if img.shape not in masks:
                masks[img.shape] = disk_mask(img.shape, args.rim_frac)
            m = masks[img.shape]
            u = img[m].astype(np.float64) / 255.0
            sel = u[(u >= u_min) & (u <= u_max)]
            hist, _ = np.histogram(sel, bins=bins)
            stats[f"n_{name}"] = len(sel)
            stats[f"cov_{name}"] = len(sel) / m.sum()
            stats[f"mean_db_{name}"] = -100 + 36 * sel.mean() if len(sel) else np.nan
            fill = (
                np.sqrt(
                    ((np.argwhere(img > 0) - (np.array(img.shape) - 1) / 2) ** 2).sum(1)
                ).max()
                / (min(img.shape) / 2)
                if (img > 0).any()
                else 0.0
            )
            stats[f"fill_radius_{name}"] = round(float(fill), 3)
            if name == "target":
                hist_t = hist
            else:
                hist_r = hist
        pooled_t += hist_t
        pooled_r += hist_r
        nt, nr = hist_t.sum(), hist_r.sum()
        if nt and nr:
            stats["emd_db"] = round(
                emd_1d(hist_t / nt, hist_r / nr, bins[1] - bins[0]) * 36.0, 3
            )
        else:
            stats["emd_db"] = np.nan
        stats["shift_db"] = round(stats["mean_db_render"] - stats["mean_db_target"], 3)
        rows.append(stats)
        pairs.append((tpath, renders[i][1]))

    if not rows:
        sys.exit("no pairs within tolerance")
    args.out_dir.mkdir(parents=True, exist_ok=True)

    with open(args.out_dir / "per_frame.csv", "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)

    nt, nr = pooled_t.sum(), pooled_r.sum()
    pooled_emd_db = emd_1d(pooled_t / nt, pooled_r / nr, bins[1] - bins[0]) * 36.0
    centers_db = -100 + 36 * (bins[:-1] + bins[1:]) / 2
    mean_db_t = float((centers_db * pooled_t).sum() / nt)
    mean_db_r = float((centers_db * pooled_r).sum() / nr)
    summary = {
        "n_pairs": len(rows),
        "u_window": [u_min, u_max],
        "pooled_emd_db": round(pooled_emd_db, 3),
        "pooled_mean_db_target": round(mean_db_t, 2),
        "pooled_mean_db_render": round(mean_db_r, 2),
        "pooled_shift_db": round(mean_db_r - mean_db_t, 2),
        "median_cov_target": round(float(np.median([r["cov_target"] for r in rows])), 5),
        "median_cov_render": round(float(np.median([r["cov_render"] for r in rows])), 5),
        "median_fill_radius_target": float(np.median([r["fill_radius_target"] for r in rows])),
        "median_fill_radius_render": float(np.median([r["fill_radius_render"] for r in rows])),
    }
    (args.out_dir / "summary.json").write_text(json.dumps(summary, indent=1))

    # Pooled histogram overlay.
    fig, ax = plt.subplots(figsize=(8, 4.5))
    ax.stairs(pooled_t / nt, -100 + 36 * bins, color=TARGET_COLOR, label="target (screenshot)", linewidth=2)
    ax.stairs(pooled_r / nr, -100 + 36 * bins, color=RENDER_COLOR, label="render", linewidth=2)
    ax.set_xlabel("echo strength (dB)")
    ax.set_ylabel("fraction of echo pixels")
    ax.set_title(f"Pooled intensity distribution, {args.side} view — EMD {pooled_emd_db:.2f} dB")
    ax.legend(frameon=False)
    ax.grid(alpha=0.25, linewidth=0.5)
    ax.spines[["top", "right"]].set_visible(False)
    fig.tight_layout()
    fig.savefig(args.out_dir / "pooled_hist.png", dpi=150)
    plt.close(fig)

    # Per-frame time series: EMD, then coverage (separate axes, no dual axis).
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(8, 6), sharex=True)
    x = np.arange(len(rows))
    ax1.plot(x, [r["emd_db"] for r in rows], color=TARGET_COLOR, linewidth=2)
    ax1.set_ylabel("EMD (dB)")
    ax1.set_title(f"Per-frame comparison, {args.side} view")
    ax2.plot(x, [r["cov_target"] * 100 for r in rows], color=TARGET_COLOR, linewidth=2, label="target")
    ax2.plot(x, [r["cov_render"] * 100 for r in rows], color=RENDER_COLOR, linewidth=2, label="render")
    ax2.set_ylabel("echo coverage (% of disk)")
    ax2.set_xlabel("frame")
    ax2.legend(frameon=False)
    for ax in (ax1, ax2):
        ax.grid(alpha=0.25, linewidth=0.5)
        ax.spines[["top", "right"]].set_visible(False)
    fig.tight_layout()
    fig.savefig(args.out_dir / "per_frame.png", dpi=150)
    plt.close(fig)

    # Eyeball side-by-sides (gamma-boosted for visibility).
    eyeball = args.out_dir / "eyeball"
    eyeball.mkdir(exist_ok=True)
    for k in np.linspace(0, len(pairs) - 1, min(N_EYEBALL, len(pairs))).astype(int):
        tpath, rpath = pairs[k]
        t_img = cv2.imread(str(tpath), cv2.IMREAD_GRAYSCALE)
        r_img = cv2.imread(str(rpath), cv2.IMREAD_GRAYSCALE)
        r_img = cv2.resize(r_img, t_img.shape[::-1], interpolation=cv2.INTER_AREA)
        boost = lambda im: (np.sqrt(im / 255.0) * 255).astype(np.uint8)
        sep = np.full((t_img.shape[0], 4), 128, np.uint8)
        cv2.imwrite(
            str(eyeball / f"{k:03d}_{tpath.parent.name.split('+')[0]}.png"),
            np.hstack([boost(t_img), sep, boost(r_img)]),
        )

    print(json.dumps(summary, indent=1))


if __name__ == "__main__":
    main()
