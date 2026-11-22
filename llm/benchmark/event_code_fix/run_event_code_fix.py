#!/usr/bin/env python3
"""Score the production event-analysis + code-fix pipeline against synthetic,
log-driven scenarios. See docs/code-analysis-eval-spec.md for the full
contract; README.md for setup and usage.

Five scenario types, dispatched by each scenario.yaml's `type` field
(default "event" for backward compatibility with the original three):
  event           — alert injection -> diagnosis -> optional code fix
  vm_event        — the same, for a VM account: fault injected on the fleet,
                    alert posted to the account's event webhook, and the
                    metrics/logs queries the investigation issued are scored
  explore         — direct, read-only code_analyzer query, no alert involved
  pr_followup     — harness opens a real PR, drives PRFollowupAgent, tears down
  multi_turn_repo — one Ask Nudgebee conversation; every turn must stay on the
                    repository the first turn settled on

Usage:
    export API_SERVER_URL=... ACTION_API_SERVER_TOKEN=... ALERTMANAGER_WEBHOOK_URL=...
    export LLM_SERVER_URL=... LLM_SERVER_TOKEN=...
    export BENCHMARK_ACCOUNT_ID=... BENCHMARK_TENANT_ID=...
    python run_event_code_fix.py [--scenario NAME ...] [--diagnosis-only]
"""

from __future__ import annotations

import argparse
import json
import subprocess
import sys
import time
from pathlib import Path

import conversation_repo_verifier
import event_rca_client
import pr_followup_verifier
from code_analyzer_client import TERMINAL_OK, CodeAnalyzerClient
from code_fix_verifier import verify_fix
from diagnosis_scorer import diagnosis_stage_metrics, score_diagnosis
from event_rca_client import EventCodeFixClient, IngestionTimeout
from explore_scorer import score_explore
from scenario_loader import load_scenarios
from telemetry_scorer import score_telemetry, telemetry_calls
from vm_scenario import restore_commands, run_commands, unresolved_placeholders

SETUP_SKIP_REASONS = {
    "skipped - no git integration configured": "skip:no_git_integration",
    "skipped - no repository mapped for this workload": "skip:no_repository_mapped",
}
MODEL_MISS_SKIP_REASONS = {
    "skipped - investigation concluded no actionable code change": "skip:no_actionable_code_change",
}


def run_event_pipeline(
    client: EventCodeFixClient, scenario: dict, diagnosis_only: bool
) -> tuple[dict, dict | None]:
    """Alert -> event -> RCA -> optional fix verification, without scoring the
    diagnosis text (the dashboard engine scores that itself).

    Returns (row, rca). rca is None when the run ended before a diagnosis
    worth scoring; row["bucket"] already says why.

    A vm_event scenario is the same pipeline with its fault injected on the
    fleet first and restored afterwards, and its queries scored on top.
    """
    if scenario.get("type") != "vm_event":
        return _run_event_pipeline(client, scenario, diagnosis_only)

    missing = unresolved_placeholders(scenario)
    if missing:
        return {
            "name": scenario["name"],
            "tags": scenario.get("tags", []),
            "bucket": "setup_error",
            "detail": f"unset VM fleet variables: {', '.join(sorted(missing))}",
        }, None

    vm = scenario.get("vm", {})
    row, rca = None, None
    try:
        try:
            run_commands(vm.get("inject", []))
        except (subprocess.SubprocessError, OSError) as exc:
            # Assigned, not returned directly, so a restore failure in the
            # finally block still lands on the row.
            row = {
                "name": scenario["name"],
                "tags": scenario.get("tags", []),
                "bucket": "setup_error",
                "detail": f"fault injection failed: {exc}",
            }
            return row, None
        # Let the fault reach the metrics and logs backends before the alert
        # claims it, or the investigation queries a window with no signal.
        time.sleep(vm.get("settle_seconds", 0))
        row, rca = _run_event_pipeline(client, scenario, diagnosis_only)
    finally:
        client.unlink_service_repos()
        restore_failure = restore_commands(vm.get("restore", []))
        if restore_failure and row is not None:
            row["restore_failed"] = restore_failure

    if rca is not None and scenario.get("expected_telemetry"):
        calls = telemetry_calls(rca, client.account_id)
        row["telemetry"] = (
            {
                "status": "not_measured",
                "detail": "APP_DATABASE_URL unset or unreachable",
            }
            if calls is None
            else score_telemetry(scenario["expected_telemetry"], calls)
        )
    return row, rca


