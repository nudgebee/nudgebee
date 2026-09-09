#!/usr/bin/env python3
"""Turn in-cluster grading Jobs into SWE-bench's own report, computed by their code.

The previous scoreboard tallied Kubernetes Job exit codes itself. That tally is
where both scoring defects came from: an instance whose Job was never created
vanished from the denominator, and instances the agent errored on never reached
grading at all, so they left the denominator too. Both made the score look better
than it was.

The fix is to own no counting. `test.sh` already writes SWE-bench's own
per-instance report — `{instance_id: {"resolved": bool, "tests_status": {...}}}`,
decided by `get_resolution_status(...) == ResolvedStatus.FULL`. That is the exact
file `swebench.harness.reporting` reads. So this script only:

  1. scrapes that file out of each pod's logs,
  2. rebuilds the log tree their aggregator expects, and
  3. calls their `make_run_report()`.

Every number in the output is therefore produced by the official code, including
the classification of instances that never ran. `full_dataset` is the *frozen
instance list*, not the predictions file — their loop iterates the dataset, so an
instance with no prediction lands in `incomplete_ids` and stays in the
denominator. That property is the whole point.

Usage:
    python collect.py --predictions predictions.jsonl --dataset dataset \
                      --run-id nightly-2026-08-21
"""

import argparse
import inspect
import json
import re
import subprocess
import sys
import time
from pathlib import Path

REPORT_RE = re.compile(
    r"---NB-REPORT-BEGIN---\n(.*?)\n?---NB-REPORT-END---", re.DOTALL
)


def kubectl(*args: str, check: bool = True) -> str:
    p = subprocess.run(
        ["kubectl", *args], capture_output=True, text=True, check=False
    )
    if check and p.returncode != 0:
        raise RuntimeError(f"kubectl {' '.join(args)} failed: {p.stderr.strip()}")
    return p.stdout


def load_predictions(path: Path) -> dict:
    preds = {}
    for line in path.read_text().splitlines():
        if line.strip():
            d = json.loads(line)
            preds[d["instance_id"]] = d
    return preds


def load_frozen_set(dataset: Path, predictions: dict, frozen: Path | None) -> list:
    """The set we intended to grade.

    Prefer an explicit frozen list; fall back to the dataset directory. Falling
    back to `predictions.keys()` would reintroduce the exact bug this script
    exists to prevent, so that is deliberately not an option.

    The directory fallback is a convenience for one-off whole-dataset runs and is
    a trap for anything else: `dataset/` holds all 500 task packages, so a 16
    instance run scored through it would silently report out of 500. Pass
    --frozen for any run that is not the entire dataset.
    """
    explicit = frozen is not None
    frozen = frozen or dataset / "instances.txt"
    if explicit and not frozen.exists():
        # Never fall through to the dataset glob here. --frozen was passed on
        # purpose, and dataset/ holds all 500 task packages: a typo would score a
        # 16-instance run out of 500 and report it as a catastrophic drop.
        sys.exit(f"--frozen {frozen} does not exist")
    if frozen.exists():
        ids = [l.strip() for l in frozen.read_text().splitlines() if l.strip()]
    elif dataset.is_dir():
        ids = sorted(p.parent.parent.name for p in dataset.glob("*/tests/config.json"))
    else:
        sys.exit(
            f"no frozen instance list at {frozen} and {dataset} is not a task dir; "
            "refusing to infer the denominator from predictions"
        )
    missing = set(predictions) - set(ids)
    if missing:
        sys.exit(
            f"predictions contain {len(missing)} instance(s) outside the frozen set "
            f"(e.g. {sorted(missing)[:3]}) — the denominator would be wrong"
        )
    return [{"instance_id": i} for i in ids]


def settled_instances(ns: str) -> dict:
    """instance_id -> True if its Job has settled (succeeded or failed)."""
    items = json.loads(kubectl("-n", ns, "get", "jobs", "-l", "app=swebench-grade",
                               "-o", "json", check=False) or '{"items":[]}')["items"]
    out = {}
    for j in items:
        st = j.get("status") or {}
        out[j["metadata"]["labels"]["instance"]] = bool(
            st.get("succeeded") or st.get("failed")
        )
    return out


