#!/usr/bin/env bash
# Runs one k6 script as a Job inside the cluster, next to the services it tests, and
# streams its output. Running in-cluster avoids the ingress, the per-client rate limit
# and the laptop's network, all of which would otherwise be what gets measured.
#
#   loadtest/run.sh <script> [profile]
#     script   services-server-api | services-server-metrics | services-server-worker | llm-server | rag-server | app
#     profile  smoke (default) | baseline | stress | soak | spike | users   (the worker ignores it)
#
# Test parameters (TENANT_ID, RATE, ...) come from this shell's environment; see README.md.
# While the test runs, k6's live dashboard is at http://localhost:$DASHBOARD_PORT (5665);
# at the end its HTML report is saved next to the text output in results/.
# Ctrl-C stops the test: the Job is deleted on exit.
set -euo pipefail

SCRIPT=${1:?usage: loadtest/run.sh <script> [profile]}
PROFILE=${2:-smoke}
NAMESPACE=${NAMESPACE:-nudgebee}
TOKEN_SECRET=${TOKEN_SECRET:-nudgebee}
IMAGE=${K6_IMAGE:-grafana/k6:2.2.0}
DASHBOARD_PORT=${DASHBOARD_PORT:-5665}
DIR=$(cd "$(dirname "$0")" && pwd)
[ -f "$DIR/scripts/$SCRIPT.js" ] || { echo "no such script: $SCRIPT (see $DIR/scripts)" >&2; exit 1; }

JOB="k6-$SCRIPT-$(date +%s)"
mkdir -p "$DIR/results"
LOG="$DIR/results/$JOB.log"
REPORT="$DIR/results/$JOB.html"
RAW=$(mktemp)
PF_PID=""

# Only test parameters are forwarded. Service URLs are deliberately not: a local shell
# often exports LLM_SERVER_URL=http://localhost:..., which is wrong inside the cluster,
# and the scripts already default to the in-cluster Service names.
PARAMS=(TENANT_ID USER_ID ROLE ACCOUNT_ID ACCOUNT_IDS RATE TIME_UNIT DURATION MAX_VUS VUS THINK_TIME P95_MS
  CRONS REPEAT INTERVAL QUERY POLL_INTERVAL MAX_WAIT CONVERSATION_P95_MS LLM_SERVER_TOKEN_HEADER
  MODULES K UNIQUE_QUERIES APP_URL API_TOKEN K6_PROMETHEUS_RW_SERVER_URL ACTIONS SWEEP POOL_SIZE)
ENV_FILE=$(mktemp)
echo "PROFILE=$PROFILE" >"$ENV_FILE"
for p in "${PARAMS[@]}"; do
  if [ -n "${!p:-}" ]; then echo "$p=${!p}" >>"$ENV_FILE"; fi
done
OUTPUT_FLAGS=""
if [ -n "${K6_PROMETHEUS_RW_SERVER_URL:-}" ]; then
  OUTPUT_FLAGS="-o experimental-prometheus-rw"
  echo 'K6_PROMETHEUS_RW_TREND_STATS=p(50),p(95),p(99),max' >>"$ENV_FILE"
fi

# The HTML report is written inside the pod as k6 exits, too late for `kubectl cp`, so the
# container prints it base64-encoded between markers and this script decodes it from the log.
BEGIN=@@k6-report-begin@@
END=@@k6-report-end@@
K6_CMD="k6 run --no-usage-report $OUTPUT_FLAGS /scripts/$SCRIPT.js; rc=\$?; if [ -s /tmp/report.html ]; then echo $BEGIN; base64 /tmp/report.html; echo $END; fi; exit \$rc"
hide_report() { awk -v b="$BEGIN" -v e="$END" '$0 == b { h = 1 } !h { print; fflush() } $0 == e { h = 0 }'; }

cleanup() {
  rm -f "$ENV_FILE" "$RAW"
  if [ -n "$PF_PID" ]; then kill "$PF_PID" 2>/dev/null || true; fi
  kubectl -n "$NAMESPACE" delete job "$JOB" --ignore-not-found --wait=false >/dev/null
  kubectl -n "$NAMESPACE" delete configmap,secret "$JOB" --ignore-not-found >/dev/null
}
trap cleanup EXIT

# Parameters go in a Secret because API_TOKEN is a credential.
kubectl -n "$NAMESPACE" create configmap "$JOB" --from-file="$DIR/scripts" >/dev/null
kubectl -n "$NAMESPACE" create secret generic "$JOB" --from-env-file="$ENV_FILE" >/dev/null

