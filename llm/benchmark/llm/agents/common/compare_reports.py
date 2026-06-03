"""
Benchmark report comparison tool.

Compares two benchmark report JSON files and produces a detailed
per-query delta report showing regressions, improvements, and changes
in accuracy, latency, tool calls, tool names, and cost.

Usage:
    python -m llm.agents.common.compare_reports baseline.json candidate.json [-o output.json] [--threshold 5]
"""

import argparse
import json
import sys
from typing import Any, Dict


def load_report(path: str) -> Dict[str, Any]:
    with open(path) as f:
        return json.load(f)


def delta_obj(baseline: float, candidate: float) -> Dict[str, float]:
    """Compute delta and delta_pct between two values."""
    d = round(candidate - baseline, 6)
    pct = round((d / baseline * 100) if baseline != 0 else 0.0, 2)
    return {"baseline": baseline, "candidate": candidate, "delta": d, "delta_pct": pct}


def get_overall_accuracy(detail: Dict[str, Any]) -> float:
    return (
        detail.get("answer_similarity", 0.0) + detail.get("answer_relevancy", 0.0)
    ) / 2


def compute_summary_delta(
    baseline: Dict[str, Any], candidate: Dict[str, Any]
) -> Dict[str, Any]:
    """Compute deltas for summary-level metrics."""
    bs = baseline.get("summary", {})
    cs = candidate.get("summary", {})

    result = {}

    # Accuracy metrics
    for key in [
        "answer_similarity",
        "answer_relevancy",
        "planner_relevancy",
        "overall_accuracy",
    ]:
        result[key] = delta_obj(bs.get(key, 0.0), cs.get(key, 0.0))

    # Latency
    bl = bs.get("latency", {})
    cl = cs.get("latency", {})
    result["avg_latency_seconds"] = delta_obj(
        bl.get("avg_seconds", 0.0), cl.get("avg_seconds", 0.0)
    )
    result["p50_latency_seconds"] = delta_obj(
        bl.get("p50_seconds", 0.0), cl.get("p50_seconds", 0.0)
    )
    result["p95_latency_seconds"] = delta_obj(
        bl.get("p95_seconds", 0.0), cl.get("p95_seconds", 0.0)
    )

    # Tool calls
    bt = bs.get("tool_calls", {})
    ct = cs.get("tool_calls", {})
    result["avg_tool_calls"] = delta_obj(
        bt.get("avg_per_query", 0.0), ct.get("avg_per_query", 0.0)
    )
    result["tool_success_rate"] = delta_obj(
        bt.get("success_rate", 0.0), ct.get("success_rate", 0.0)
    )

    # Cost
    bc = bs.get("cost", {})
    cc = cs.get("cost", {})
    result["avg_cost_per_query"] = delta_obj(
        bc.get("avg_per_query_usd", 0.0), cc.get("avg_per_query_usd", 0.0)
    )
    result["accuracy_per_dollar"] = delta_obj(
        bc.get("accuracy_per_dollar", 0.0), cc.get("accuracy_per_dollar", 0.0)
    )

    # Tokens
    btkn = bs.get("tokens", {})
    ctkn = cs.get("tokens", {})
    result["avg_cache_hit_rate"] = delta_obj(
        btkn.get("avg_cache_hit_rate", 0.0), ctkn.get("avg_cache_hit_rate", 0.0)
    )

    return result


def compute_tag_delta(
    baseline: Dict[str, Any], candidate: Dict[str, Any]
) -> Dict[str, Any]:
    """Compute deltas for each tag."""
    b_tags = baseline.get("by_tag", {})
    c_tags = candidate.get("by_tag", {})
    all_tags = sorted(set(list(b_tags.keys()) + list(c_tags.keys())))

    tag_delta = {}
    for tag in all_tags:
        bt = b_tags.get(tag, {})
        ct = c_tags.get(tag, {})
        td = {}
        for metric in [
            "overall_accuracy",
            "answer_similarity",
            "answer_relevancy",
            "avg_duration_seconds",
            "avg_tool_calls",
            "avg_cost_usd",
        ]:
            bv = bt.get(metric, 0.0)
            cv = ct.get(metric, 0.0)
            if bv != 0.0 or cv != 0.0:
                td[metric] = delta_obj(bv, cv)
        td["baseline_count"] = bt.get("count", 0)
        td["candidate_count"] = ct.get("count", 0)
        tag_delta[tag] = td

    return tag_delta


