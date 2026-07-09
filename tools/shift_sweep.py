"""Test whether a constant dB offset explains the target/render mismatch.

Pools per-level intensity histograms (255 levels = the 8-bit signal
quantization) of target and render pixels inside the disk, then sweeps a
constant dB offset applied to the render. For each offset it reports, inside
the gate-valid u window: EMD between the normalized distributions, and the
coverage ratio (render echo pixels per disk pixel / target ditto).

If one offset brings both EMD to ~0 and coverage ratio to ~1, the display's
gain (e.g. the G:15 setting) is a pure dB shift of the renderer's fixed
[-100, -64] window. If EMD minimizes where coverage is still off, something
shape-changing (noise filtering, splat width) is also in play.

Usage:
    python shift_sweep.py <targets_signal_tree> <render_signal_dir>
                          <palette_json> <out_dir> [--side left]
"""

import argparse
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

TARGET_COLOR = "#2563EB"
RENDER_COLOR = "#EA8600"
N_LEVELS = 256
DB_PER_LEVEL = 36.0 / 255.0


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


def disk_level_hist(img: np.ndarray, rim_frac: float = 0.97) -> tuple[np.ndarray, int]:
    h, w = img.shape
    yy, xx = np.mgrid[:h, :w]
    cy, cx = (h - 1) / 2, (w - 1) / 2
    r = min(h, w) / 2 * rim_frac
    m = (yy - cy) ** 2 + (xx - cx) ** 2 <= r * r
    return np.bincount(img[m].ravel(), minlength=N_LEVELS).astype(float), int(m.sum())


def emd(hist_a: np.ndarray, hist_b: np.ndarray) -> float:
    a = hist_a / hist_a.sum()
    b = hist_b / hist_b.sum()
    return float(np.abs(np.cumsum(a) - np.cumsum(b)).sum() * DB_PER_LEVEL)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("targets_signal_tree", type=Path)
    parser.add_argument("render_signal_dir", type=Path)
    parser.add_argument("palette_json", type=Path)
    parser.add_argument("out_dir", type=Path)
    parser.add_argument("--side", choices=["left", "right"], default="left")
    parser.add_argument("--tolerance", type=float, default=0.6)
    parser.add_argument("--db-range", type=float, default=12.0, help="sweep +/- this many dB")
    args = parser.parse_args()

    palette = json.loads(args.palette_json.read_text())
    lvl_min = int(np.ceil(palette["gate_u_min"] * 255))
    lvl_max = int(np.floor(palette["gate_u_max"] * 255))

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

    pooled_t = np.zeros(N_LEVELS)
    pooled_r = np.zeros(N_LEVELS)
    disk_t = disk_r = 0
    n_pairs = 0
    for ts, tpath in targets:
        i = int(np.abs(render_ts - ts.timestamp()).argmin())
        if abs(render_ts[i] - ts.timestamp()) > args.tolerance:
            continue
        t_img = cv2.imread(str(tpath), cv2.IMREAD_GRAYSCALE)
        r_img = cv2.imread(str(renders[i][1]), cv2.IMREAD_GRAYSCALE)
        if t_img is None or r_img is None:
            continue
        ht, nt = disk_level_hist(t_img)
        hr, nr = disk_level_hist(r_img)
        pooled_t += ht
        pooled_r += hr
        disk_t += nt
        disk_r += nr
        n_pairs += 1
    if n_pairs == 0:
        sys.exit("no pairs")

    target_win = pooled_t[lvl_min : lvl_max + 1]
    cov_t = target_win.sum() / disk_t

    max_shift = int(round(args.db_range / DB_PER_LEVEL))
    shifts, emds, cov_ratios = [], [], []
    for s in range(-max_shift, max_shift + 1):
        shifted = np.roll(pooled_r, s)
        if s > 0:
            shifted[:s] = 0
        elif s < 0:
            shifted[s:] = 0
        win = shifted[lvl_min : lvl_max + 1]
        if win.sum() == 0:
            continue
        shifts.append(s * DB_PER_LEVEL)
        emds.append(emd(target_win, win))
        cov_ratios.append((win.sum() / disk_r) / cov_t)

    shifts = np.array(shifts)
    emds = np.array(emds)
    cov_ratios = np.array(cov_ratios)
    best_emd = shifts[emds.argmin()]
    best_cov = shifts[np.abs(np.log(cov_ratios)).argmin()]

    args.out_dir.mkdir(parents=True, exist_ok=True)
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(7, 6.5), sharex=True)
    ax1.plot(shifts, emds, color=RENDER_COLOR, linewidth=2)
    ax1.axvline(best_emd, color="#9CA3AF", linewidth=1, linestyle="--")
    ax1.set_ylabel("EMD in gate window (dB)")
    ax1.set_title(
        f"Constant-offset sweep, {args.side} view (n={n_pairs}) — "
        f"EMD min at {best_emd:+.1f} dB, coverage match at {best_cov:+.1f} dB"
    )
    ax2.plot(shifts, cov_ratios, color=TARGET_COLOR, linewidth=2)
    ax2.axhline(1.0, color="#9CA3AF", linewidth=1, linestyle="--")
    ax2.set_yscale("log")
    ax2.set_ylabel("coverage ratio (render / target)")
    ax2.set_xlabel("dB offset applied to render")
    for ax in (ax1, ax2):
        ax.grid(alpha=0.25, linewidth=0.5)
        ax.spines[["top", "right"]].set_visible(False)
    fig.tight_layout()
    fig.savefig(args.out_dir / "shift_sweep.png", dpi=150)

    print(
        json.dumps(
            {
                "n_pairs": n_pairs,
                "best_emd_shift_db": round(float(best_emd), 2),
                "emd_at_best_db": round(float(emds.min()), 3),
                "emd_at_zero_db": round(float(emds[np.abs(shifts).argmin()]), 3),
                "coverage_match_shift_db": round(float(best_cov), 2),
                "coverage_ratio_at_zero": round(float(cov_ratios[np.abs(shifts).argmin()]), 2),
                "coverage_ratio_at_emd_best": round(float(cov_ratios[emds.argmin()]), 2),
            },
            indent=1,
        )
    )


if __name__ == "__main__":
    main()