token() {
  cat <<EOF
            - name: $1
              valueFrom: { secretKeyRef: { name: $TOKEN_SECRET, key: $1, optional: true } }
EOF
}

kubectl -n "$NAMESPACE" apply -f - >/dev/null <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: $JOB
  labels: { app.kubernetes.io/name: k6-loadtest, app.kubernetes.io/component: $SCRIPT }
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 600
  template:
    metadata:
      labels: { app.kubernetes.io/name: k6-loadtest, app.kubernetes.io/component: $SCRIPT }
    spec:
      restartPolicy: Never
      securityContext: { runAsNonRoot: true, seccompProfile: { type: RuntimeDefault } }
      containers:
        - name: k6
          image: $IMAGE
          command: ["sh", "-c", "$K6_CMD"]
          env:
            - { name: K6_WEB_DASHBOARD, value: "true" }
            - { name: K6_WEB_DASHBOARD_EXPORT, value: /tmp/report.html }
$(token ACTION_API_SERVER_TOKEN)
$(token LLM_SERVER_TOKEN)
$(token RAG_SERVER_TOKEN)
          envFrom:
            - secretRef: { name: $JOB }
          resources:
            requests: { cpu: 500m, memory: 256Mi }
            limits: { cpu: "2", memory: 2Gi }
          securityContext: { allowPrivilegeEscalation: false, capabilities: { drop: [ALL] } }
          volumeMounts:
            - { name: scripts, mountPath: /scripts, readOnly: true }
      volumes:
        - name: scripts
          configMap: { name: $JOB }
EOF

echo "job $NAMESPACE/$JOB started (profile $PROFILE); output also saved to $LOG"
# `logs -f` gives up on a pod that is still ContainerCreating, so wait it out first.
PHASE=""
for _ in $(seq 1 150); do
  PHASE=$(kubectl -n "$NAMESPACE" get pods -l job-name="$JOB" -o jsonpath='{.items[0].status.phase}' 2>/dev/null || true)
  [ -n "$PHASE" ] && [ "$PHASE" != Pending ] && break
  sleep 2
done
if [ -z "$PHASE" ] || [ "$PHASE" = Pending ]; then
  echo "k6 pod did not start within 5 minutes:" >&2
  kubectl -n "$NAMESPACE" describe pods -l job-name="$JOB" | tail -15 >&2
  exit 1
fi
if [ "$PHASE" = Running ]; then
  POD=$(kubectl -n "$NAMESPACE" get pods -l job-name="$JOB" -o jsonpath='{.items[0].metadata.name}')
  kubectl -n "$NAMESPACE" port-forward "pod/$POD" "$DASHBOARD_PORT:5665" >/dev/null 2>&1 &
  PF_PID=$!
  disown "$PF_PID" # no "Terminated" line when cleanup stops it
  sleep 1
  if kill -0 "$PF_PID" 2>/dev/null; then
    echo "live dashboard: http://localhost:$DASHBOARD_PORT (until the test ends)"
  else
    echo "no live dashboard: local port $DASHBOARD_PORT is busy; set DASHBOARD_PORT" >&2
    PF_PID=""
  fi
fi
STREAM_OK=1
kubectl -n "$NAMESPACE" logs -f "job/$JOB" | tee "$RAW" | hide_report | tee "$LOG" || STREAM_OK=0

# The Job's status, not the log stream, decides the verdict: a stream can drop while
# k6 keeps running. k6 exits 99 when a threshold is crossed; that is a result too.
[ "$STREAM_OK" = 1 ] || echo "log stream ended early; waiting for the job to finish" >&2
while :; do
  STATUS=$(kubectl -n "$NAMESPACE" get job "$JOB" -o jsonpath='{.status.succeeded}/{.status.failed}')
  [ "$STATUS" != "/" ] && break
  sleep 5
done
if [ "$STREAM_OK" = 0 ]; then
  kubectl -n "$NAMESPACE" logs "job/$JOB" >"$RAW"
  hide_report <"$RAW" >"$LOG"
  echo "final output:"
  tail -60 "$LOG"
fi
if grep -qx "$END" "$RAW"; then
  awk -v b="$BEGIN" -v e="$END" '$0 == e { f = 0 } f { print } $0 == b { f = 1 }' "$RAW" | base64 -d >"$REPORT"
  echo "HTML report: $REPORT"
else
  echo "no HTML report: k6 skips it when a run is too short"
fi
if [ "${STATUS%%/*}" = "1" ]; then
  echo "PASS: all thresholds held"
else
  echo "FAIL: thresholds crossed or k6 errored; see the summary above"
  exit 1
fi
