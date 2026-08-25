#!/usr/bin/env python3
"""
OSS-sync gap audit.

For every EE PR merged onto a given EE branch in a commit range, check whether
its content is on the OSS repo. Outputs three buckets:

  - already-labeled  : has oss-synced / oss-skip / oss-deferred — trust the label
  - oss-has-content  : unlabeled, but heuristic check finds the content on OSS
                       (file added by PR exists on OSS at same path with same byte size,
                        OR a unique substring from the diff is present in OSS)
  - GAP              : unlabeled AND heuristic check finds no trace on OSS

Caveats: heuristics, not proof. Both directions of false positive are possible
(OSS may have it under a different path, or independently regenerated content).
Use as a triage list; spot-check items before acting.

Usage:
  ./audit-gap.py --since <ee-sha> --until <ee-sha-or-ref> \
                 --ee-repo /path/to/nudgebee-enterprise \
                 --oss-repo /path/to/nudgebee-oss \
                 [--out /tmp/audit.md]

Requires: gh (authenticated for nudgebee/nudgebee-enterprise) + git.
"""

import argparse
import json
import os
import re
import subprocess
import sys
from pathlib import Path

# ---------- shell helpers ----------


def run(cmd, cwd=None, check=True, env=None):
    res = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, env=env)
    if check and res.returncode != 0:
        sys.exit(f"FAILED: {' '.join(cmd)}\n  stderr: {res.stderr}")
    return res.stdout


def git(args, cwd):
    return run(["git"] + args, cwd=cwd)


_LABEL_CACHE = {}  # populated by warm_label_cache()


def warm_label_cache(repo):
    """One-shot bulk fetch: for each sync label, get the full list of PRs that have it."""
    for label in ("oss-synced", "oss-skip", "oss-deferred"):
        # check=True: `gh pr list` exits 0 even for zero-result queries,
        # so any non-zero exit is a real failure (auth, rate limit, network).
        # Silently swallowing it would leave _LABEL_CACHE empty and cause
        # every labeled PR to look unlabeled — a completely broken audit.
        out = run(
            ["gh", "pr", "list", "--repo", repo, "--search", f"label:{label}",
             "--state", "all", "--json", "number", "--limit", "1000"],
        )
        for entry in json.loads(out or "[]"):
            _LABEL_CACHE.setdefault(entry["number"], set()).add(label)


def gh_pr_labels(pr_num, repo):
    """Return list[str] of sync-relevant labels for an EE PR, fed from cache."""
    return sorted(_LABEL_CACHE.get(pr_num, set()))


# ---------- PR enumeration ----------

# Match "Merge pull request #N" (merge-commit subject)
RE_MERGE = re.compile(r"^Merge pull request #(\d+)")
# Match trailing "(#N)" (squash-merge subject)
RE_SQUASH = re.compile(r"\(#(\d+)\)\s*$")


def enumerate_prs(ee_repo, since, until):
    """Return ordered list[(pr_num, sha, subject)] for PR merges in (since..until]."""
    out = git(
        ["log", f"{since}..{until}", "--pretty=%H|%s", "--reverse"],
        cwd=ee_repo,
    )
    prs = []
    seen = set()
    for line in out.splitlines():
        sha, _, subject = line.partition("|")
        m = RE_MERGE.match(subject) or RE_SQUASH.search(subject)
        if not m:
            continue
        pr = int(m.group(1))
        if pr in seen:
            continue
        seen.add(pr)
        prs.append((pr, sha, subject))
    return prs


# ---------- OSS content probe ----------


def _is_merge(ee_repo, sha):
    parents = git(["log", "-1", "--pretty=%P", sha], cwd=ee_repo).strip()
    return len(parents.split()) > 1


def _diff_range(ee_repo, sha):
    """Return the `A..B` form to diff a commit's changes.

    For a normal commit, that's `sha^..sha`.
    For a merge commit, `git show sha` emits nothing (combined diff is empty for octopus /
    clean merges) — use `sha^1..sha` to compare against first parent.
    """
    if _is_merge(ee_repo, sha):
        return f"{sha}^1..{sha}"
    return f"{sha}^..{sha}"


