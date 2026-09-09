#!/usr/bin/env bash
# One SWE-bench run, end to end, inside the cluster.
#
# Previously stages 2-4 ran from a laptop, so the run died with the shell that
# started it. All four stages happen here now:
#
#   1. predict  — drive NuBi -> code_analyzer -> workspace pod, collect patches
#   2. convert  — results.json -> predictions.jsonl (official format)
#   3. grade    — one Job per instance, running that instance's own image
#   4. score    — capture SWE-bench's per-instance reports, call their aggregator
#
# The same frozen list feeds stage 1 and stage 4. That is what keeps the
# denominator equal to what was attempted rather than to what came back.
#
# Env:
#   FROZEN      frozen instance list (default /app/frozen_stratified.txt)
#   RUN_ID      run identifier (default swebench-<date>)
#   MODEL_NAME  recorded as model_name_or_path (default nubi-code-analyzer)
#   OUT_DIR     where artifacts land (default /results)
#   NUBI_URL / NUBI_TOKEN / NUBI_ACCOUNT_ID / NUBI_TENANT_ID  — see k8s/pipeline-job.yaml
set -euo pipefail

FROZEN="${FROZEN:-/app/frozen_stratified.txt}"
RUN_ID="${RUN_ID:-swebench-$(date +%Y%m%d-%H%M%S)}"
MODEL_NAME="${MODEL_NAME:-nubi-code-analyzer}"
OUT_DIR="${OUT_DIR:-/results}"
DATASET="${DATASET:-/app/dataset}"
TASK_TIMEOUT="${TASK_TIMEOUT:-900}"

[ -f "$FROZEN" ] || { echo "no frozen instance list at $FROZEN" >&2; exit 1; }
mkdir -p "$OUT_DIR/$RUN_ID"
cd "$OUT_DIR/$RUN_ID"

echo "[pipeline] run_id=$RUN_ID  instances=$(wc -l < "$FROZEN")  model=$MODEL_NAME"

echo "[pipeline] 1/5 predict"
python3 /app/run_swebench.py \
  --dataset "$DATASET" \
  --frozen "$FROZEN" \
  --task-timeout "$TASK_TIMEOUT" \
  --out results.json

echo "[pipeline] 2/5 convert"
python3 /app/make_predictions.py \
  --results results.json \
  --model-name "$MODEL_NAME" \
  --out predictions.jsonl

echo "[pipeline] 3/5 grade"
/app/k8s/grade.sh predictions.jsonl "$DATASET"

# --watch is not optional here. Grading pods are deleted as soon as their node is
# drained or reclaimed, and on a spot-backed pool that happens within minutes; a
# whole run's reports were lost exactly that way. Capturing at the end reads
# those instances as errors and understates the score.
echo "[pipeline] 4/5 score"
python3 /app/k8s/collect.py \
  --predictions predictions.jsonl \
  --dataset "$DATASET" \
  --frozen "$FROZEN" \
  --run-id "$RUN_ID" \
  --report-dir . \
  --watch

echo "[pipeline] 5/5 persist"
REPORT=$(ls -1 "$OUT_DIR/$RUN_ID"/*."$RUN_ID".json 2>/dev/null | head -1)
if [ -z "${APP_DATABASE_URL:-}" ]; then
  echo "[pipeline] APP_DATABASE_URL unset — skipping DB write, artifacts are on the volume only"
elif [ -z "$REPORT" ]; then
  echo "[pipeline] no official report found — nothing to persist" >&2
else
  # Non-fatal on purpose. The run has already spent an hour of paid LLM
  # conversations by this point; a database hiccup must not discard a completed
  # measurement that is sitting valid on disk.
  python3 /app/k8s/persist.py \
    --report "$REPORT" \
    --results results.json \
    --frozen "$FROZEN" \
    --run-id "$RUN_ID" \
    --model-name "$MODEL_NAME" || echo "[pipeline] persist failed; artifacts remain on the volume" >&2
fi

echo "[pipeline] artifacts in $OUT_DIR/$RUN_ID"
ls -1 "$OUT_DIR/$RUN_ID"
