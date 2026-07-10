"""Collect splat_sweep.sh results into a ranked table.

Ranks configs by |log(coverage ratio)| (how far the render's echo area is
from the target's, either direction), with in-window EMD as tie-breaker.
Writes <sweep_dir>/sweep_results.csv and prints the table.

Usage:
    python collect_sweep.py <sweep_dir>
"""

import csv
import json
import math
import re
import sys
from pathlib import Path


def main() -> None:
    sweep = Path(sys.argv[1])
    rows = []
    for run in sorted((sweep / "runs").iterdir()):
        summary = run / "metric" / "summary.json"
        m = re.match(r"r([\d.]+)_a([\d.]+)_t([\d.]+)_pp(\w+)", run.name)
        if not summary.exists() or m is None:
            print(f"skipping (no summary): {run.name}", file=sys.stderr)
            continue
        s = json.loads(summary.read_text())
        cov_ratio = s["median_cov_render"] / s["median_cov_target"]
        rows.append(
            {
                "name": run.name,
                "range_sigma": float(m[1]),
                "arc_sigma": float(m[2]),
                "threshold": float(m[3]),
                "pingping": m[4],
                "cov_ratio": round(cov_ratio, 2),
                "emd_db": s["pooled_emd_db"],
                "shift_db": s["pooled_shift_db"],
            }
        )
    if not rows:
        sys.exit("no results found")

    rows.sort(key=lambda r: (abs(math.log(r["cov_ratio"])), r["emd_db"]))
    out = sweep / "sweep_results.csv"
    with open(out, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader()
        w.writerows(rows)

    print(f"{'name':32} {'cov_ratio':>9} {'emd_db':>7} {'shift_db':>8}")
    for r in rows:
        print(f"{r['name']:32} {r['cov_ratio']:>9} {r['emd_db']:>7} {r['shift_db']:>8}")
    print(f"\nwrote {out}")


if __name__ == "__main__":
    main()
