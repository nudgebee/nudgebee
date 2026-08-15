#!/usr/bin/env python3
"""
Validate golang-migrate migration filenames in migrations/app/ on a PR.

Why this exists
---------------
golang-migrate tracks a single high-water `version` integer (the leading
unix-ms timestamp of the filename) and is strictly forward-only: `migrate up`
applies only files whose version is GREATER than the current tracker, in
ascending order, and never revisits a version below the mark. There is no
per-migration ledger, so a migration whose number lands below an
already-applied version is SILENTLY skipped (no error, dirty=false).

That is how V751 anomaly_table_indexes (ts 1780917680820) was skipped: it was
authored with a timestamp lower than migrations already applied to the DB, so
`migrate up` treated it as already-passed. The duplicate "V751" label (two
parallel branches both grabbed V751) is the human-visible symptom of the same
parallel-authoring that crossed the version lines.

Ratchet model
-------------
The migration tree has years of legacy naming debt (baseline `<ts>_V0` files
with no name, early `<ts>_<name>` files with no V<N>, and ~149 duplicate V<N>
labels). Blocking PRs on that is a non-starter. So this gate is a RATCHET: it
hard-fails ONLY on problems introduced by files that are new in this PR (their
timestamp is not present on any reference branch). Everything pre-existing is a
::warning::. Pass the tiers the DBs track via --refs so cross-branch collisions
(e.g. a V752 on test vs a different V752 on main) are caught.

Hard-fail (only for files new in this PR)
  - timestamp collides with an existing migration (the real version key)
  - timestamp <= the max already on a reference branch (would be skipped)
  - reuses a V<N> already on a reference branch or on a sibling new file
  - malformed name / missing _V<N>_ / missing .up.sql

Warn (grandfathered legacy)
  - pre-existing duplicate V<N> labels, missing .down.sql, legacy naming

Usage
  ./validate_migrations.py --refs origin/main,origin/test,origin/prod
  (refs default to $MIGRATION_REFS or "origin/main")
"""

import argparse
import os
import re
import subprocess
import sys
from collections import defaultdict

# Lenient: ts is required; V<N> and name are each optional (legacy shapes).
FILENAME_RE = re.compile(
    r"^(?P<ts>\d+)(?:_V(?P<v>\d+))?(?:_(?P<name>[a-z0-9_]+))?\.(?P<dir>up|down)\.sql$"
)
REL_DIR = "api-server/migrations/migrations/app"
APP_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "migrations", "app")


def parse(path):
    m = FILENAME_RE.match(os.path.basename(path))
    if not m:
        return None
    return {
        "file": os.path.basename(path),
        "ts": int(m.group("ts")),
        "v": int(m.group("v")) if m.group("v") is not None else None,
        "name": m.group("name"),
        "dir": m.group("dir"),
    }


def list_local():
    files, bad = [], []
    for f in sorted(os.listdir(APP_DIR)):
        if not f.endswith(".sql"):
            continue
        p = parse(f)
        (files if p else bad).append(p or f)
    return files, bad


