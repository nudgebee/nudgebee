#!/usr/bin/env python3
"""Write a SWE-bench run into the existing benchmark tables.

Artifacts previously landed on a PVC, so comparing two runs meant reading JSON
off a volume by hand — which is how two runs that had entered via different
agents sat side by side for days without anyone noticing they measured different
things.

Reuses `benchmark_server.models.benchmark_run` rather than redeclaring the
schema: one definition, no drift. The row shape is the same one the other agent
suites write, so existing queries and UI work unchanged.

Status per instance is taken verbatim from SWE-bench's own report buckets. This
script classifies nothing.

Usage:
    python persist.py --report nubi-code-analyzer.<run>.json \
                      --results results.json --frozen frozen_stratified.txt \
                      --run-id <run> --model-name nubi-code-analyzer
"""

import argparse
import json
import os
import sys
from pathlib import Path

# The five buckets SWE-bench's reporting emits. Anything not resolved counts
# against the score; the distinction only explains *why*.
BUCKETS = {
    "resolved_ids": "resolved",
    "unresolved_ids": "unresolved",
    "empty_patch_ids": "empty_patch",
    "error_ids": "error",
    "incomplete_ids": "incomplete",
}


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--report", required=True, type=Path)
    ap.add_argument("--results", type=Path, help="runner results.json, for per-instance detail")
    ap.add_argument("--frozen", type=Path)
    ap.add_argument("--run-id", required=True)
    ap.add_argument("--model-name", default="nubi-code-analyzer")
    ap.add_argument("--agent", default="code_analyzer")
    ap.add_argument("--llm-provider", default=os.environ.get("LLM_PROVIDER", ""))
    ap.add_argument("--llm-model", default=os.environ.get("LLM_MODEL_NAME", ""))
    args = ap.parse_args()

    if not os.environ.get("APP_DATABASE_URL"):
        sys.exit("APP_DATABASE_URL is not set — nothing to write to")

    try:
        from sqlalchemy import create_engine
        from sqlalchemy.orm import sessionmaker
        from benchmark_server.models.benchmark_run import (
            BenchmarkRun,
            BenchmarkTestResult,
        )
    except ImportError as e:
        sys.exit(f"missing dependency: {e}")

    report = json.loads(args.report.read_text())

    # instance_id -> bucket. Built from the report, so an instance that never ran
    # still gets a row; that is the whole point of the frozen denominator.
    status = {}
    for key, label in BUCKETS.items():
        for iid in report.get(key, []):
            status[iid] = label

    detail = {}
    if args.results and args.results.exists():
        for row in json.loads(args.results.read_text()).get("results", []):
            detail[row["instance_id"]] = row

    frozen = []
    if args.frozen:
        # Same rule as collect.py: --frozen was passed deliberately, so a missing
        # path is an error, never a reason to fall back. Falling back here writes
        # a run whose row count silently disagrees with the set that was actually
        # attempted, which is the defect this whole harness exists to prevent.
        if not args.frozen.exists():
            sys.exit(f"--frozen {args.frozen} does not exist")
        frozen = [l.strip() for l in args.frozen.read_text().splitlines() if l.strip()]
    ids = frozen or sorted(status)

    total = report.get("total_instances", len(ids))
    resolved = report.get("resolved_instances", 0)

    engine = create_engine(os.environ["APP_DATABASE_URL"], pool_pre_ping=True)
    Session = sessionmaker(bind=engine, expire_on_commit=False)
    db = Session()
    try:
        run = BenchmarkRun(
            run_id=args.run_id[:12],
            agent=args.agent,
            state="completed",
            phase="completed",
            run_name=args.run_id,
            progress_total=total,
            progress_current=report.get("submitted_instances", 0),
            # The official report, stored whole. Anything derived from it can be
            # recomputed; the report itself cannot be.
            report_json=report,
            llm_provider=args.llm_provider or None,
            llm_model_name=args.llm_model or args.model_name,
            errors=[],
        )
        db.merge(run)

        for idx, iid in enumerate(ids):
            d = detail.get(iid, {})
            tags = [t for t in (d.get("repo"), d.get("difficulty")) if t]
            # Entry agent is a tag, not prose: it is the field that decides
            # whether two runs may be compared at all.
            if entry := d.get("entry_agent"):
                tags.append(f"entry:{entry}")
            db.merge(BenchmarkTestResult(
                run_id=args.run_id[:12],
                test_id=iid,
                test_index=idx,
                status=status.get(iid, "incomplete"),
                conversation_id=d.get("conversation_id") or "",
                duration_seconds=d.get("seconds", 0.0) or 0.0,
                tags=tags,
                error_message=(d.get("detail") or "")[:2000],
                error_category=d.get("status") if d.get("status") != "ok" else "",
            ))
        db.commit()
    except Exception:
        db.rollback()
        raise
    finally:
        db.close()

    pct = 100 * resolved / total if total else 0.0
    print(f"[persist] run {args.run_id} -> {len(ids)} rows, "
          f"{resolved}/{total} resolved ({pct:.1f}%)")


if __name__ == "__main__":
    main()
