"""Per-provider recall check for memory_write against a running llm-server.

Each case is one chat, pinned to a provider via llm_config_source. The server
must run with LLM_MEMORY_TOOLS_ENABLED=true. Example:

  python -m memory_golden.run --providers anthropic=db:<id>,openai=db:<id>
"""

import argparse
import json
import os
import sys
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta, timezone
from pathlib import Path

import httpx

from memory_golden.scoring import (
    CaseResult,
    ProviderSummary,
    Write,
    name_matches,
    render_repeats,
    render_table,
    score_case,
)

CASES_PATH = Path(__file__).with_name("cases.json")


def parse_providers(raw: str) -> list[tuple[str, str]]:
    out = []
    for item in filter(None, (p.strip() for p in raw.split(","))):
        name, sep, source = item.partition("=")
        if not sep or not source:
            raise ValueError(f"provider {item!r} must be name=llm_config_source")
        out.append((name, source))
    return out


def _dict(value) -> dict:
    return value if isinstance(value, dict) else {}


def extract_writes(body: dict) -> list[Write]:
    data = body.get("data") if isinstance(body, dict) else None
    if not isinstance(data, dict):
        data = body if isinstance(body, dict) else {}
    writes = []
    steps = data.get("agent_step_response")
    for step in steps if isinstance(steps, list) else []:
        if not isinstance(step, dict):
            continue
        fn = _dict(_dict(_dict(step.get("Call")).get("tool_call")).get("function"))
        if fn.get("name") != "memory_write":
            continue
        raw = fn.get("arguments")
        try:
            args = raw if isinstance(raw, dict) else json.loads(raw or "{}")
        except (json.JSONDecodeError, TypeError):
            args = {}
        if not isinstance(args, dict):
            args = {}
        response = _dict(_dict(step.get("Response")).get("tool_response"))
        content = str(response.get("content") or "")
        writes.append(Write(args=args, accepted=content.startswith("Remembered")))
    return writes


class Runner:
    def __init__(self, args: argparse.Namespace):
        self.args = args
        self.headers = {
            os.environ.get("LLM_SERVER_TOKEN_HEADER", "X-ACTION-TOKEN"): os.environ[
                "LLM_SERVER_TOKEN"
            ],
            "x-tenant-id": args.tenant,
            "x-user-id": args.user,
        }

    def run_case(self, client: httpx.Client, source: str, case: dict) -> CaseResult:
        payload = {
            "session_variables": {
                "tenant_id": self.args.tenant,
                "user_id": self.args.user,
            },
            "input": {
                "query": f"@{self.args.agent} {case['message']}",
                "account_id": self.args.account,
                "session_id": str(uuid.uuid4()),
                "async": False,
                "config": {"llm_config_source": source},
            },
        }
        start = time.monotonic()
        try:
            resp = client.post(
                f"{self.args.server}/v1/completions/chat",
                json=payload,
                headers=self.headers,
            )
        except httpx.HTTPError as exc:
            return CaseResult(
                case["id"], case["expect"], "error", detail=str(exc)[:300]
            )
        latency = time.monotonic() - start
        try:
            body = resp.json()
        except ValueError:
            detail = f"HTTP {resp.status_code}: {resp.text[:250]}"
            return CaseResult(case["id"], case["expect"], "error", latency, detail)
        if not isinstance(body, dict):
            body = {}
        if resp.is_error and not body.get("errors"):
            detail = f"HTTP {resp.status_code}: {json.dumps(body)[:250]}"
            return CaseResult(case["id"], case["expect"], "error", latency, detail)
        data = body.get("data") if isinstance(body.get("data"), dict) else {}
        if body.get("errors") or data.get("status") != "COMPLETED":
            detail = json.dumps(body.get("errors") or data.get("status"))[:300]
            return CaseResult(case["id"], case["expect"], "error", latency, detail)
        writes = extract_writes(body)
        detail = json.dumps([w.args for w in writes])[:500]
        return CaseResult(
            case["id"], case["expect"], score_case(case, writes), latency, detail
        )

    def run_provider(
        self, name: str, source: str, cases: list[dict], run: int = 1
    ) -> ProviderSummary:
        summary = ProviderSummary(name, source, run)
        with httpx.Client(timeout=self.args.timeout) as client:
            for case in cases:
                result = self.run_case(client, source, case)
                summary.results.append(result)
                print(
                    f"[{name}] {case['id']}: {result.outcome}",
                    file=sys.stderr,
                    flush=True,
                )
        return summary


def find_existing(db_url: str, tenant: str, user: str, cases: list[dict]) -> list[str]:
    """Rows whose key or subject a save case would reuse; the run would overwrite them and
    cleanup, which only deletes new rows, would leave the overwritten value behind."""
    import psycopg2

    queries = [
        (
            "SELECT 'preference', key FROM llm_memory_preferences WHERE tenant_id = %s AND user_id = %s",
            (tenant, user),
        ),
        (
            "SELECT 'pattern', subject FROM llm_memory_patterns WHERE tenant_id = %s AND user_id = %s",
            (tenant, user),
        ),
        (
            "SELECT 'collective', subject FROM llm_memory_collective WHERE tenant_id = %s",
            (tenant,),
        ),
    ]
    conn = psycopg2.connect(db_url)
    try:
        with conn.cursor() as cur:
            rows = []
            for sql, params in queries:
                cur.execute(sql, params)
                rows.extend(cur.fetchall())
    finally:
        conn.close()
    return [
        f"{case['id']}: {table} {name!r}"
        for case in cases
        if case["expect"] != "none"
        for table, name in rows
        if name and name_matches(case, name)
    ]