def files_added(ee_repo, sha):
    """List paths the PR commit added (filter A). Handles merge commits."""
    rng = _diff_range(ee_repo, sha)
    out = git(["diff", "--diff-filter=A", "--name-only", rng], cwd=ee_repo)
    return [l for l in out.splitlines() if l.strip()]


def files_modified(ee_repo, sha):
    """List paths the PR commit modified. Handles merge commits."""
    rng = _diff_range(ee_repo, sha)
    out = git(["diff", "--diff-filter=M", "--name-only", rng], cwd=ee_repo)
    return [l for l in out.splitlines() if l.strip()]


def oss_has_path(oss_repo, path):
    return (Path(oss_repo) / path).exists()


# Lines that aren't strong content signals — skip when sampling diff
SAMPLE_SKIP_PATTERNS = (
    re.compile(r"^\s*$"),
    re.compile(r"^\s*//"),
    re.compile(r"^\s*#"),
    re.compile(r"^\s*\*"),
    re.compile(r"^\s*import\s"),
    re.compile(r"^\s*from\s"),
    re.compile(r"^\s*export\s"),
    re.compile(r"^\s*const\s+\w+\s*=\s*$"),
    re.compile(r"^\s*[\)\}\],]"),
)


def sample_added_lines(ee_repo, sha, path, n=3, min_len=40):
    """Return up to n distinctive added lines from a diff of `path` in `sha`. Handles merges."""
    rng = _diff_range(ee_repo, sha)
    diff = git(["diff", rng, "--", path], cwd=ee_repo)
    candidates = []
    for line in diff.splitlines():
        if not line.startswith("+") or line.startswith("+++"):
            continue
        body = line[1:]
        if len(body.strip()) < min_len:
            continue
        if any(p.match(body) for p in SAMPLE_SKIP_PATTERNS):
            continue
        candidates.append(body.strip())
        if len(candidates) >= n:
            break
    return candidates


def oss_contains_substring(oss_repo, substring):
    """git grep oss_repo for fixed substring — indexed, much faster than recursive grep."""
    res = subprocess.run(
        # `-e` prevents git treating a leading `-` in `substring` as a flag
        # (common in code: negative numbers, comment markers, unified-diff
        # markers) — without it the process would fail or misparse.
        ["git", "grep", "-l", "-F", "--quiet", "-e", substring],
        cwd=oss_repo,
        capture_output=True,
        text=True,
        timeout=10,
    )
    # git grep: 0 = match, 1 = no match, anything else (e.g. 128 for a bad
    # repo path) is a fatal error. Silently returning False on those would
    # turn every real PR into a spurious GAP.
    if res.returncode not in (0, 1):
        sys.exit(f"FAILED: git grep in {oss_repo}\n  stderr: {res.stderr}")
    return res.returncode == 0


# ---------- per-PR verdict ----------