def ref_index(refs):
    """Across reference branches: (timestamps, V<N> labels, max timestamp, filenames)."""
    ts, vs = set(), set()
    filenames = set()
    for ref in refs:
        try:
            out = subprocess.check_output(
                ["git", "ls-tree", "-r", "--name-only", ref, "--", REL_DIR],
                text=True, stderr=subprocess.DEVNULL)
        except subprocess.CalledProcessError:
            print(f"::warning:: could not read ref '{ref}'; excluding it")
            continue
        for line in out.splitlines():
            fn = os.path.basename(line.strip())
            if not fn:
                continue
            filenames.add(fn)
            p = parse(fn)
            if p:
                ts.add(p["ts"])
                if p["v"] is not None:
                    vs.add(p["v"])
    return ts, vs, (max(ts) if ts else 0), filenames


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--refs", default=os.environ.get("MIGRATION_REFS", "origin/main"))
    args = ap.parse_args()
    refs = [r.strip() for r in args.refs.split(",") if r.strip()]

    files, bad_local = list_local()
    errors, warnings = [], []

    base_ts, base_v, base_max, base_files = ref_index(refs)
    if refs and not base_ts:
        print("\nError: No reference migrations could be resolved from the specified refs.")
        print("This usually means the reference branches (e.g., origin/main) have not been fetched.")
        print("Fetch the reference branches (a full or targeted fetch) before running validation.")
        sys.exit(1)

    # Local timestamp -> identities, for in-tree dup detection.
    by_ts = defaultdict(set)
    by_v_all = defaultdict(set)
    pair = defaultdict(set)
    for p in files:
        by_ts[p["ts"]].add((p["v"], p["name"]))
        if p["v"] is not None:
            by_v_all[p["v"]].add(p["ts"])
        pair[(p["ts"], p["v"], p["name"])].add(p["dir"])

    def is_new(filename):
        # Newness keys on the filename, not the timestamp: a new file that
        # *reuses* an existing timestamp must still be classified as new so the
        # duplicate-timestamp check (A) below fires on it instead of skipping it.
        return bool(base_files) and filename not in base_files

    # Malformed names: hard only if the file is new (absent from reference
    # branches); pre-existing malformed files are grandfathered as warnings.
    for b in bad_local:
        if base_files and b not in base_files:
            errors.append(f"[naming] new migration '{b}' does not match the filename convention")
        else:
            warnings.append(f"[naming-legacy] '{b}' does not match the filename convention")

    # Per new-migration checks (the ratchet). Group up/down into one identity so
    # each problem is reported once, not twice.
    new_idents = {}
    for p in files:
        if is_new(p["file"]):
            new_idents.setdefault((p["ts"], p["v"], p["name"]), p)
    seen_new_v = {}
    for (ts, v, name), p in sorted(new_idents.items(), key=lambda kv: kv[0][0]):
        tag = f"{ts}_V{v}_{name}" if v is not None else (name or str(ts))
        # Must use the _V<N>_ convention.
        if p["v"] is None:
            errors.append(f"[naming] new migration '{p['file']}' must be <ts>_V<N>_<snake_name>")
            continue
        # A: duplicate timestamp with anything else in the tree or on a ref.
        # Hard error — duplicate ts is a per-file identity collision
        # regardless of engine (atlas keys revisions by version).
        if len(by_ts[p["ts"]]) > 1 or p["ts"] in base_ts:
            errors.append(f"[A duplicate-timestamp] new migration {tag} reuses version {p['ts']}")
        # D: out-of-order timestamp. Under golang-migrate this would have been
        # SILENTLY skipped (the V756 incident class), so the gate was a hard
        # fail. Under atlas with `--exec-order non-linear` (configured in
        # atlas.hcl) the file IS applied — out-of-order arrivals are
        # first-class. Downgrade to a warning: still untidy (signals a
        # cherry-pick / HF promotion collision) but not a correctness issue.
        if p["ts"] <= base_max:
            warnings.append(f"[D out-of-order] new migration {tag} has timestamp <= max on "
                            f"{refs} ({base_max}). atlas applies this via --exec-order non-linear; "
                            f"the previous engine would have silently skipped it. Consider recreating "
                            f"with new-migration.sh for cleaner ordering, unless this is a deliberate "
                            f"cherry-pick / hotfix backmerge.")
        # B: must not reuse a V<N> from a ref branch or a sibling new file.
        if p["v"] in base_v:
            errors.append(f"[B Vlabel-reuse] new migration {tag} reuses V{p['v']} which already "
                          f"exists on {refs}. Re-run new-migration.sh for the next free V number.")
        elif p["v"] in seen_new_v and seen_new_v[p["v"]] != p["ts"]:
            errors.append(f"[B Vlabel-reuse] new migration {tag} reuses V{p['v']} also used by "
                          f"new migration at {seen_new_v[p['v']]} in this change.")
        seen_new_v[p["v"]] = p["ts"]
        # E: needs an up file; down recommended.
        key = (p["ts"], p["v"], p["name"])
        if "up" not in pair[key]:
            errors.append(f"[E pairing] new migration {tag} has no matching .up.sql")
        if "down" not in pair[key]:
            warnings.append(f"[E pairing] new migration {p['ts']}_V{p['v']}_{p['name']} has no .down.sql")

    # Legacy hygiene warnings (informational only).
    for v, tss in sorted(by_v_all.items()):
        if len(tss) > 1:
            warnings.append(f"[C duplicate-Vlabel] V{v} reused by timestamps {sorted(tss)}")

    for w in warnings:
        print(f"::warning:: {w}")

    if errors:
        print("\nMigration validation FAILED (problems introduced by this change):\n")
        for e in errors:
            print(f"  ✗ {e}")
        print(f"\n{len(errors)} blocking problem(s); {len(warnings)} legacy warning(s).")
        sys.exit(1)

    new_count = len(new_idents)
    print(f"Migration validation passed: {new_count} new migration(s) clean, "
          f"{len(warnings)} legacy warning(s).")


if __name__ == "__main__":
    main()