def cleanup(db_url: str, tenant: str, user: str, since: datetime) -> None:
    import psycopg2

    statements = [
        "DELETE FROM llm_memory_preferences WHERE tenant_id = %s AND user_id = %s AND created_at >= %s",
        "DELETE FROM llm_memory_patterns WHERE tenant_id = %s AND user_id = %s AND created_at >= %s",
        "DELETE FROM llm_memory_collective WHERE tenant_id = %s AND curated_by = %s AND created_at >= %s",
    ]
    conn = psycopg2.connect(db_url)
    try:
        with conn, conn.cursor() as cur:
            for stmt in statements:
                cur.execute(stmt, (tenant, user, since))
                print(
                    f"cleanup: {cur.rowcount} rows <- {stmt.split()[2]}",
                    file=sys.stderr,
                )
    finally:
        conn.close()


def cleanup_best_effort(args: argparse.Namespace, since: datetime) -> None:
    if args.no_cleanup:
        return
    # The per-turn extractor saves in the background after the reply returns.
    time.sleep(args.settle)
    try:
        # A few seconds of slack for clock skew between this host and the DB.
        since = since - timedelta(seconds=5)
        cleanup(os.environ["LLM_SERVER_DB_URL"], args.tenant, args.user, since)
    except Exception as exc:  # cleanup must not hide the run's own result
        print(f"cleanup failed: {exc}", file=sys.stderr)


def run_all(runner: "Runner", providers, cases, args) -> list[ProviderSummary]:
    jobs = [(n, s, r) for r in range(1, args.repeat + 1) for n, s in providers]
    if args.parallel <= 1:
        summaries = []
        for name, source, run in jobs:
            since = datetime.now(timezone.utc)
            try:
                summaries.append(runner.run_provider(name, source, cases, run))
            finally:
                cleanup_best_effort(args, since)  # next run starts clean
        return summaries
    since = datetime.now(timezone.utc)
    try:
        with ThreadPoolExecutor(max_workers=args.parallel) as pool:
            return list(
                pool.map(lambda j: runner.run_provider(j[0], j[1], cases, j[2]), jobs)
            )
    finally:
        cleanup_best_effort(args, since)


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument(
        "--providers", required=True, help="comma list of name=llm_config_source"
    )
    p.add_argument(
        "--server", default=os.environ.get("LLM_SERVER_URL", "http://localhost:9999")
    )
    p.add_argument("--tenant", default=os.environ.get("TEST_TENANT"))
    p.add_argument("--user", default=os.environ.get("TEST_USER"))
    p.add_argument("--account", default=os.environ.get("TEST_ACCOUNT"))
    p.add_argument("--agent", default="k8s_orchestrator")
    p.add_argument("--cases", type=Path, default=CASES_PATH)
    p.add_argument("--timeout", type=float, default=400)
    p.add_argument(
        "--parallel",
        type=int,
        default=1,
        help="providers run at once; they share the run user's memory, so >1 lets "
        "one provider's saves affect another's cases",
    )
    p.add_argument("--repeat", type=int, default=1, help="runs per provider")
    p.add_argument("--out", type=Path, default=Path("memory_golden_results.json"))
    p.add_argument(
        "--no-cleanup",
        action="store_true",
        help="keep the memory rows the run created (deleted by default)",
    )
    p.add_argument(
        "--settle",
        type=float,
        default=15,
        help="seconds to wait for background saves before cleanup",
    )
    args = p.parse_args()
    if not (args.tenant and args.user and args.account):
        p.error(
            "--tenant, --user and --account (or TEST_TENANT/TEST_USER/TEST_ACCOUNT) are required"
        )
    if not os.environ.get("LLM_SERVER_TOKEN"):
        p.error("LLM_SERVER_TOKEN must be set")
    db_url = os.environ.get("LLM_SERVER_DB_URL")
    if not args.no_cleanup and not db_url:
        p.error("cleanup needs LLM_SERVER_DB_URL (or pass --no-cleanup)")
    if args.repeat < 1:
        p.error("--repeat must be at least 1")

    cases = json.loads(args.cases.read_text())
    if db_url:
        existing = find_existing(db_url, args.tenant, args.user, cases)
        if existing:
            p.error(
                "memory already holds rows a case would overwrite; remove them or use "
                "another tenant/user:\n  " + "\n  ".join(existing)
            )
    try:
        providers = parse_providers(args.providers)
    except ValueError as exc:
        p.error(str(exc))
    if not providers:
        p.error("--providers lists no providers")
    summaries = run_all(Runner(args), providers, cases, args)

    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps([s.as_dict() for s in summaries], indent=2))
    print(render_table(summaries))
    if args.repeat > 1:
        print("\n" + render_repeats(summaries))
    print(f"\nfull results: {args.out}")
    return 0 if all(s.verdict == "PASS" for s in summaries) else 1


if __name__ == "__main__":
    sys.exit(main())