def compute_per_query_delta(
    baseline: Dict[str, Any],
    candidate: Dict[str, Any],
    threshold: float,
) -> Dict[str, Any]:
    """Compute per-query deltas, regressions, and improvements."""
    b_details = {d["test_id"]: d for d in baseline.get("details", []) if "test_id" in d}
    c_details = {
        d["test_id"]: d for d in candidate.get("details", []) if "test_id" in d
    }

    matched_ids = sorted(set(b_details.keys()) & set(c_details.keys()))
    only_in_baseline = sorted(set(b_details.keys()) - set(c_details.keys()))
    only_in_candidate = sorted(set(c_details.keys()) - set(b_details.keys()))

    per_query = []
    regressions = []
    improvements = []

    for test_id in matched_ids:
        bd = b_details[test_id]
        cd = c_details[test_id]

        b_acc = get_overall_accuracy(bd)
        c_acc = get_overall_accuracy(cd)
        acc_delta = round(c_acc - b_acc, 2)

        b_tc = bd.get("tool_calls_total", 0)
        c_tc = cd.get("tool_calls_total", 0)
        tc_delta = c_tc - b_tc

        b_lat = bd.get("duration_seconds", 0)
        c_lat = cd.get("duration_seconds", 0)
        lat_delta = round(c_lat - b_lat, 2)

        b_cost = bd.get("cost", 0.0)
        c_cost = cd.get("cost", 0.0)
        cost_delta = round(c_cost - b_cost, 6)

        # Tool name diff
        b_tools = set(bd.get("tool_names", []))
        c_tools = set(cd.get("tool_names", []))
        tools_added = sorted(c_tools - b_tools)
        tools_removed = sorted(b_tools - c_tools)

        # Status classification
        if acc_delta >= threshold:
            status = "improved"
        elif acc_delta <= -threshold:
            status = "regressed"
        else:
            status = "stable"

        entry = {
            "test_id": test_id,
            "tags": cd.get("tags", bd.get("tags", [])),
            "status": status,
            "accuracy": {
                "baseline": round(b_acc, 2),
                "candidate": round(c_acc, 2),
                "delta": acc_delta,
            },
            "latency_seconds": {
                "baseline": round(b_lat, 2),
                "candidate": round(c_lat, 2),
                "delta": lat_delta,
            },
            "tool_calls": {"baseline": b_tc, "candidate": c_tc, "delta": tc_delta},
            "tool_names": {
                "baseline": sorted(b_tools),
                "candidate": sorted(c_tools),
                "added": tools_added,
                "removed": tools_removed,
            },
            "cost_usd": {
                "baseline": round(b_cost, 6),
                "candidate": round(c_cost, 6),
                "delta": cost_delta,
            },
        }
        per_query.append(entry)

        changes = {
            "tool_calls_delta": tc_delta,
            "latency_delta_seconds": lat_delta,
            "tools_added": tools_added,
            "tools_removed": tools_removed,
        }

        if status == "regressed":
            regressions.append(
                {
                    "test_id": test_id,
                    "accuracy_delta": acc_delta,
                    "tags": entry["tags"],
                    "changes": changes,
                }
            )
        elif status == "improved":
            improvements.append(
                {
                    "test_id": test_id,
                    "accuracy_delta": acc_delta,
                    "tags": entry["tags"],
                    "changes": changes,
                }
            )

    # Sort regressions by severity (most regressed first)
    regressions.sort(key=lambda x: x["accuracy_delta"])
    # Sort improvements by magnitude (most improved first)
    improvements.sort(key=lambda x: x["accuracy_delta"], reverse=True)

    return {
        "per_query": per_query,
        "regressions": regressions,
        "improvements": improvements,
        "only_in_baseline": only_in_baseline,
        "only_in_candidate": only_in_candidate,
    }


def compare_reports(
    baseline_path: str,
    candidate_path: str,
    threshold: float = 5.0,
) -> Dict[str, Any]:
    """Compare two benchmark reports and return a comparison dict."""
    baseline = load_report(baseline_path)
    candidate = load_report(candidate_path)

    b_meta = baseline.get("metadata", {})
    c_meta = candidate.get("metadata", {})

    comparison = {
        "baseline": {
            "run_id": b_meta.get("run_id", "unknown"),
            "model_names": b_meta.get("model_names", []),
            "model_providers": b_meta.get("model_providers", []),
            "total_queries": b_meta.get(
                "total_queries", len(baseline.get("details", []))
            ),
            "report_path": baseline_path,
        },
        "candidate": {
            "run_id": c_meta.get("run_id", "unknown"),
            "model_names": c_meta.get("model_names", []),
            "model_providers": c_meta.get("model_providers", []),
            "total_queries": c_meta.get(
                "total_queries", len(candidate.get("details", []))
            ),
            "report_path": candidate_path,
        },
        "threshold": threshold,
        "summary_delta": compute_summary_delta(baseline, candidate),
        "tag_delta": compute_tag_delta(baseline, candidate),
    }

    query_results = compute_per_query_delta(baseline, candidate, threshold)
    comparison.update(query_results)

    return comparison