def classify(pr_num, sha, ee_repo, oss_repo, ee_repo_name):
    labels = gh_pr_labels(pr_num, ee_repo_name) or []
    label_str = ",".join(labels) if labels else "(none)"

    sync_labels = {"oss-synced", "oss-skip", "oss-deferred"}
    has_sync_label = bool(sync_labels.intersection(labels))
    if has_sync_label:
        return "LABELED", label_str, None

    # Unlabeled — content probe
    added = files_added(ee_repo, sha)
    modified = files_modified(ee_repo, sha)

    # Strategy: prefer Added files (more decisive). If none, sample Modified.
    if added:
        # Filter EE-only paths from probe set (they won't be on OSS by design)
        applicable_added = [
            p for p in added
            if not p.startswith((
                "app/src/ee/", "api-server/services/ee/", "api-server/services/licensing/",
                "app/src/lib/license", "docs/ee/", "scripts/check-oss-boundary.sh",
                ".oss-exclude", "llm/llm-server/ee/", "llm/llm-server/cmd/imports_enterprise.go",
            )) and not p.endswith(("-prod.yaml", "-dev-gke.yaml", "-test-gke.yaml"))
            and "values-enterprise" not in p
            and ".jules/" not in p
        ]
        if not applicable_added:
            return "EE-ONLY", label_str, "all added files are EE-only paths"

        present = sum(1 for p in applicable_added if oss_has_path(oss_repo, p))
        missing = len(applicable_added) - present
        if missing == 0:
            return "OSS-HAS", label_str, f"{present}/{len(applicable_added)} added files exist on OSS"
        if present == 0:
            return "GAP", label_str, f"all {len(applicable_added)} added files missing on OSS"
        return "PARTIAL", label_str, f"{present}/{len(applicable_added)} added files exist on OSS, {missing} missing"

    # No added files — probe via modified-line samples
    if not modified:
        return "EMPTY", label_str, "no added/modified files in diff"

    # EE-only filter applies to modified files too — a PR that only touches
    # app/src/ee/init.ts is EE-ONLY, not INCONCLUSIVE.
    def _is_ee_only(p):
        return p.startswith((
            "app/src/ee/", "api-server/services/ee/", "api-server/services/licensing/",
            "app/src/lib/license", "docs/ee/", "scripts/check-oss-boundary.sh",
            ".oss-exclude", "llm/llm-server/ee/", "llm/llm-server/cmd/imports_enterprise.go",
        )) or p.endswith(("-prod.yaml", "-dev-gke.yaml", "-test-gke.yaml")) \
          or "values-enterprise" in p or ".jules/" in p

    applicable_modified = [p for p in modified if not _is_ee_only(p)]
    if not applicable_modified:
        return "EE-ONLY", label_str, "all modified files are EE-only paths"

    # Sample lines from first 3 applicable modified files
    matched = 0
    sampled = 0
    for path in applicable_modified[:3]:
        lines = sample_added_lines(ee_repo, sha, path, n=2)
        for line in lines:
            sampled += 1
            if oss_contains_substring(oss_repo, line):
                matched += 1
    if sampled == 0:
        return "INCONCLUSIVE", label_str, "no distinctive lines to sample"
    ratio = matched / sampled
    if ratio >= 0.5:
        return "OSS-HAS", label_str, f"{matched}/{sampled} sampled lines present on OSS"
    if ratio == 0:
        return "GAP", label_str, f"0/{sampled} sampled lines found on OSS"
    return "PARTIAL", label_str, f"{matched}/{sampled} sampled lines on OSS"