def _run_event_pipeline(
    client: EventCodeFixClient, scenario: dict, diagnosis_only: bool
) -> tuple[dict, dict | None]:
    row = {"name": scenario["name"], "tags": scenario.get("tags", [])}
    t0 = time.monotonic()

    fingerprint = client.inject(scenario)
    try:
        event_id = client.wait_for_event(fingerprint)
    except IngestionTimeout as exc:
        row["bucket"] = "ingestion_timeout"
        row["detail"] = str(exc)
        row["seconds"] = round(time.monotonic() - t0, 1)
        return row, None

    link = scenario.get("vm", {}).get("link_repo")
    if link:
        client.link_service_repo(
            event_id, link["container_id"], link["repo"], link.get("branch", "")
        )

    rca = client.run_rca(event_id)
    row["diagnosis_seconds"] = round(time.monotonic() - t0, 1)

    if not isinstance(rca, dict):
        row["bucket"] = "error:rca_invalid_payload"
        row["detail"] = "RCA response payload is not a dictionary"
        row["seconds"] = round(time.monotonic() - t0, 1)
        return row, None

    rca_status = str(rca.get("status", "")).lower()
    if rca_status not in event_rca_client.TERMINAL_OK:
        # A failed/timed-out/terminated RCA run is not a diagnosis to score —
        # scoring it would spend real RAGAS/verdict-judge LLM calls on an
        # empty or partial answer and produce a misleading number.
        row["bucket"] = f"error:rca_{rca_status or 'unknown'}"
        row["detail"] = str(
            rca.get("status_reason") or f"RCA analysis ended with status: {rca_status}"
        )
        row["seconds"] = round(time.monotonic() - t0, 1)
        return row, None

    fixture_repo = scenario.get("fixture_repo")
    if not fixture_repo:
        row["bucket"] = "diagnosis_only"
    elif diagnosis_only:
        row["bucket"] = "diagnosis_only_run"
    else:
        status_reason = (rca.get("status_reason") or "").strip()
        if status_reason in SETUP_SKIP_REASONS:
            row["bucket"] = SETUP_SKIP_REASONS[status_reason]
        elif status_reason in MODEL_MISS_SKIP_REASONS:
            row["bucket"] = MODEL_MISS_SKIP_REASONS[status_reason]
        else:
            fix_result = verify_fix(fixture_repo, rca.get("git_diff", ""))
            row["bucket"] = fix_result["bucket"]
            row["fix_detail"] = fix_result["detail"]

    row["seconds"] = round(time.monotonic() - t0, 1)
    return row, rca


def run_event_scenario(
    client: EventCodeFixClient, scenario: dict, diagnosis_only: bool
) -> dict:
    t0 = time.monotonic()
    row, rca = run_event_pipeline(client, scenario, diagnosis_only)
    if rca is None:
        return row

    row["diagnosis"] = score_diagnosis(scenario, rca)
    metrics = diagnosis_stage_metrics(
        rca, client.account_id, client.tenant_id, client.user_id
    )
    if metrics:
        row["diagnosis_metrics"] = metrics
    # Scoring time counts toward the scenario's wall clock, as it did before
    # the pipeline/scoring split.
    row["seconds"] = round(time.monotonic() - t0, 1)
    return row


