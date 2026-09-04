#!/usr/bin/env bash
# make demo -- both windows, Finding 1, end to end.
#
# Runs the simulator once per quirk (each in isolation, against a control run in
# which every quirk is off), reconciles each, and renders reports/FINDINGS.md.
# Same seed in, identical tables out.
set -euo pipefail
cd "$(dirname "$0")/.."

SEED="${SHADOWBOOK_SEED:-20260903}"
WORK="${SHADOWBOOK_WORK:-reports/runs/demo}"
UV="${UV:-uv}"

echo "SHADOWBOOK demo -- seed ${SEED}"
rm -rf "${WORK}"
mkdir -p "${WORK}" reports

start=$(date +%s)

echo "[1/3] simulating and reconciling both windows, one quirk at a time"
"${UV}" run python -m reconcile.finding1 \
    --seed "${SEED}" \
    --quirks legacy-sim/quirks.yaml \
    --workdir "${WORK}/sim" \
    --out "${WORK}/finding1.json" > /dev/null

echo "[2/3] rendering reports/FINDINGS.md"
"${UV}" run python -m report.render \
    --finding1 "${WORK}/finding1.json" \
    --finding2 "${WORK}/finding2.json" \
    --repo . \
    --out reports/FINDINGS.md

echo "[3/3] summary"
"${UV}" run python - "${WORK}/finding1.json" <<'PY'
import json, sys
f = json.load(open(sys.argv[1]))
specs = f["quirk_specs"]
detected = []
for q in sorted(specs, key=lambda x: int(x[1:])):
    rows = [v["discoveries"][q] for k, v in f["per_quirk"].items()
            if k.split("/")[0] == q and q in v["discoveries"]]
    detected.append(any(r["detected"] for r in rows))
print(f"  Finding 1: {sum(detected)} of {len(detected)} quirks detected")
for w, row in sorted(f["combined"].items()):
    c = row["by_classification"]
    print(f"  {w}: {row['total_breaks']} breaks over {row['business_days']} business days "
          f"({c['timing']} timing, {c['model_difference']} model-difference, {c['defect']} defect)")
PY

end=$(date +%s)
echo "  wall time: $((end - start))s (NFR-4 budget: 300s)"
echo "  report: reports/FINDINGS.md"
