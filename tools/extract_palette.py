"""Extract the SIMRAD display palette from the legend bar in screen1 screenshots.

The legend gradient bar in the bottom status bar renders the exact palette the
display applies to dB values in [-100, -64] (per the sonar vendor: samples are
converted with 10*log10(2)/256 dB/count; values above -64 dB use the maximum
color). Sampling the bar per screenshot and taking the pixel-wise median across
the clip yields the palette as a 1-D RGB curve color(u), u in [0, 1] mapping
linearly onto [-100, -64] dB.

Also reports where the curve crosses the strip_overlays HSV echo gate: target
blob images only keep pixels inside the gate, so any target-vs-render
comparison is only valid on u in [u_min, u_max].

Usage:
    python extract_palette.py <screenshots_dir> <out_json> [--debug-dir DIR]
"""

import argparse
import json
import sys
from pathlib import Path

import cv2
import numpy as np

N_SAMPLES = 256

# Legend bar search: bottom status bar of the 1080p screen1 layout.
BOTTOM_BAND_FRAC = 0.97  # search below this fraction of image height
MIN_BAR_WIDTH_FRAC = 0.05
MIN_BAR_ASPECT = 4.0

# Echo gate from kongsberg-training-utils src/sonar/strip_overlays.py --
# target blob images only contain pixels passing this.
GATE_SAT_MIN = 80
GATE_VAL_MIN = 40
GATE_HUE_GREEN_MAX = 90
GATE_HUE_RED_MIN = 165