def print_summary(comparison: Dict[str, Any]) -> None:  # noqa: C901
    """Print a human-readable summary of the comparison."""
    b = comparison["baseline"]
    c = comparison["candidate"]
    sd = comparison["summary_delta"]

    print(f"\n{'=' * 60}")
    print("BENCHMARK COMPARISON REPORT")
    print(f"{'=' * 60}")
    print(
        f"Baseline:  {b['model_names']}  (run: {b['run_id']}, queries: {b['total_queries']})"
    )
    print(
        f"Candidate: {c['model_names']}  (run: {c['run_id']}, queries: {c['total_queries']})"
    )
    print(f"Threshold: {comparison['threshold']}%")

    print(f"\n{'─' * 60}")
    print("SUMMARY DELTAS")
    print(f"{'─' * 60}")
    for key, val in sd.items():
        arrow = "+" if val["delta"] >= 0 else ""
        print(
            f"  {key:30s}  {val['baseline']:>8.2f} → {val['candidate']:>8.2f}"
            f"  ({arrow}{val['delta']:.2f}, {arrow}{val['delta_pct']:.1f}%)"
        )

    # Tag deltas
    tag_delta = comparison.get("tag_delta", {})
    if tag_delta:
        print(f"\n{'─' * 60}")
        print("PER-TAG ACCURACY DELTA")
        print(f"{'─' * 60}")
        for tag, metrics in tag_delta.items():
            acc = metrics.get("overall_accuracy", {})
            if acc:
                arrow = "+" if acc["delta"] >= 0 else ""
                print(
                    f"  {tag:25s}  {acc['baseline']:>6.1f}% → {acc['candidate']:>6.1f}%  ({arrow}{acc['delta']:.1f}%)"
                )

    # Regressions
    regressions = comparison.get("regressions", [])
    if regressions:
        print(f"\n{'─' * 60}")
        print(f"REGRESSIONS ({len(regressions)})")
        print(f"{'─' * 60}")
        for r in regressions:
            changes = r["changes"]
            parts = []
            if changes["tool_calls_delta"] != 0:
                parts.append(f"tool_calls {changes['tool_calls_delta']:+d}")
            if changes["latency_delta_seconds"] != 0:
                parts.append(f"latency {changes['latency_delta_seconds']:+.1f}s")
            if changes["tools_added"]:
                parts.append(f"+tools: {changes['tools_added']}")
            if changes["tools_removed"]:
                parts.append(f"-tools: {changes['tools_removed']}")
            hint = ", ".join(parts) if parts else "no other changes"
            print(
                f"  {r['test_id']:40s}  accuracy: {r['accuracy_delta']:+.1f}%  [{hint}]  tags: {r['tags']}"
            )

    # Improvements
    improvements = comparison.get("improvements", [])
    if improvements:
        print(f"\n{'─' * 60}")
        print(f"IMPROVEMENTS ({len(improvements)})")
        print(f"{'─' * 60}")
        for r in improvements:
            changes = r["changes"]
            parts = []
            if changes["tool_calls_delta"] != 0:
                parts.append(f"tool_calls {changes['tool_calls_delta']:+d}")
            if changes["latency_delta_seconds"] != 0:
                parts.append(f"latency {changes['latency_delta_seconds']:+.1f}s")
            if changes["tools_added"]:
                parts.append(f"+tools: {changes['tools_added']}")
            if changes["tools_removed"]:
                parts.append(f"-tools: {changes['tools_removed']}")
            hint = ", ".join(parts) if parts else "no other changes"
            print(
                f"  {r['test_id']:40s}  accuracy: {r['accuracy_delta']:+.1f}%  [{hint}]  tags: {r['tags']}"
            )

    # Only in one report
    only_b = comparison.get("only_in_baseline", [])
    only_c = comparison.get("only_in_candidate", [])
    if only_b:
        print(f"\n  Only in baseline ({len(only_b)}): {only_b}")
    if only_c:
        print(f"\n  Only in candidate ({len(only_c)}): {only_c}")

    # Overall verdict
    acc_delta = sd.get("overall_accuracy", {})
    print(f"\n{'=' * 60}")
    if acc_delta.get("delta", 0) > 0:
        print(f"VERDICT: Candidate is better (+{acc_delta['delta']:.1f}% accuracy)")
    elif acc_delta.get("delta", 0) < 0:
        print(f"VERDICT: Baseline is better ({acc_delta['delta']:.1f}% accuracy)")
    else:
        print("VERDICT: No significant difference")
    print(
        f"  Regressions: {len(regressions)}  |  Improvements: {len(improvements)}"
        f"  |  Stable: {len(comparison.get('per_query', [])) - len(regressions) - len(improvements)}"
    )
    print(f"{'=' * 60}\n")


def main():
    parser = argparse.ArgumentParser(
        description="Compare two benchmark report JSON files."
    )
    parser.add_argument("baseline", help="Path to baseline report JSON")
    parser.add_argument("candidate", help="Path to candidate report JSON")
    parser.add_argument(
        "-o", "--output", help="Output path for comparison JSON (default: stdout)"
    )
    parser.add_argument(
        "--threshold",
        type=float,
        default=5.0,
        help="Accuracy delta threshold (in %%) to classify as regressed/improved (default: 5)",
    )
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="Suppress human-readable summary output",
    )
    args = parser.parse_args()

    comparison = compare_reports(args.baseline, args.candidate, args.threshold)

    if not args.quiet:
        print_summary(comparison)

    if args.output:
        with open(args.output, "w") as f:
            json.dump(comparison, f, indent=4)
        print(f"Comparison report saved to: {args.output}", file=sys.stderr)
    elif args.quiet:
        json.dump(comparison, sys.stdout, indent=4)


if __name__ == "__main__":
    main()
