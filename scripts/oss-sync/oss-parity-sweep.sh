#!/usr/bin/env bash
# oss-parity-sweep.sh — post-pick completeness check for EE→OSS syncs.
#
# WHY: build / go vet / type-check / lint only catch INVALID code. They do NOT catch
# a conflict resolution that DROPPED a line or MISORDERED a block — that code still
# compiles. Two prod issues (a TDZ crash from a hoisted useEffect, and Discord missing
# from the integrations catalog) both passed every gate and were only found in the UI.
# The one check that catches this class is a per-file byte-diff against the RELEASE TAG.
#
# WHAT: for every file the sync branch changed vs <base>, diff it against <tag>:<file>.
# Lines matching the known intentional OSS adaptations are filtered out; anything left
# is a REVIEW ⚠ — a probable drop/misorder to investigate. Files at 0 residual = PARITY.
#
# USAGE (run from the OSS repo, on the sync branch):
#   scripts/oss-sync/oss-parity-sweep.sh [<release-tag>] [<base>]
#     <release-tag>  ref reachable in the OSS repo (fetch first: git fetch ee tag 1.4.0). default: 1.4.0
#     <base>         merge-base to diff the branch against. default: origin/main
#
# Exit: 0 if no REVIEW files, 1 if any REVIEW files (so CI/pre-push can gate on it).
set -uo pipefail

TAG="${1:-1.4.0}"
BASE="${2:-origin/main}"

git rev-parse "$TAG" >/dev/null 2>&1 || { echo "ERROR: tag '$TAG' not in this repo. Run: git fetch ee tag $TAG"; exit 2; }

# ── Known intentional OSS adaptations (benign deltas). A changed hunk whose ONLY
#    differing lines match these is expected divergence, NOT a drop. Extend as needed. ──
# DS-V2 token/import conventions + OSS-specific fields are legitimate divergence.
# A hunk whose ONLY differing lines match these is an adaptation, not a drop.
ADAPT='(@shared/|@components/common/|@ui/|@components/KnowledgeGraph|@components/knowledge-graph/|CustomTabs|watchEnabled|nudgebee-.*-base:ubuntu-noble|// OSS |\bcolors\.|\bds\.|var\(--ds-|from '"'"'@components/KnowledgeGraph'"'"')'

changed=$(git diff --name-only "${BASE}...HEAD" -- 'app/**' 'api-server/services/**' 'llm/**' 'collector-server/**' 'ml-k8s-server/**' 'notifications-server/**' 'ticket-server/**' 2>/dev/null)
[ -z "$changed" ] && { echo "No source files changed vs $BASE."; exit 0; }

review=0; parity=0; adapt=0; ossonly=0; migr=0
echo "== OSS↔$TAG parity sweep (branch vs $BASE) =="
while IFS= read -r f; do
  [ -z "$f" ] && continue
  case "$f" in
    *migrations/*) migr=$((migr+1)); continue ;;                 # renumbered on purpose
  esac
  if ! git cat-file -e "$TAG:$f" 2>/dev/null; then
    ossonly=$((ossonly+1)); echo "  OSS-ONLY   $f  (no $TAG counterpart — verify intentional)"; continue
  fi
  d=$(diff <(git show "HEAD:$f" 2>/dev/null) <(git show "$TAG:$f" 2>/dev/null) 2>/dev/null | grep -E '^[<>]')
  if [ -z "$d" ]; then parity=$((parity+1)); continue; fi           # exact parity
  residual=$(echo "$d" | grep -vE "$ADAPT")
  if [ -z "$residual" ]; then adapt=$((adapt+1)); echo "  adaptation $f  (all deltas are known OSS adaptations)"; continue; fi
  review=$((review+1))
  n=$(echo "$residual" | grep -c '^[<>]')
  echo "  REVIEW ⚠   $f  ($n unexplained delta line(s) vs $TAG):"
  echo "$residual" | sed 's/^/      /' | head -8
done <<< "$changed"

echo "-- parity=$parity  adaptation=$adapt  OSS-only=$ossonly  migrations=$migr  REVIEW=$review --"
[ "$review" -gt 0 ] && { echo "!! $review file(s) differ from $TAG beyond known adaptations — investigate each for a dropped/misordered hunk before merging."; exit 1; }
echo "OK: every changed file is at $TAG parity or a known adaptation."
exit 0
