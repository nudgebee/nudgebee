#!/usr/bin/env bash
# Grade SWE-bench predictions in-cluster, one Job per instance.
#
# Why one Job per instance: each instance needs its own image
# (swebench/sweb.eval.x86_64.<slug>) carrying that commit's fully installed
# environment, and a Job has a single pod spec. Kubernetes then spreads them
# across nodes, so the ~4 GB images land on different disks instead of stacking
# on one — which is what makes this infeasible on a laptop.
#
# Why not `swebench eval`: that orchestrates containers through a Docker daemon,
# which a containerd cluster does not have. Running each instance AS a pod needs
# no daemon, no privileged pods, and no docker-in-docker. The grading logic is
# still theirs — tests/test.sh ships with each task package and ends in
# `sys.exit(0 if resolved else 1)`.
#
# Which means the result needs no shared storage: Job Succeeded == resolved,
# Job Failed == unresolved. Read it with:
#
#   kubectl -n nudgebee get jobs -l app=swebench-grade
#
# Usage:  ./grade.sh [predictions.jsonl] [dataset-dir]
set -euo pipefail

NS=nudgebee
PRED="${1:-$(dirname "$0")/../predictions.jsonl}"
DATASET="${2:-$(dirname "$0")/../dataset}"

[ -f "$PRED" ] || { echo "no predictions at $PRED" >&2; exit 1; }

echo "[grade] packing patches + grading scripts"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/patches" "$TMP/grade" "$TMP/config"

python3 - "$PRED" "$DATASET" "$TMP" <<'PY'
import json, shutil, sys, pathlib
pred, dataset, tmp = sys.argv[1], pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3])
ids = []
for line in open(pred):
    d = json.loads(line)
    iid = d["instance_id"]
    if not d.get("model_patch"):
        # An empty patch means the agent produced nothing. SWE-bench's aggregator
        # buckets it as empty_patch straight from the prediction, before it ever
        # looks for a report — so grading it would spend a Job to guarantee a
        # `git apply` failure and change no outcome.
        continue
    # Verbatim. Stripping a diff removes the single-space trailing context line,
    # leaving the hunk one line short of its own @@ header — git then rejects the
    # whole patch as corrupt.
    (tmp / "patches" / f"{iid}.diff").write_text(d["model_patch"])
    shutil.copy(dataset / iid / "tests" / "test.sh", tmp / "grade" / f"{iid}.sh")
    # parser.py reads /tests/config.json for the FAIL_TO_PASS / PASS_TO_PASS
    # lists — without it the tests run and the grading step dies.
    shutil.copy(dataset / iid / "tests" / "config.json", tmp / "config" / f"{iid}.json")
    ids.append(iid)
# Trailing newline is load-bearing: `while read -r IID` returns non-zero on a
# final line that is not newline-terminated, so bash leaves the loop without
# running the body and the last instance is silently never graded.
(tmp / "ids.txt").write_text("".join(f"{i}\n" for i in ids))
print(f"  {len(ids)} instances")
PY

kubectl -n "$NS" delete configmap swebench-patches swebench-grade swebench-config --ignore-not-found >/dev/null
kubectl -n "$NS" create configmap swebench-patches --from-file="$TMP/patches" >/dev/null
kubectl -n "$NS" create configmap swebench-grade   --from-file="$TMP/grade"   >/dev/null
kubectl -n "$NS" create configmap swebench-config  --from-file="$TMP/config"  >/dev/null
echo "[grade] configmaps created"

kubectl -n "$NS" delete jobs -l app=swebench-grade --ignore-not-found >/dev/null 2>&1 || true

while read -r IID; do
  [ -n "$IID" ] || continue
  # swebench image naming: astropy__astropy-12907 -> astropy_1776_astropy-12907
  # ("__" becomes "_1776_", 1776 being the decimal codepoint pair they encode with)
  SLUG=$(echo "$IID" | sed 's/__/_1776_/')
  JOB=$(echo "grade-$IID" | tr '[:upper:]_' '[:lower:]-' | cut -c1-63 | sed 's/-*$//')

  # Not silenced: a rejected Job manifest is indistinguishable from a graded
  # instance once the run is over, and a missing job reads as a missing score.
  cat <<EOF | kubectl -n "$NS" apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: $JOB
  labels: { app: swebench-grade, instance: "$IID" }
spec:
  # No retries. test.sh's exit code IS the verdict, so a retry could flip a
  # genuine FAILED to SUCCEEDED on a flake and silently inflate the score.
  backoffLimit: 0
  ttlSecondsAfterFinished: 86400
  template:
    metadata:
      labels: { app: swebench-grade, instance: "$IID" }
    spec:
      restartPolicy: Never
      containers:
        - name: grade
          image: swebench/sweb.eval.x86_64.$SLUG:latest
          command: ["bash","-lc"]
          args:
            - |
              set -o pipefail
              # test.sh drives grading through \`uv run parser.py\`, which pulls
              # swebench==4.0.3 from PyPI. The Harbor task image adds uv; the raw
              # swebench base image does not, so install it here.
              if ! command -v uv >/dev/null 2>&1; then
                curl -LsSf https://astral.sh/uv/install.sh | sh >/dev/null 2>&1
              fi
              export PATH="/root/.local/bin:\$PATH"
              cd /testbed
              git apply -v /patches/$IID.diff || { echo "PATCH_APPLY_FAILED"; exit 1; }
              mkdir -p /logs/verifier
              bash /grade/$IID.sh
              rc=\$?
              # test.sh already writes SWE-bench's own per-instance report — the same
              # {instance_id: {resolved, tests_status}} file their aggregator reads.
              # Echo it so collect.py can rebuild their log tree from pod logs and let
              # THEIR make_run_report() do the counting. Nothing here interprets it.
              echo "---NB-REPORT-BEGIN---"
              cat /logs/verifier/report.json 2>/dev/null || true
              echo "---NB-REPORT-END---"
              exit \$rc
          volumeMounts:
            - { name: patches, mountPath: /patches }
            - { name: grade,   mountPath: /grade }
            # subPath so this instance's config lands at exactly /tests/config.json
            - { name: config,  mountPath: /tests/config.json, subPath: $IID.json }
          resources:
            requests: { cpu: "500m", memory: "2Gi", ephemeral-storage: "8Gi" }
            limits:   { cpu: "2",    memory: "6Gi", ephemeral-storage: "20Gi" }
      volumes:
        - name: patches
          configMap: { name: swebench-patches }
        - name: grade
          configMap: { name: swebench-grade, defaultMode: 0755 }
        - name: config
          configMap: { name: swebench-config }
EOF
  echo "  submitted $JOB"
done < "$TMP/ids.txt"

# The denominator is derived from Jobs that exist, so an instance with no Job
# does not fail — it silently leaves both numerator and denominator and the run
# still prints a plausible rate. Assert the counts match instead.
WANT=$(wc -l < "$TMP/ids.txt" | tr -d ' ')
GOT=$(kubectl -n "$NS" get jobs -l app=swebench-grade --no-headers 2>/dev/null | wc -l | tr -d ' ')
if [ "$WANT" != "$GOT" ]; then
  echo "[grade] FATAL: $WANT predictions but $GOT jobs — scores would be computed over an incomplete set" >&2
  exit 1
fi
echo "[grade] $GOT/$WANT jobs submitted"

echo
echo "[grade] watch:   kubectl -n $NS get jobs -l app=swebench-grade -w"
echo "[grade] score:   $(dirname "$0")/score.sh"