def query_explore(
    code_analyzer: CodeAnalyzerClient, scenario: dict
) -> tuple[dict, dict | None]:
    """The read-only code_analyzer query, unscored. Returns (row, response);
    response is None when the query itself failed (row["bucket"] says why)."""
    row = {"name": scenario["name"], "tags": scenario.get("tags", [])}
    t0 = time.monotonic()

    explore = scenario["explore"]
    response = code_analyzer.query(
        {
            "query": explore["query"],
            "git_repo": explore["git_repo"],
            "git_commit": explore["git_commit"],
            "mode": "explore",
        }
    )
    row["seconds"] = round(time.monotonic() - t0, 1)

    if response.get("_status"):
        row["bucket"] = f"error:{response['_status'].lower()}"
        return row, None
    return row, response


def run_explore_scenario(code_analyzer: CodeAnalyzerClient, scenario: dict) -> dict:
    row, response = query_explore(code_analyzer, scenario)
    if response is None:
        return row

    scored = score_explore(scenario, response)
    row["explore"] = scored
    verdict_ok = scored["verdict"] in ("passed", "partially_correct")
    citation_ok = scored["citation_grounded"]
    if verdict_ok and citation_ok:
        row["bucket"] = "resolved"
    elif not verdict_ok and not citation_ok:
        row["bucket"] = "failed"
    else:
        row["bucket"] = "partial"
    return row


def run_pr_followup_scenario(code_analyzer: CodeAnalyzerClient, scenario: dict) -> dict:
    row = {"name": scenario["name"], "tags": scenario.get("tags", [])}
    t0 = time.monotonic()

    pr_followup = scenario["pr_followup"]
    fixture_repo = pr_followup["fixture_repo"]

    # Sweep before, not just clean up after: a crashed prior run's PR gets
    # closed by whichever run happens to go next, so no separate cron is
    # needed just for cleanup (docs/code-analysis-eval-spec.md §5c).
    pr_followup_verifier.sweep_orphaned_prs(fixture_repo["url"])

    diff_file = scenario["_dir"] / pr_followup["incomplete_fix_diff_file"]
    setup = pr_followup_verifier.setup_pr(pr_followup, diff_file, scenario["name"])
    if "error" in setup:
        row["bucket"] = "setup_error"
        row["detail"] = setup["error"]
        row["seconds"] = round(time.monotonic() - t0, 1)
        return row

    # try/finally: an exception from either call below must not skip cleanup
    # and leave the PR open — the orphan sweep only catches that on the
    # *next* run, up to _ORPHAN_MAX_AGE_HOURS later.
    try:
        response = code_analyzer.query(
            {
                "followup": True,
                "pr_url": setup["pr_url"],
                "pr_branch": setup["pr_branch"],
                "git_repo": fixture_repo["url"],
            }
        )
        row["followup_seconds"] = round(time.monotonic() - t0, 1)

        if response.get("_status"):
            row["bucket"] = f"error:{response['_status'].lower()}"
            return row

        result = pr_followup_verifier.verify_followup(
            pr_followup,
            setup["pr_url"],
            setup["pr_branch"],
            setup["repo"],
            setup["before_head_sha"],
        )
        row["bucket"] = result["bucket"]
        row["detail"] = result["detail"]
        return row
    finally:
        pr_followup_verifier.cleanup_pr(setup["pr_url"], setup["repo"])
        row["seconds"] = round(time.monotonic() - t0, 1)