def find_legend_bar(
    img: np.ndarray,
) -> tuple[tuple[int, int, int, int], np.ndarray] | None:
    """Return the legend bar bbox (x, y, w, h) and its pixel mask, or None.

    Candidates are wide saturated components in the bottom band; the legend
    is the one covering the most distinct hues (a gradient bar spans the
    whole palette, while panel edges and separators are single-hue).
    """
    h_img = img.shape[0]
    y0 = int(h_img * BOTTOM_BAND_FRAC)
    band = img[y0:]
    hsv = cv2.cvtColor(band, cv2.COLOR_BGR2HSV)
    mask = ((hsv[..., 1] >= 60) & (hsv[..., 2] >= 25)).astype(np.uint8)
    n, labels, stats, _ = cv2.connectedComponentsWithStats(mask, connectivity=8)
    best = None
    best_score = 0
    for i in range(1, n):
        x, y, w, h, _area = stats[i]
        if w < img.shape[1] * MIN_BAR_WIDTH_FRAC or h == 0:
            continue
        if w / h < MIN_BAR_ASPECT:
            continue
        comp = labels == i
        hue_bins = np.unique(hsv[..., 0][comp] // 10)
        if len(hue_bins) > best_score:
            best_score = len(hue_bins)
            best = ((x, y0 + y, w, h), comp)
    if best is None or best_score < 4:
        return None
    (x, y, w, h), comp = best
    full_mask = np.zeros(img.shape[:2], bool)
    full_mask[y0:] = comp
    return (x, y, w, h), full_mask


def sample_bar(
    img: np.ndarray, bbox: tuple[int, int, int, int], comp_mask: np.ndarray
) -> np.ndarray:
    """Sample N_SAMPLES median colors along the bar (component pixels only)."""
    x, y, w, h = bbox
    bar = img[y : y + h, x : x + w].astype(np.float32)
    m = comp_mask[y : y + h, x : x + w]
    cols = np.linspace(0, w - 1, N_SAMPLES)
    samples = np.empty((N_SAMPLES, 3), np.float32)
    half = max(1, w // (2 * N_SAMPLES))
    prev = None
    for i, c in enumerate(cols):
        c0 = max(0, int(c) - half)
        c1 = min(w, int(c) + half + 1)
        sel = bar[:, c0:c1][m[:, c0:c1]]
        if len(sel) == 0:
            samples[i] = prev if prev is not None else 0
        else:
            samples[i] = np.median(sel, axis=0)
        prev = samples[i]
    return samples


def gate_mask(colors_bgr: np.ndarray) -> np.ndarray:
    """Apply the strip_overlays echo gate to a (N, 3) BGR color array."""
    hsv = cv2.cvtColor(colors_bgr.astype(np.uint8)[None], cv2.COLOR_BGR2HSV)[0]
    hue, sat, val = hsv[:, 0].astype(int), hsv[:, 1], hsv[:, 2]
    return (
        (sat >= GATE_SAT_MIN)
        & (val >= GATE_VAL_MIN)
        & ((hue <= GATE_HUE_GREEN_MAX) | (hue >= GATE_HUE_RED_MIN))
    )


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("screenshots_dir", type=Path)
    parser.add_argument("out_json", type=Path)
    parser.add_argument("--debug-dir", type=Path, default=None)
    args = parser.parse_args()

    paths = sorted(
        p
        for p in args.screenshots_dir.iterdir()
        if p.suffix.lower() in {".png", ".jpg", ".jpeg"}
    )
    if not paths:
        sys.exit(f"no images in {args.screenshots_dir}")

    all_samples = []
    skipped = 0
    for p in paths:
        img = cv2.imread(str(p))
        if img is None:
            skipped += 1
            continue
        found = find_legend_bar(img)
        if found is None:
            skipped += 1
            continue
        bbox, comp_mask = found
        all_samples.append(sample_bar(img, bbox, comp_mask))
    if not all_samples:
        sys.exit("legend bar not found in any screenshot")

    curve_bgr = np.median(np.stack(all_samples), axis=0)

    # Orientation: weak end is blue (OpenCV hue ~100-130). Flip if needed so
    # index 0 = weak (u=0, -100 dB).
    hsv = cv2.cvtColor(curve_bgr.astype(np.uint8)[None], cv2.COLOR_BGR2HSV)[0]
    head_blue = np.mean((hsv[:25, 0] > 95) & (hsv[:25, 0] < 135))
    tail_blue = np.mean((hsv[-25:, 0] > 95) & (hsv[-25:, 0] < 135))
    if tail_blue > head_blue:
        curve_bgr = curve_bgr[::-1]

    passes = gate_mask(curve_bgr)
    idx = np.where(passes)[0]
    u = np.linspace(0.0, 1.0, N_SAMPLES)
    u_min = float(u[idx.min()]) if len(idx) else None
    u_max = float(u[idx.max()]) if len(idx) else None

    out = {
        "n": N_SAMPLES,
        "db_min": -100.0,
        "db_max": -64.0,
        "gate_u_min": u_min,
        "gate_u_max": u_max,
        "n_screenshots": len(all_samples),
        "colors_rgb": curve_bgr[:, ::-1].round(1).tolist(),
    }
    args.out_json.parent.mkdir(parents=True, exist_ok=True)
    args.out_json.write_text(json.dumps(out, indent=1))

    if args.debug_dir is not None:
        args.debug_dir.mkdir(parents=True, exist_ok=True)
        ramp = np.repeat(curve_bgr.astype(np.uint8)[None], 40, axis=0)
        ramp = cv2.resize(ramp, (1024, 40), interpolation=cv2.INTER_NEAREST)
        gate_row = np.where(
            np.repeat(passes[None, :, None], 3, axis=2), 255, 0
        ).astype(np.uint8)
        gate_row = cv2.resize(
            np.repeat(gate_row, 10, axis=0), (1024, 10), cv2.INTER_NEAREST
        )
        cv2.imwrite(str(args.debug_dir / "palette_ramp.png"), np.vstack([ramp, gate_row]))

    db_lo = -100 + 36 * (u_min if u_min is not None else 0)
    db_hi = -100 + 36 * (u_max if u_max is not None else 1)
    print(
        f"done: {len(all_samples)} bars sampled, {skipped} skipped -> {args.out_json}\n"
        f"gate-valid u range: [{u_min:.3f}, {u_max:.3f}] "
        f"= [{db_lo:.1f}, {db_hi:.1f}] dB"
    )


if __name__ == "__main__":
    main()
