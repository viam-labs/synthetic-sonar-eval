"""Component-matching comparison: target echo arcs vs render echo arcs.

compare_1d.py's aggregate histogram cannot tell the target's legitimate
weak-band mass (rims of strong arcs) from standalone weak arcs, and its
coverage ratio can be gamed by fragmentation. This tool matches echo
structure spatially instead:

- pixel level: target/render echo masks compared with a small dilation
  tolerance -> mass-weighted recall (display mass the render reproduces)
  and precision (render mass the display actually draws), split by
  intensity band;
- component level: connected components on both masks, overlap graph ->
  clusters; per matched target component the intensity delta against the
  render pixels in its footprint (the weak-band question: "the render
  draws weak echoes one band up" reads as a positive delta in the weak
  bands), plus a fragmentation index (render comps per target comp) that
  catches speckle-gaming in sweeps;
- unmatched-target diagnosis: where the display drew an echo and the
  render has no in-window pixel within tolerance, was the render silent
  or merely sub-window (energy just below the gate)? Sub-window means
  the arc exists but is under-driven.

Delta-dB is reported under two binnings: by the target band (the
display-referenced reading) and by the band of the target/render mean
(symmetric — immune to the regression-to-the-mean artifact that
conditioning on one noisy side introduces; the plot uses this one). A
p90-based delta is included because the footprint mean absorbs the
render's stroke-taper flanks, biasing the mean delta negative on strong
arcs.

Bands are derived from the measured palette's hue transitions (e.g.
green / yellow / orange / red for the SIMRAD blue skin; the display's
faint blue sits below the gate window and never reaches the targets).
Bands where the palette curve is flat (indistinguishable colors) are
flagged: target intensity there is a quantization artifact of
invert_targets.py, so delta-dB is unreliable.

Both inputs are 8-bit grayscale in the same unit: v/255 = u = (dB + 100)/36.
The render is resized to the target grid before masking; the sub-pixel
edge dilution this causes is well below one band width.

Outputs to <out_dir>:
    summary.json         pooled metrics, per band
    per_frame.csv        per-frame mass/match/component stats
    per_component.csv    every component >= min size, both sides
    delta_db.png         matched-component delta-dB by band + scatter
    unmatched_mass.png   unmatched mass fraction by band, both sides
    per_frame.png        matched-mass fractions + component counts over time
    eyeball/*.png        overlays: target-only blue, render-only orange,
                         matched white

Usage:
    python compare_components.py <targets_signal_tree> <render_signal_dir>
                                 <palette_json> <out_dir> [--side left]
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

TARGET_COLOR = "#2563EB"  # blue
RENDER_COLOR = "#EA8600"  # orange
NEUTRAL_COLOR = "#6B7280"
INK_COLOR = "#111827"

BGR_TARGET = (235, 99, 37)  # TARGET_COLOR in BGR
BGR_RENDER = (0, 134, 234)  # RENDER_COLOR in BGR
BGR_MATCHED = (225, 225, 225)

N_EYEBALL = 6
MATCHED_COMP_MIN_FRAC = 0.5
SILENT_U = 0.02  # render u below this = drew nothing there
MIN_HUE_RUN = 3  # palette indices; shorter hue runs are transition noise
FLAT_RUN_LEN = 15  # indices of near-identical color that make a band flat
FLAT_STEP_RGB = 1.0


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


def hue_class(h: int) -> str:
    """Coarse display-palette hue name (OpenCV H in [0, 180))."""
    if h < 12 or h >= 140:
        return "red"
    if h < 25:
        return "orange"
    if h < 40:
        return "yellow"
    if h < 100:
        return "green"  # includes teal
    return "blue"


def palette_bands(palette: dict) -> tuple[list[dict], np.ndarray]:
    """Derive intensity bands from the palette's hue transitions.

    Returns (bands, u_edges). Each band: name, u/db range, and an
    `invertible` flag (False when the band contains a long flat palette
    stretch, where invert_targets.py piles intensities onto one value).
    Falls back to 4 equal-width bands if the hue structure is degenerate.
    """
    rgb = np.array(palette["colors_rgb"], np.float64)
    n = len(rgb)
    hsv = cv2.cvtColor(
        rgb[:, ::-1].astype(np.uint8).reshape(1, n, 3), cv2.COLOR_BGR2HSV
    )[0]
    lo = int(np.ceil(palette["gate_u_min"] * (n - 1)))
    hi = int(np.floor(palette["gate_u_max"] * (n - 1)))

    runs: list[list] = []  # [name, start_idx, length]
    for i in range(lo, hi + 1):
        c = hue_class(int(hsv[i, 0]))
        if runs and runs[-1][0] == c:
            runs[-1][2] += 1
        else:
            runs.append([c, i, 1])
    long_runs = [r for r in runs if r[2] >= MIN_HUE_RUN]
    merged: list[list] = []
    for r in long_runs:
        if merged and merged[-1][0] == r[0]:
            merged[-1][2] = r[1] + r[2] - merged[-1][1]
        else:
            merged.append(list(r))

    if len(merged) >= 2:
        edges_idx = [lo]
        for a, b in zip(merged, merged[1:]):
            edges_idx.append((a[1] + a[2] + b[1]) // 2)
        edges_idx.append(hi + 1)
        names = [r[0] for r in merged]
    else:  # degenerate palette: equal-width fallback
        edges_idx = list(np.linspace(lo, hi + 1, 5).astype(int))
        names = [f"q{k + 1}" for k in range(4)]

    step = np.linalg.norm(np.diff(rgb, axis=0), axis=1)
    bands = []
    for k, name in enumerate(names):
        i0, i1 = edges_idx[k], edges_idx[k + 1]
        flat = step[i0 : max(i0, i1 - 1)] < FLAT_STEP_RGB
        run, longest = 0, 0
        for f in flat:
            run = run + 1 if f else 0
            longest = max(longest, run)
        bands.append(
            {
                "name": name,
                "db_lo": round(-100 + 36 * i0 / (n - 1), 2),
                "db_hi": round(-100 + 36 * i1 / (n - 1), 2),
                "invertible": longest < FLAT_RUN_LEN,
            }
        )
    return bands, np.array(edges_idx) / (n - 1)


class DSU:
    def __init__(self) -> None:
        self.p: dict = {}

    def find(self, a):
        self.p.setdefault(a, a)
        while self.p[a] != a:
            self.p[a] = self.p[self.p[a]]
            a = self.p[a]
        return a

    def union(self, a, b) -> None:
        self.p[self.find(a)] = self.find(b)


def echo_masks(
    t_img: np.ndarray,
    r_img: np.ndarray,
    disk: np.ndarray,
    u_min: float,
    u_max: float,
    kernel: np.ndarray,
) -> dict:
    """Grayscale pair -> u images, echo masks, dilations, matched masks."""
    t_u = t_img.astype(np.float64) / 255.0
    r_u = r_img.astype(np.float64) / 255.0
    t_mask = (t_u >= u_min) & (t_u <= u_max) & disk
    r_mask = (r_u >= u_min) & (r_u <= u_max) & disk
    dil_t = cv2.dilate(t_mask.astype(np.uint8), kernel).astype(bool)
    dil_r = cv2.dilate(r_mask.astype(np.uint8), kernel).astype(bool)
    return {
        "t_u": t_u,
        "r_u": r_u,
        "t_mask": t_mask,
        "r_mask": r_mask,
        "dil_t": dil_t,
        "dil_r": dil_r,
        "matched_t": t_mask & dil_r,
        "matched_r": r_mask & dil_t,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("targets_signal_tree", type=Path)
    parser.add_argument("render_signal_dir", type=Path)
    parser.add_argument("palette_json", type=Path)
    parser.add_argument("out_dir", type=Path)
    parser.add_argument("--side", choices=["left", "right"], default="left")
    parser.add_argument("--rim-frac", type=float, default=0.97)
    parser.add_argument("--tolerance", type=float, default=0.6, help="max pairing gap (s)")
    parser.add_argument(
        "--tol-px",
        type=int,
        default=6,
        help="match tolerance in px at target scale (~one display stroke width)",
    )
    parser.add_argument(
        "--min-comp-px",
        type=int,
        default=12,
        help="min component area for per-component stats (mass counts keep everything)",
    )
    args = parser.parse_args()

    palette = json.loads(args.palette_json.read_text())
    u_min, u_max = palette["gate_u_min"], palette["gate_u_max"]
    bands, u_edges = palette_bands(palette)
    nb = len(bands)
    print(
        "bands:",
        ", ".join(
            f"{b['name']} [{b['db_lo']}, {b['db_hi']}] dB"
            + ("" if b["invertible"] else " (flat palette: target dB unreliable)")
            for b in bands
        ),
    )

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

    kernel = cv2.getStructuringElement(
        cv2.MORPH_ELLIPSE, (2 * args.tol_px + 1, 2 * args.tol_px + 1)
    )
    pooled = {
        s: {"px": np.zeros(nb, np.int64), "matched": np.zeros(nb, np.int64)}
        for s in ("target", "render")
    }
    # unmatched target px, by band: what the render holds nearby
    unm_t_state = {
        s: np.zeros(nb, np.int64) for s in ("silent", "sub_window", "supra_window")
    }
    rows: list[dict] = []
    comp_rows: list[dict] = []
    frag_ratios: list[float] = []
    render_dust_px = 0
    render_total_px = 0
    pairs: list[tuple[Path, Path]] = []
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
        if r_img.shape != t_img.shape:
            r_img = cv2.resize(r_img, t_img.shape[::-1], interpolation=cv2.INTER_AREA)
        if t_img.shape not in masks:
            masks[t_img.shape] = disk_mask(t_img.shape, args.rim_frac)
        mm = echo_masks(t_img, r_img, masks[t_img.shape], u_min, u_max, kernel)

        for side, msk, matched in (
            ("target", mm["t_mask"], mm["matched_t"]),
            ("render", mm["r_mask"], mm["matched_r"]),
        ):
            u_img = mm["t_u"] if side == "target" else mm["r_u"]
            pooled[side]["px"] += np.bincount(
                np.digitize(u_img[msk], u_edges[1:-1]), minlength=nb
            )
            pooled[side]["matched"] += np.bincount(
                np.digitize(u_img[matched], u_edges[1:-1]), minlength=nb
            )

        unm_t = mm["t_mask"] & ~mm["dil_r"]
        near_r_u = cv2.dilate(mm["r_u"].astype(np.float32), kernel)  # max filter
        near = near_r_u[unm_t]
        unm_band = np.digitize(mm["t_u"][unm_t], u_edges[1:-1])
        for state, sel in (
            ("silent", near < SILENT_U),
            ("sub_window", (near >= SILENT_U) & (near < u_min)),
            ("supra_window", near > u_max),
        ):
            unm_t_state[state] += np.bincount(unm_band[sel], minlength=nb)

        t8 = mm["t_mask"].astype(np.uint8)
        r8 = mm["r_mask"].astype(np.uint8)
        n_t, lab_t, st_t, _ = cv2.connectedComponentsWithStats(t8, connectivity=8)
        n_r, lab_r, st_r, _ = cv2.connectedComponentsWithStats(r8, connectivity=8)
        render_total_px += int(mm["r_mask"].sum())
        render_dust_px += int(
            sum(
                st_r[j, cv2.CC_STAT_AREA]
                for j in range(1, n_r)
                if st_r[j, cv2.CC_STAT_AREA] < args.min_comp_px
            )
        )

        H, W = t_img.shape
        pad = args.tol_px + 1
        dsu = DSU()
        frame_deltas: list[float] = []
        n_comp_t = n_comp_t_matched = 0
        date = ts.isoformat()
        for ci in range(1, n_t):
            area = int(st_t[ci, cv2.CC_STAT_AREA])
            if area < args.min_comp_px:
                continue
            n_comp_t += 1
            x, y = st_t[ci, cv2.CC_STAT_LEFT], st_t[ci, cv2.CC_STAT_TOP]
            w, h = st_t[ci, cv2.CC_STAT_WIDTH], st_t[ci, cv2.CC_STAT_HEIGHT]
            y0, y1 = max(0, y - pad), min(H, y + h + pad)
            x0, x1 = max(0, x - pad), min(W, x + w + pad)
            comp = lab_t[y0:y1, x0:x1] == ci
            dil_comp = cv2.dilate(comp.astype(np.uint8), kernel).astype(bool)
            foot = dil_comp & mm["r_mask"][y0:y1, x0:x1]
            comp_u = mm["t_u"][y0:y1, x0:x1][comp]
            mean_u_t = float(comp_u.mean())
            matched_frac = float(mm["matched_t"][y0:y1, x0:x1][comp].mean())
            partners = np.unique(lab_r[y0:y1, x0:x1][foot])
            partners = partners[partners > 0]
            for rj in partners:
                dsu.union(("t", ci), ("r", int(rj)))
            if foot.any():
                foot_u = mm["r_u"][y0:y1, x0:x1][foot]
                mean_u_r = float(foot_u.mean())
                delta_db = 36.0 * (mean_u_r - mean_u_t)
                delta_db_p90 = 36.0 * float(
                    np.percentile(foot_u, 90) - np.percentile(comp_u, 90)
                )
                band_sym = bands[
                    int(np.digitize((mean_u_t + mean_u_r) / 2, u_edges[1:-1]))
                ]["name"]
            else:
                delta_db = delta_db_p90 = np.nan
                band_sym = ""
            if matched_frac >= MATCHED_COMP_MIN_FRAC:
                n_comp_t_matched += 1
                if np.isfinite(delta_db):
                    frame_deltas.append(delta_db)
            comp_rows.append(
                {
                    "ts": date,
                    "side": "target",
                    "comp": ci,
                    "area_px": area,
                    "mean_db": round(-100 + 36 * mean_u_t, 2),
                    "band": bands[int(np.digitize(mean_u_t, u_edges[1:-1]))]["name"],
                    "band_sym": band_sym,
                    "matched_frac": round(matched_frac, 3),
                    "delta_db": round(delta_db, 3) if np.isfinite(delta_db) else "",
                    "delta_db_p90": round(delta_db_p90, 3)
                    if np.isfinite(delta_db_p90)
                    else "",
                    "n_partner_comps": len(partners),
                }
            )

        n_comp_r = n_comp_r_matched = 0
        for cj in range(1, n_r):
            area = int(st_r[cj, cv2.CC_STAT_AREA])
            if area < args.min_comp_px:
                continue
            n_comp_r += 1
            comp = lab_r == cj
            mean_u_r = float(mm["r_u"][comp].mean())
            matched_frac = float(mm["matched_r"][comp].mean())
            if matched_frac >= MATCHED_COMP_MIN_FRAC:
                n_comp_r_matched += 1
            comp_rows.append(
                {
                    "ts": date,
                    "side": "render",
                    "comp": cj,
                    "area_px": area,
                    "mean_db": round(-100 + 36 * mean_u_r, 2),
                    "band": bands[int(np.digitize(mean_u_r, u_edges[1:-1]))]["name"],
                    "band_sym": "",
                    "matched_frac": round(matched_frac, 3),
                    "delta_db": "",
                    "delta_db_p90": "",
                    "n_partner_comps": "",
                }
            )

        clusters: dict = {}
        for node in list(dsu.p):
            clusters.setdefault(dsu.find(node), []).append(node)
        for members in clusters.values():
            ct = sum(1 for kind, _ in members if kind == "t")
            cr = sum(1 for kind, _ in members if kind == "r")
            if ct and cr:
                frag_ratios.append(cr / ct)

        n_px_t, n_px_r = int(mm["t_mask"].sum()), int(mm["r_mask"].sum())
        rows.append(
            {
                "ts": date,
                "gap_s": round(gap, 3),
                "n_px_target": n_px_t,
                "n_px_render": n_px_r,
                "mass_matched_target": round(int(mm["matched_t"].sum()) / n_px_t, 4)
                if n_px_t
                else np.nan,
                "mass_matched_render": round(int(mm["matched_r"].sum()) / n_px_r, 4)
                if n_px_r
                else np.nan,
                "n_comp_target": n_comp_t,
                "n_comp_render": n_comp_r,
                "n_comp_target_matched": n_comp_t_matched,
                "n_comp_render_matched": n_comp_r_matched,
                "median_delta_db": round(float(np.median(frame_deltas)), 3)
                if frame_deltas
                else np.nan,
            }
        )
        pairs.append((tpath, renders[i][1]))

    if not rows:
        sys.exit("no pairs within tolerance")
    args.out_dir.mkdir(parents=True, exist_ok=True)

    with open(args.out_dir / "per_frame.csv", "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)
    with open(args.out_dir / "per_component.csv", "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(comp_rows[0].keys()))
        w.writeheader()
        w.writerows(comp_rows)

    idx_of = {b["name"]: k for k, b in enumerate(bands)}
    band_deltas: list[list[float]] = [[] for _ in bands]  # symmetric binning
    band_deltas_tgt: list[list[float]] = [[] for _ in bands]  # target binning
    band_deltas_p90: list[list[float]] = [[] for _ in bands]  # symmetric binning
    for c in comp_rows:
        if (
            c["side"] != "target"
            or c["matched_frac"] < MATCHED_COMP_MIN_FRAC
            or c["delta_db"] == ""
        ):
            continue
        band_deltas_tgt[idx_of[c["band"]]].append(c["delta_db"])
        if c["band_sym"]:
            band_deltas[idx_of[c["band_sym"]]].append(c["delta_db"])
            band_deltas_p90[idx_of[c["band_sym"]]].append(c["delta_db_p90"])

    tp, tm = pooled["target"]["px"], pooled["target"]["matched"]
    rp, rm = pooled["render"]["px"], pooled["render"]["matched"]
    per_band = []
    for k, b in enumerate(bands):
        d = np.array(band_deltas[k])
        d_tgt = np.array(band_deltas_tgt[k])
        d_p90 = np.array(band_deltas_p90[k])
        unm_total = int(tp[k] - tm[k])
        per_band.append(
            {
                "band": b["name"],
                "db_range": [b["db_lo"], b["db_hi"]],
                "invertible": b["invertible"],
                "target_px": int(tp[k]),
                "render_px": int(rp[k]),
                "target_matched_frac": round(float(tm[k] / tp[k]), 4) if tp[k] else None,
                "render_matched_frac": round(float(rm[k] / rp[k]), 4) if rp[k] else None,
                "target_unmatched_render_state": {
                    s: round(float(unm_t_state[s][k] / unm_total), 3)
                    for s in unm_t_state
                }
                if unm_total
                else None,
                "n_matched_target_comps": len(d),
                "delta_db_median": round(float(np.median(d)), 3) if len(d) else None,
                "delta_db_iqr": [
                    round(float(np.percentile(d, 25)), 3),
                    round(float(np.percentile(d, 75)), 3),
                ]
                if len(d)
                else None,
                "delta_db_p90_median": round(float(np.median(d_p90)), 3)
                if len(d_p90)
                else None,
                "delta_db_median_target_binned": round(float(np.median(d_tgt)), 3)
                if len(d_tgt)
                else None,
            }
        )
    summary = {
        "n_pairs": len(rows),
        "tol_px": args.tol_px,
        "min_comp_px": args.min_comp_px,
        "u_window": [u_min, u_max],
        "mass_matched_frac_target": round(float(tm.sum() / tp.sum()), 4),
        "mass_matched_frac_render": round(float(rm.sum() / rp.sum()), 4),
        "fragmentation_median": round(float(np.median(frag_ratios)), 3)
        if frag_ratios
        else None,
        "n_clusters": len(frag_ratios),
        "render_dust_mass_frac": round(render_dust_px / render_total_px, 4)
        if render_total_px
        else None,
        "median_n_comp_target": float(np.median([r["n_comp_target"] for r in rows])),
        "median_n_comp_render": float(np.median([r["n_comp_render"] for r in rows])),
        "bands": per_band,
    }
    (args.out_dir / "summary.json").write_text(json.dumps(summary, indent=1))

    # Delta-dB: banded distribution + scatter vs target intensity.
    band_labels = [
        b["name"] + ("" if b["invertible"] else "*") for b in bands
    ]
    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(11, 4.5), sharey=True)
    rng = np.random.default_rng(0)
    for k, d in enumerate(band_deltas):
        if not d:
            continue
        ax1.scatter(
            k + rng.uniform(-0.18, 0.18, len(d)),
            d,
            s=12,
            color=NEUTRAL_COLOR,
            alpha=0.45,
            linewidths=0,
        )
        q25, q50, q75 = np.percentile(d, [25, 50, 75])
        ax1.plot([k, k], [q25, q75], color=INK_COLOR, linewidth=2)
        ax1.plot([k - 0.25, k + 0.25], [q50, q50], color=INK_COLOR, linewidth=2.5)
    ax1.axhline(0, color=NEUTRAL_COLOR, linewidth=1, linestyle="--", alpha=0.6)
    ax1.set_xticks(range(nb), band_labels)
    ax1.set_xlabel("band of (target+render)/2 mean — symmetric binning")
    ax1.set_ylabel("Δ mean dB (render − target)")
    ax1.set_title("Matched components: intensity delta by band")
    tgt = [
        c
        for c in comp_rows
        if c["side"] == "target"
        and c["matched_frac"] >= MATCHED_COMP_MIN_FRAC
        and c["delta_db"] != ""
    ]
    ax2.scatter(
        [c["mean_db"] for c in tgt],
        [c["delta_db"] for c in tgt],
        s=12,
        color=NEUTRAL_COLOR,
        alpha=0.45,
        linewidths=0,
    )
    ax2.axhline(0, color=NEUTRAL_COLOR, linewidth=1, linestyle="--", alpha=0.6)
    ax2.set_xlabel("target component mean dB")
    ax2.set_title("vs target intensity")
    for ax in (ax1, ax2):
        ax.grid(alpha=0.25, linewidth=0.5)
        ax.spines[["top", "right"]].set_visible(False)
    if any(not b["invertible"] for b in bands):
        fig.text(
            0.01,
            0.01,
            "* flat palette stretch: target dB partly unrecoverable (inversion artifact)",
            fontsize=8,
            color=NEUTRAL_COLOR,
        )
    fig.tight_layout(rect=(0, 0.03, 1, 1))
    fig.savefig(args.out_dir / "delta_db.png", dpi=150)
    plt.close(fig)

    # Unmatched mass by band, both sides.
    fig, ax = plt.subplots(figsize=(8, 4.5))
    xs = np.arange(nb)
    un_t = [1 - tm[k] / tp[k] if tp[k] else 0 for k in range(nb)]
    un_r = [1 - rm[k] / rp[k] if rp[k] else 0 for k in range(nb)]
    for off, vals, color, label in (
        (-0.19, un_t, TARGET_COLOR, "target mass unmatched (render misses it)"),
        (0.19, un_r, RENDER_COLOR, "render mass unmatched (display doesn't draw it)"),
    ):
        ax.bar(xs + off, vals, width=0.36, color=color, label=label)
        for x, v in zip(xs + off, vals):
            ax.text(x, v + 0.01, f"{v:.2f}", ha="center", fontsize=8, color=INK_COLOR)
    ax.set_xticks(xs, band_labels)
    ax.set_ylabel("unmatched fraction of band mass")
    ax.set_title(f"Unmatched echo mass by band, {args.side} view (tol {args.tol_px} px)")
    ax.legend(frameon=False)
    ax.grid(alpha=0.25, linewidth=0.5, axis="y")
    ax.spines[["top", "right"]].set_visible(False)
    fig.tight_layout()
    fig.savefig(args.out_dir / "unmatched_mass.png", dpi=150)
    plt.close(fig)

    # Per-frame time series.
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(8, 6), sharex=True)
    x = np.arange(len(rows))
    ax1.plot(x, [r["mass_matched_target"] for r in rows], color=TARGET_COLOR, linewidth=2, label="target")
    ax1.plot(x, [r["mass_matched_render"] for r in rows], color=RENDER_COLOR, linewidth=2, label="render")
    ax1.set_ylabel("matched mass fraction")
    ax1.set_title(f"Per-frame matching, {args.side} view")
    ax1.legend(frameon=False)
    ax2.plot(x, [r["n_comp_target"] for r in rows], color=TARGET_COLOR, linewidth=2, label="target")
    ax2.plot(x, [r["n_comp_render"] for r in rows], color=RENDER_COLOR, linewidth=2, label="render")
    ax2.set_ylabel(f"components ≥ {args.min_comp_px} px")
    ax2.set_xlabel("frame")
    ax2.legend(frameon=False)
    for ax in (ax1, ax2):
        ax.grid(alpha=0.25, linewidth=0.5)
        ax.spines[["top", "right"]].set_visible(False)
    fig.tight_layout()
    fig.savefig(args.out_dir / "per_frame.png", dpi=150)
    plt.close(fig)

    # Eyeball overlays: evenly spaced frames + worst misses + worst phantoms.
    eyeball = args.out_dir / "eyeball"
    eyeball.mkdir(exist_ok=True)
    miss_px = [
        r["n_px_target"] * (1 - r["mass_matched_target"])
        if np.isfinite(r["mass_matched_target"])
        else 0
        for r in rows
    ]
    phantom_px = [
        r["n_px_render"] * (1 - r["mass_matched_render"])
        if np.isfinite(r["mass_matched_render"])
        else 0
        for r in rows
    ]
    picks: dict[int, str] = {}
    for k in np.linspace(0, len(pairs) - 1, min(N_EYEBALL, len(pairs))).astype(int):
        picks[int(k)] = "even"
    for k in np.argsort(miss_px)[-2:]:
        picks.setdefault(int(k), "miss")
    for k in np.argsort(phantom_px)[-2:]:
        picks.setdefault(int(k), "phantom")
    for k, kind in sorted(picks.items()):
        tpath, rpath = pairs[k]
        t_img = cv2.imread(str(tpath), cv2.IMREAD_GRAYSCALE)
        r_img = cv2.imread(str(rpath), cv2.IMREAD_GRAYSCALE)
        if r_img.shape != t_img.shape:
            r_img = cv2.resize(r_img, t_img.shape[::-1], interpolation=cv2.INTER_AREA)
        mm = echo_masks(t_img, r_img, masks[t_img.shape], u_min, u_max, kernel)
        canvas = np.zeros((*t_img.shape, 3), np.uint8)
        canvas[mm["matched_t"] | mm["matched_r"]] = BGR_MATCHED
        canvas[mm["t_mask"] & ~mm["dil_r"]] = BGR_TARGET
        canvas[mm["r_mask"] & ~mm["dil_t"]] = BGR_RENDER
        header = np.zeros((30, canvas.shape[1], 3), np.uint8)
        cv2.putText(
            header,
            "blue: target only   orange: render only   white: matched",
            (10, 21),
            cv2.FONT_HERSHEY_SIMPLEX,
            0.55,
            (200, 200, 200),
            1,
            cv2.LINE_AA,
        )
        cv2.imwrite(
            str(eyeball / f"{kind}_{k:03d}_{tpath.parent.name.split('+')[0]}.png"),
            np.vstack([header, canvas]),
        )

    print(json.dumps(summary, indent=1))


if __name__ == "__main__":
    main()