def run_multi_turn_repo_scenario(
    code_analyzer: CodeAnalyzerClient, scenario: dict
) -> dict:
    row = {"name": scenario["name"], "tags": scenario.get("tags", [])}
    t0 = time.monotonic()

    multi_turn = scenario["multi_turn"]
    turns = multi_turn["turns"]
    conversation_id = None
    conversation = None
    try:
        for number, text in enumerate(turns, 1):
            result = code_analyzer.converse(text, conversation_id, turn_number=number)
            conversation_id = result.get("_conversation_id") or conversation_id
            if result["_status"] not in TERMINAL_OK:
                row["bucket"] = f"error:turn_{number}_{result['_status'].lower()}"
                return row
            conversation = result["conversation"]

        scored = conversation_repo_verifier.score_repo_continuity(
            conversation,
            len(turns),
            multi_turn["repo"],
            multi_turn.get("distractor_repo"),
        )
        row["bucket"] = scored["bucket"]
        row["detail"] = scored["detail"]
        row["turns"] = scored["turns"]
        return row
    finally:
        # A turn that says "raise a pr" opens a real one. Close whatever the
        # conversation raised, on whichever repo it landed — a drifted run's PR
        # is exactly the one most likely to be left behind.
        if conversation:
            for i in range(len(turns)):
                pr_url = conversation_repo_verifier.turn_repo_facts(conversation, i)[
                    "pr_url"
                ]
                repo = conversation_repo_verifier.pr_repo_slug(pr_url or "")
                if pr_url and repo:
                    pr_followup_verifier.cleanup_pr(pr_url, repo)
        row["seconds"] = round(time.monotonic() - t0, 1)


def run_one(
    client: EventCodeFixClient,
    code_analyzer: CodeAnalyzerClient,
    scenario: dict,
    diagnosis_only: bool,
) -> dict:
    scenario_type = scenario.get("type", "event")
    if scenario_type in ("event", "vm_event"):
        return run_event_scenario(client, scenario, diagnosis_only)
    if scenario_type == "explore":
        return run_explore_scenario(code_analyzer, scenario)
    if scenario_type == "pr_followup":
        return run_pr_followup_scenario(code_analyzer, scenario)
    if scenario_type == "multi_turn_repo":
        return run_multi_turn_repo_scenario(code_analyzer, scenario)
    raise ValueError(f"unknown scenario type: {scenario_type!r}")


def _finished(row: dict) -> bool:
    """A row that reached a verdict. An error, a setup error or an ingestion
    timeout says the run did not get that far, so resuming must run it again
    and not report it as done."""
    bucket = str(row.get("bucket", ""))
    return not (
        bucket == "error"
        or bucket.startswith("error:")
        or bucket in ("setup_error", "ingestion_timeout")
    )


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument(
        "--scenario-dir", type=Path, default=Path(__file__).parent / "scenarios"
    )
    p.add_argument("--scenario", action="append", help="scenario name; repeatable")
    p.add_argument(
        "--diagnosis-only",
        action="store_true",
        help="skip fix verification even for scenarios with a fixture_repo",
    )
    p.add_argument("--out", type=Path, default=Path("event_code_fix_results.json"))
    args = p.parse_args()

    only = set(args.scenario) if args.scenario else None
    scenarios = load_scenarios(args.scenario_dir, only)
    if not scenarios:
        sys.exit("no scenarios matched")

    rows = []
    done = set()
    if args.out.exists():
        rows = json.loads(args.out.read_text()).get("results", [])
        done = {r["name"] for r in rows if _finished(r)}
        if done:
            print(f"resuming: {len(done)} already run", flush=True)

    client = EventCodeFixClient()
    code_analyzer = CodeAnalyzerClient()
    for i, scenario in enumerate(scenarios, 1):
        if scenario["name"] in done:
            continue
        try:
            row = run_one(client, code_analyzer, scenario, args.diagnosis_only)
        except Exception as exc:  # keep the run alive; one bad scenario is not the run
            row = {
                "name": scenario["name"],
                "bucket": "error",
                "detail": f"{type(exc).__name__}: {exc}",
            }
        rows = [r for r in rows if r["name"] != scenario["name"]] + [row]
        print(
            f"[{i}/{len(scenarios)}] {row['name']}: {row.get('bucket', '?')}",
            flush=True,
        )
        args.out.write_text(json.dumps({"results": rows}, indent=1))

    print(f"\nwrote {args.out}")


if __name__ == "__main__":
    main()