def pod_log(ns: str, instance_id: str) -> str | None:
    """Logs for an instance's grading pod, or None if it never produced any."""
    out = kubectl(
        "-n", ns, "get", "pods",
        "-l", f"app=swebench-grade,instance={instance_id}",
        "-o", "jsonpath={.items[*].metadata.name}",
        check=False,
    ).split()
    for pod in out:
        logs = kubectl("-n", ns, "logs", pod, "--tail=-1", check=False)
        if logs:
            return logs
    return None


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--predictions", required=True, type=Path)
    ap.add_argument("--dataset", required=True, type=Path)
    ap.add_argument("--run-id", required=True)
    ap.add_argument("--namespace", default="nudgebee")
    ap.add_argument("--report-dir", default=".", type=Path)
    ap.add_argument("--watch", action="store_true",
                    help="poll and capture each report as its Job settles; "
                         "required whenever grading runs on reclaimable nodes")
    ap.add_argument("--watch-timeout", type=int, default=3600)
    ap.add_argument("--poll-interval", type=int, default=30)
    ap.add_argument("--frozen", type=Path,
                    help="file listing the instance ids this run intended to grade "
                         "(default: <dataset>/instances.txt)")
    args = ap.parse_args()

    # Imported here so the failure mode is a clear message rather than a
    # traceback at import time when the venv is missing.
    try:
        from swebench.harness.constants import (
            LOG_INSTANCE,
            LOG_REPORT,
            LOG_TEST_OUTPUT,
            RUN_EVALUATION_LOG_DIR,
        )
        from swebench.harness.reporting import make_run_report
        import swebench
    except ImportError:
        sys.exit("swebench not installed — `uv pip install swebench` in .venv-swebench")

    predictions = load_predictions(args.predictions)
    full_dataset = load_frozen_set(args.dataset, predictions, args.frozen)

    def dest_for(iid: str) -> Path:
        model = predictions[iid]["model_name_or_path"].replace("/", "__")
        return RUN_EVALUATION_LOG_DIR / args.run_id / model / iid

    def scrape(iid: str) -> bool:
        """Capture one instance's report. Idempotent — already-captured wins."""
        dest = dest_for(iid)
        if (dest / LOG_REPORT).exists():
            return True
        logs = pod_log(args.namespace, iid)
        if logs is None:
            return False
        dest.mkdir(parents=True, exist_ok=True)
        # test_output.txt and run_instance.log feed their infra-failure
        # classifier, which explains *why* a non-resolved instance failed. It is
        # additive and does not move the denominator.
        (dest / LOG_TEST_OUTPUT).write_text(logs)
        (dest / LOG_INSTANCE).write_text(logs)
        m = REPORT_RE.search(logs)
        if m and m.group(1).strip():
            (dest / LOG_REPORT).write_text(m.group(1))
            return True
        return False

    # Empty-patch instances are classified from the prediction itself and are
    # never graded, so waiting for a report they cannot produce just burns the
    # watch window.
    pending = [
        i["instance_id"] for i in full_dataset
        if predictions.get(i["instance_id"], {}).get("model_patch")
    ]

    # Pod logs are ephemeral. A completed pod is deleted whenever its node is
    # drained or reclaimed — on a spot-backed pool that happens within minutes,
    # and a whole run's worth of reports vanished exactly that way. The Job
    # object survives, the log does not, so the report has to be taken as soon as
    # the Job settles rather than after the last one finishes.
    deadline = time.monotonic() + args.watch_timeout
    # A Job can settle without ever producing a capturable report — an evicted
    # pod, an image that never ran. Without a cap those instances are retried
    # every poll until the deadline, turning a five-minute run into an hour of
    # polling something that will never appear.
    attempts: dict[str, int] = {}
    while args.watch and pending and time.monotonic() < deadline:
        settled = settled_instances(args.namespace)
        for iid in list(pending):
            if not settled.get(iid):
                continue
            if scrape(iid):
                pending.remove(iid)
                continue
            attempts[iid] = attempts.get(iid, 0) + 1
            if attempts[iid] >= 3:
                print(f"[collect] giving up on {iid}: Job settled, no report", flush=True)
                pending.remove(iid)
        done = len(predictions) - len(pending)
        print(f"[collect] captured {done}/{len(predictions)}", flush=True)
        if pending:
            time.sleep(args.poll_interval)

    # Final sweep: picks up anything already finished (and everything, when not
    # watching).
    for iid in list(pending):
        if scrape(iid):
            pending.remove(iid)

    scraped = len(predictions) - len(pending)
    print(f"[collect] scraped {scraped}/{len(predictions)} per-instance reports", flush=True)
    if pending:
        # Loud, because their aggregator will count these as errors and the score
        # would silently read low. A lost log is not a failed instance.
        print(
            f"[collect] WARNING: no report captured for {len(pending)} instance(s) — "
            "their pods are gone, so these count as errors and the score is a "
            f"floor, not a measurement: {sorted(pending)}",
            file=sys.stderr,
        )

    args.report_dir.mkdir(parents=True, exist_ok=True)
    # `report_dir` only exists in swebench 5.x; 4.x writes to the cwd. Probe
    # rather than assume — the image pin and the local venv drifted apart once
    # already, and it surfaced only after a full run had been paid for.
    kwargs = {}
    if "report_dir" in inspect.signature(make_run_report).parameters:
        kwargs["report_dir"] = str(args.report_dir)
    elif args.report_dir.resolve() != Path.cwd():
        sys.exit(
            f"swebench {getattr(swebench, '__version__', '?')} writes the report to "
            f"the cwd and cannot honour --report-dir {args.report_dir}; run from "
            "that directory instead"
        )
    path = make_run_report(predictions, full_dataset, args.run_id, **kwargs)
    print(f"[collect] official report: {path}")

    report = json.loads(Path(path).read_text())
    total = report["total_instances"]
    resolved = report["resolved_instances"]
    print(f"\n  % Resolved: {100 * resolved / total:.1f}%  ({resolved}/{total})")


if __name__ == "__main__":
    main()
