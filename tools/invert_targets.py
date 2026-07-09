"""Invert target blob images from display colors to signal space.

Maps every non-black pixel of the blob-only target views (strip_overlays
output) through the inverse of the display palette (extract_palette.py
output): nearest point on the 1-D palette curve in RGB space -> u in [0, 1],
where u spans [-100, -64] dB. Output is 8-bit grayscale (round(u * 255)),
unit-compatible with the renderer's sonar-signal/ images.

Pixels farther than --max-dist from any palette color (off-curve junk) are
set to 0.

Usage:
    python invert_targets.py <blobs_tree> <palette_json> <out_tree>
"""

import argparse
import json
import sys
from pathlib import Path

import cv2
import numpy as np

DEFAULT_MAX_DIST = 80.0
CHUNK = 65536


def invert_image(
    img_bgr: np.ndarray, curve_rgb: np.ndarray, max_dist: float
) -> np.ndarray:
    h, w = img_bgr.shape[:2]
    out = np.zeros((h, w), np.uint8)
    mask = img_bgr.any(axis=2)
    pix = img_bgr[mask][:, ::-1].astype(np.float32)  # -> RGB
    if len(pix) == 0:
        return out
    n_curve = len(curve_rgb)
    u_vals = np.empty(len(pix), np.float32)
    for i in range(0, len(pix), CHUNK):
        chunk = pix[i : i + CHUNK]
        d2 = ((chunk[:, None, :] - curve_rgb[None]) ** 2).sum(axis=2)
        best = d2.argmin(axis=1)
        u = best.astype(np.float32) / (n_curve - 1)
        u[np.sqrt(d2[np.arange(len(chunk)), best]) > max_dist] = 0.0
        u_vals[i : i + CHUNK] = u
    out[mask] = np.round(u_vals * 255).astype(np.uint8)
    return out


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("blobs_tree", type=Path)
    parser.add_argument("palette_json", type=Path)
    parser.add_argument("out_tree", type=Path)
    parser.add_argument("--max-dist", type=float, default=DEFAULT_MAX_DIST)
    args = parser.parse_args()

    palette = json.loads(args.palette_json.read_text())
    curve_rgb = np.array(palette["colors_rgb"], np.float32)

    pngs = sorted(args.blobs_tree.rglob("*.png"))
    pngs = [p for p in pngs if "_debug" not in p.parts]
    if not pngs:
        sys.exit(f"no PNGs under {args.blobs_tree}")

    n_done = 0
    for p in pngs:
        img = cv2.imread(str(p))
        if img is None:
            continue
        gray = invert_image(img, curve_rgb, args.max_dist)
        dst = args.out_tree / p.relative_to(args.blobs_tree)
        dst.parent.mkdir(parents=True, exist_ok=True)
        cv2.imwrite(str(dst), gray)
        n_done += 1
    print(f"done: {n_done} inverted -> {args.out_tree}")


if __name__ == "__main__":
    main()
