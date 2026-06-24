#!/usr/bin/env bash
# Acceptance checks for insecure-default startup warnings (warnings only — no
# service fails to boot, no Helm install is rejected on these findings).
#
#   1. relay-server   warns when security.secret_key is empty
#   2. llm-server     warns when llm_server_jwt_secret / relay_server_secret_key
#                     are the publicly known defaults
#   3. runbook-server warns when relay_server_secret_key is the default
#   4. Helm NOTES.txt warns when NEXTAUTH_DUMMY_CREDS_ENABLED=true
#
# Steps 1-3 run the Go unit tests that boot each config path with the insecure
# default (asserting one warning) and with a strong override (asserting zero).
# Step 4 renders the chart with helm and greps NOTES — skipped if helm is not
# installed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$REPO_ROOT/deploy/kubernetes/nudgebee"
FAIL=0

step() { printf '\n==> %s\n' "$*"; }
pass() { printf 'PASS: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*"; FAIL=1; }
skip() { printf 'SKIP: %s\n' "$*"; }

step "relay-server: empty security.secret_key warns, strong value does not"
if (cd "$REPO_ROOT/collector-server/k8s-collector/relay-server" &&
	go test ./pkg/config -run 'TestLoadWarnsOnEmptySecretKey|TestLoadNoWarningWhenSecretKeySet'); then
	pass "relay-server warning behavior"
else
	fail "relay-server warning behavior"
fi

step "llm-server: default jwt/relay secrets warn, strong values do not"
if (cd "$REPO_ROOT/llm/llm-server" && go test ./config -run 'TestLogSecurityWarnings'); then
	pass "llm-server warning behavior"
else
	fail "llm-server warning behavior"
fi

step "runbook-server: default relay secret warns, strong value does not"
if (cd "$REPO_ROOT/runbook-server" && go test ./config -run 'TestLogSecurityWarnings'); then
	pass "runbook-server warning behavior"
else
	fail "runbook-server warning behavior"
fi

step "Helm NOTES.txt: dummy-creds warning"
if ! command -v helm >/dev/null 2>&1; then
	skip "helm not installed — NOTES.txt checks not run"
else
	# helm template skips NOTES.txt; install --dry-run renders it. The chart
	# requires a non-placeholder encryption key, so set a throwaway one.
	render() {
		helm install nudgebee-notes-check "$CHART_DIR" --dry-run=client --namespace nudgebee \
			--set nudgebee_secret.NUDGEBEE_ENCRYPTION_KEY=acceptance-check-only "$@" 2>&1
	}
	if render | grep -q 'WARNING: NEXTAUTH_DUMMY_CREDS_ENABLED'; then
		pass "default values render the dummy-creds WARNING block"
	else
		fail "default values should render the dummy-creds WARNING block"
	fi
	if render --set nudgebee_secret.NEXTAUTH_DUMMY_CREDS_ENABLED=false |
		grep -q 'WARNING: NEXTAUTH_DUMMY_CREDS_ENABLED'; then
		fail "NEXTAUTH_DUMMY_CREDS_ENABLED=false should not render the WARNING block"
	else
		pass "NEXTAUTH_DUMMY_CREDS_ENABLED=false renders no WARNING block"
	fi
fi

echo
if [ "$FAIL" -ne 0 ]; then
	echo "RESULT: FAIL"
	exit 1
fi
echo "RESULT: OK"