# ---------- main ----------


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--since", required=True, help="EE commit/ref (exclusive lower bound)")
    ap.add_argument("--until", required=True, help="EE commit/ref (inclusive upper bound)")
    ap.add_argument("--ee-repo", default="/Users/shiv/Workspace/nudgebee", help="Path to EE repo")
    ap.add_argument("--oss-repo", default="/Users/shiv/Workspace/nudgebee-oss", help="Path to OSS repo")
    ap.add_argument("--ee-repo-name", default="nudgebee/nudgebee-enterprise", help="EE repo name for gh")
    ap.add_argument("--out", help="Write markdown report to this path (default: stdout)")
    ap.add_argument("--bucket-files-dir", default="/tmp", help="Write per-bucket PR-number files (bucket_<NAME>.txt) here for downstream label-loops")
    args = ap.parse_args()

    prs = enumerate_prs(args.ee_repo, args.since, args.until)
    print(f"# OSS-sync gap audit", file=sys.stderr, flush=True)
    print(f"# Range: {args.since}..{args.until}", file=sys.stderr, flush=True)
    print(f"# {len(prs)} PR merges to classify", file=sys.stderr, flush=True)
    print(f"# Warming label cache via bulk gh search...", file=sys.stderr, flush=True)
    warm_label_cache(args.ee_repo_name)
    print(f"# Label cache: {len(_LABEL_CACHE)} labeled PRs across all sync labels", file=sys.stderr, flush=True)
    print(file=sys.stderr, flush=True)

    # Parallel classify — most cost is per-PR git+grep subprocesses; CPU-light.
    from concurrent.futures import ThreadPoolExecutor, as_completed
    # Pre-populate every possible verdict so a zero-count bucket still writes
    # an empty bucket_<verdict>.txt below — otherwise a stale file from a
    # previous run persists and downstream `while read pr` loops process it.
    buckets = {v: [] for v in ("GAP", "PARTIAL", "INCONCLUSIVE", "EMPTY", "OSS-HAS", "EE-ONLY", "LABELED")}
    done = 0

    def _run(item):
        pr, sha, subject = item
        v, l, d = classify(pr, sha, args.ee_repo, args.oss_repo, args.ee_repo_name)
        return pr, sha, subject, v, l, d

    with ThreadPoolExecutor(max_workers=8) as ex:
        futures = {ex.submit(_run, item): item for item in prs}
        for fut in as_completed(futures):
            pr, sha, subject, verdict, label_str, detail = fut.result()
            buckets[verdict].append((pr, sha, subject, label_str, detail))
            done += 1
            print(f"[{done}/{len(prs)}] #{pr} {verdict}: {subject[:80]}", file=sys.stderr, flush=True)

    # Render report
    lines = [
        f"# OSS-sync gap audit — {args.since}..{args.until}",
        "",
        f"**{len(prs)} PRs scanned.** Bucket counts:",
        "",
    ]
    for verdict in ("GAP", "PARTIAL", "INCONCLUSIVE", "EMPTY", "OSS-HAS", "EE-ONLY", "LABELED"):
        lines.append(f"- **{verdict}**: {len(buckets[verdict])}")
    lines.append("")
    lines.append("Bucket meanings:")
    lines.append("- **GAP** — unlabeled, content not detected on OSS. Most likely real miss.")
    lines.append("- **PARTIAL** — unlabeled, some added files present, some missing. Investigate.")
    lines.append("- **INCONCLUSIVE** — unlabeled, no distinctive lines could be sampled. Manual check.")
    lines.append("- **EMPTY** — empty diff. Backmerge wrapper or similar — usually safe to oss-skip.")
    lines.append("- **OSS-HAS** — unlabeled, but content detected on OSS. Retroactively label `oss-synced`.")
    lines.append("- **EE-ONLY** — unlabeled, but added files only touch EE-only paths. Label `oss-skip`.")
    lines.append("- **LABELED** — already labeled (`oss-synced` / `oss-skip` / `oss-deferred`). No action.")
    lines.append("")

    # Drop per-bucket PR-number files for downstream `while read pr; do gh pr edit ...` loops.
    bucket_dir = Path(args.bucket_files_dir)
    bucket_dir.mkdir(parents=True, exist_ok=True)
    for verdict, items in buckets.items():
        # Always overwrite — empty content clears stale files from prior runs.
        content = "\n".join(str(pr) for pr, _, _, _, _ in items)
        if content:
            content += "\n"
        (bucket_dir / f"bucket_{verdict}.txt").write_text(content)

    for verdict in ("GAP", "PARTIAL", "INCONCLUSIVE", "EMPTY", "OSS-HAS", "EE-ONLY"):
        items = buckets[verdict]
        if not items:
            continue
        lines.append(f"## {verdict} ({len(items)})")
        lines.append("")
        lines.append("| PR | Subject | Detail |")
        lines.append("|---|---|---|")
        for pr, sha, subject, label_str, detail in items:
            short_subject = subject.replace("|", "\\|")[:90]
            short_detail = (detail or "").replace("|", "\\|")
            lines.append(f"| [#{pr}](https://github.com/{args.ee_repo_name}/pull/{pr}) | {short_subject} | {short_detail} |")
        lines.append("")

    report = "\n".join(lines)
    if args.out:
        Path(args.out).write_text(report)
        print(f"\nReport written to {args.out}", file=sys.stderr)
    else:
        print(report)


if __name__ == "__main__":
    main()
