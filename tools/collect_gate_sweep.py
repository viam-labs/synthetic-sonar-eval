"""Collect gate_sweep.sh results into a ranked table.

Objective: maximize matched echo mass (mean of the target- and render-side
matched fractions from compare_components.py). Constraint columns shown
alongside: fragmentation (speckle-gaming guard, want ~1), render dust,
red-band p90 delta (strong-echo temperature, want ~0), green-band matched
fractions (the weak-arc gap), plus compare_1d's coverage ratio and EMD for
continuity with the earlier sweeps.

Writes <sweep_dir>/sweep_results.csv and prints the table.

Usage:
    python collect_gate_sweep.py <sweep_dir>
"""

import csv
import json
import sys
from pathlib import Path


def main() -> None:
    sweep = Path(sys.argv[1])
    rows = []
    for run in sorted((sweep / "runs").iterdir()):
        summary = run / "metric" / "summary.json"
        if not summary.exists():
            print(f"skipping (no summary): {run.name}", file=sys.stderr)
            continue
        s = json.loads(summary.read_text())
        bands = {b["band"]: b for b in s["bands"]}
        green, red = bands.get("green", {}), bands.get("red", {})
        row = {
            "name": run.name,
            "match_t": s["mass_matched_frac_target"],
            "match_r": s["mass_matched_frac_render"],
            "green_t": green.get("target_matched_frac"),
            "green_r": green.get("render_matched_frac"),
            "red_p90_db": red.get("delta_db_p90_median"),
            "frag": s["fragmentation_median"],
            "dust": s["render_dust_mass_frac"],
        }
        summary1d = run / "metric1d" / "summary.json"
        if summary1d.exists():
            s1 = json.loads(summary1d.read_text())
            row["cov_ratio"] = round(
                s1["median_cov_render"] / s1["median_cov_target"], 2
            )
            row["emd_db"] = s1["pooled_emd_db"]
        rows.append(row)
    if not rows:
        sys.exit("no results found")

    rows.sort(key=lambda r: -(r["match_t"] + r["match_r"]) / 2)
    out = sweep / "sweep_results.csv"
    with open(out, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)

    cols = list(rows[0].keys())
    print(f"{'name':34}" + "".join(f"{c:>11}" for c in cols[1:]))
    for r in rows:
        print(
            f"{r['name']:34}"
            + "".join(f"{r.get(c) if r.get(c) is not None else '-':>11}" for c in cols[1:])
        )
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
