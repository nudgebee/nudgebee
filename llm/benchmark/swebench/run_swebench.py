#!/usr/bin/env python3
"""Score the NuBi -> code_analyzer chain against SWE-bench Verified.

Drives the same path a customer hits — NuBi's chat API, which routes to
code_analyzer, which drives a workspace pod — and grades the returned analysis
against the human maintainer's patch that ships with each instance.

No containers. SWE-bench packages carry the gold patch in
`<task>/tests/config.json`, so localisation and diff comparison are offline and
deterministic. Running the instance's own FAIL_TO_PASS/PASS_TO_PASS tests needs
the task image and belongs in a separate Harbor-backed runner; the metrics here
are the ones that can be had without paying for that.

Usage:
    export NUBI_URL=http://127.0.0.1:8005
    export NUBI_TOKEN=... NUBI_ACCOUNT_ID=... NUBI_TENANT_ID=...
    python run_swebench.py --dataset /path/to/swe-bench-verified --limit 16
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

TERMINAL_OK = {"COMPLETED"}
TERMINAL_BAD = {"FAILED", "TERMINATED", "KILLED"}


def env(name: str, default: str | None = None) -> str:
    v = os.environ.get(name, default)
    if v is None:
        sys.exit(f"{name} must be set")
    return v


class Nubi:
    def __init__(self) -> None:
        self.url = env("NUBI_URL").rstrip("/")
        self.account = env("NUBI_ACCOUNT_ID")
        self.tenant = env("NUBI_TENANT_ID")
        self.headers = {
            os.environ.get("NUBI_TOKEN_HEADER", "X-ACTION-TOKEN"): env("NUBI_TOKEN"),
            "x-tenant-id": self.tenant,
            "Content-Type": "application/json",
        }

    def _post(self, path: str, body: dict, timeout: int = 90) -> dict:
        req = urllib.request.Request(
            f"{self.url}{path}", data=json.dumps(body).encode(), headers=self.headers
        )
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.load(r)

    def _submit(self, body: dict, attempts: int = 3) -> tuple[str | None, str | None]:
        """Open a conversation, retrying only the submission itself.

        Retrying the *submission* is not retrying the agent: nothing has run yet,
        so pass@1 is intact. A conversation that started and produced a poor patch
        is never retried — that would inflate the score.

        The distinction matters because a failure to even ask is not the agent
        failing, yet it lands in the denominator as an unresolved instance.
        """
        last = None
        for i in range(attempts):
            try:
                started = self._post("/v1/completions/chat", body)
            except urllib.error.HTTPError as e:
                detail = e.read()[:300].decode("utf-8", "replace")
                last = f"HTTP {e.code}: {detail}"
                # 4xx is a malformed request; retrying sends the same bytes again.
                if 400 <= e.code < 500 and e.code != 429:
                    return None, last
            except Exception as e:
                last = f"{type(e).__name__}: {e}"
            else:
                cid = (started.get("data") or {}).get("conversation_id")
                if cid:
                    return cid, None
                last = f"2xx without conversation_id: {json.dumps(started)[:300]}"
            if i + 1 < attempts:
                time.sleep(2 ** i)
        return None, last

    def analyse(self, instance: dict, poll_interval: int, deadline_sec: int) -> dict | None:
        """Run one instance to completion and return code_analyzer's structured response."""
        # The JSON form of code_analyzer's input contract. git_commit is the point
        # of the exercise: without it the workspace clones the branch tip, which
        # for these instances is years past the fix, and the agent correctly
        # reports there is nothing to fix.
        query = json.dumps(
            {
                "query": instance["problem_statement"],
                "git_repo": f"https://github.com/{instance['repo']}",
                "git_commit": instance["base_commit"],
                "mode": "fix",
                # A pinned revision must not raise a PR: the branch would be cut
                # from a stale tree. code-analysis refuses the combination.
                "raise_pr": False,
            }
        )
        # "@code_analyzer" bypasses the router outright: RouterAgent.Execute
        # returns a leading @mention verbatim before any LLM routing runs.
        #
        # Without it, ~19% of runs never reached code_analyzer at all — the
        # router handed an unambiguous code-fix payload (git_repo, git_commit,
        # mode=fix) to k8s_orchestrator, which answered from model knowledge
        # without opening the repository. One such run reasoned about SymPy
        # v1.14.0 for a task pinned to a 2017 commit and declared it fine.
        #
        # That is a real product defect, but it is not a code-fixing failure and
        # averaging it into this score measures the wrong thing. Pinned here;
        # filed separately.
        cid, submit_error = self._submit({
            "query": "@code_analyzer " + query,
            "account_id": self.account,
            "tenant_id": self.tenant,
            "user_id": "",
            "async": True,
        })
        if not cid:
            # Distinct from an agent failure: nothing ever ran. Previously this
            # returned a bare None, which scored as NO_RESPONSE with the server's
            # reply discarded — so the single largest source of lost score (three
            # of sixteen instances in one run, ~19 points) could not be diagnosed
            # after the fact.
            return {"_status": "SUBMIT_FAILED", "_detail": submit_error}

        deadline = time.monotonic() + deadline_sec
        while time.monotonic() < deadline:
            time.sleep(poll_interval)
            try:
                conv = self._post(
                    "/v1/completions/chat_get",
                    {"conversation_id": cid, "account_id": self.account},
                )
            except Exception:
                continue  # transient; the deadline is the real bound
            # `data` can be present-but-null; `.get("data", conv)` returns None
            # for that and the next attribute access dies mid-run.
            data = conv.get("data") or conv
            status = str(data.get("status", "")).upper()
            if status in TERMINAL_BAD:
                return {"_status": status, "_conversation_id": cid}
            if status in TERMINAL_OK:
                resp = _code_analyzer_response(data)
                if resp is None:
                    # Conversation finished, but code_analyzer never ran — the
                    # router sent it elsewhere. Distinct from "the agent tried
                    # and failed", and returning bare None used to discard the
                    # one handle needed to investigate.
                    agents = sorted({
                        a.get("agent_name")
                        for m in (data.get("llm_conversation_messages") or [])
                        for a in (m.get("llm_conversation_agents") or [])
                        if a.get("agent_name")
                    })
                    return {
                        "_status": "NOT_ROUTED_TO_CODE_ANALYZER",
                        "_conversation_id": cid,
                        "_agents_seen": ",".join(agents),
                    }
                resp["_conversation_id"] = cid
                # Which agent the request ENTERED, not merely which ones ran.
                # Two runs that differ here are not comparable, and nothing in
                # SWE-bench's report captures it. Two published rates (50.0% and
                # 56.2%) turned out to have entered via k8s_orchestrator and
                # reached code_analyzer only by delegation — a different path,
                # measured for weeks before anyone checked.
                resp["_entry_agent"] = _entry_agent(data)
                return resp
        return {"_status": "TIMEOUT", "_conversation_id": cid}


def _entry_agent(data: dict) -> str:
    """The agent the conversation was handed to first.

    `llm_conversation_messages` is ordered oldest-first, so the first message
    carrying an agent name is the entry point. Anything after it may have been
    reached by delegation, which is a different execution path with different
    context — and therefore a different thing to be measuring.
    """
    for msg in data.get("llm_conversation_messages") or []:
        if name := (msg.get("agent_name") or "").strip():
            return name
    return ""


def _code_analyzer_response(data: dict) -> dict | None:
    for msg in data.get("llm_conversation_messages") or []:
        for agent in msg.get("llm_conversation_agents") or []:
            if agent.get("agent_name") != "code_analyzer":
                continue
            raw = agent.get("response")
            if not raw:
                continue
            try:
                return json.loads(raw)
            except (json.JSONDecodeError, TypeError):
                return {"_status": "UNPARSEABLE", "_raw": str(raw)[:2000]}
    return None


# --- scoring -----------------------------------------------------------------
# Deliberately no LLM judge. Every metric below is a comparison against the
# maintainer's own patch, so the score is reproducible and moves only when the
# agent changes.


def patch_files(patch: str) -> list[str]:
    return re.findall(r"diff --git a/(\S+)", patch or "")


def patch_hunks(patch: str) -> list[tuple[int, int]]:
    """(start, end) line ranges touched on the pre-image side."""
    out = []
    for start, length in re.findall(r"@@ -(\d+),?(\d*)", patch or ""):
        s = int(start)
        out.append((s, s + (int(length) if length else 1) - 1))
    return out


def changed_lines(patch: str) -> list[str]:
    """Added/removed lines only — ignores hunk headers, index lines and context,
    so cosmetically different diffs of the same edit still compare equal."""
    return [
        ln.rstrip()
        for ln in (patch or "").splitlines()
        if ln[:1] in "+-" and not ln.startswith(("+++", "---"))
    ]


def score(instance: dict, resp: dict | None) -> dict:
    gold = instance["patch"]
    gold_files = patch_files(gold)
    result = {
        "instance_id": instance["instance_id"],
        "repo": instance["repo"],
        "difficulty": instance.get("difficulty"),
        "gold_files": gold_files,
        "status": "ok",
        "file_hit": False,
        "line_in_hunk": False,
        "diff_exact": False,
        "produced_diff": False,
    }
    # Carry the conversation id on failures too. A timeout or a FAILED status is
    # exactly when someone needs to go read what the agent was doing, and dropping
    # the only handle to it there is the opposite of useful.
    if resp is not None:
        result["conversation_id"] = resp.get("_conversation_id")
        if detail := resp.get("_detail"):
            result["detail"] = detail
        if entry := resp.get("_entry_agent"):
            result["entry_agent"] = entry
        if agents := resp.get("_agents_seen"):
            result["agents_seen"] = agents
    if resp is None or resp.get("_status"):
        result["status"] = (resp or {}).get("_status", "NO_RESPONSE")
        return result

    result["title"] = resp.get("title")
    file_path = (resp.get("file_path") or "").strip()
    result["file_path"] = file_path
    result["file_hit"] = file_path in gold_files

    line = resp.get("line_number") or 0
    result["line_number"] = line
    if result["file_hit"] and line:
        result["line_in_hunk"] = any(s <= line <= e for s, e in patch_hunks(gold))

    got = resp.get("git_diff") or ""
    result["produced_diff"] = bool(got.strip())
    if result["produced_diff"]:
        # Keep the patch itself, not just verdicts about it. It is the only
        # artifact that can be re-scored later — the official swebench evaluator
        # needs it as `model_patch`, and without it the run has to be replayed
        # against every conversation to recover what it already produced.
        result["model_patch"] = got
        result["diff_files"] = patch_files(got)
        result["diff_exact"] = changed_lines(got) == changed_lines(gold)
    return result


def load_instances(dataset: Path, limit: int | None, only: set[str] | None) -> list[dict]:
    """Accepts either an unpacked SWE-bench directory or a compacted JSON list.

    The compacted form exists so the whole 500-instance set fits in a ConfigMap
    (1.8 MB raw, 0.51 MB gzipped) and the runner can execute in-cluster without
    an image build or a seeded volume. It keeps only the fields scoring needs.
    """
    if dataset.is_file():
        raw = dataset.read_bytes()
        if dataset.suffix == ".gz" or raw[:2] == b"\x1f\x8b":
            import gzip

            raw = gzip.decompress(raw)
        records = json.loads(raw)
    else:
        records = (json.loads(c.read_text()) for c in sorted(dataset.glob("*/tests/config.json")))

    out = []
    for d in records:
        if only and d["instance_id"] not in only:
            continue
        out.append(d)
        if limit and len(out) >= limit:
            break
    return out


EXPECTED_ENTRY_AGENT = "code_analyzer"


def summarise(rows: list[dict]) -> dict:
    scored = [r for r in rows if r["status"] == "ok"]
    n = len(scored) or 1
    entries = sorted({r.get("entry_agent", "") for r in scored if r.get("entry_agent")})
    return {
        # Recorded in the summary so it travels with the score. A rate whose
        # requests entered somewhere other than code_analyzer is a measurement of
        # a different path, and comparing it with a pinned run is meaningless.
        "entry_agents": entries,
        "instances": len(rows),
        "scored": len(scored),
        "errors": len(rows) - len(scored),
        "file_hit@1": round(sum(r["file_hit"] for r in scored) / n, 3),
        "line_in_hunk": round(sum(r["line_in_hunk"] for r in scored) / n, 3),
        "produced_diff": round(sum(r["produced_diff"] for r in scored) / n, 3),
        "diff_exact": round(sum(r["diff_exact"] for r in scored) / n, 3),
    }


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--dataset", required=True, type=Path)
    p.add_argument("--limit", type=int)
    p.add_argument("--instance", action="append", help="instance_id; repeatable")
    # Predict and score must run over the same list. If the runner picks its
    # instances by --limit and the scorer is handed a different frozen file, the
    # denominator silently stops describing what was attempted — which is exactly
    # how a 50% run got reported as 61.5%.
    p.add_argument("--frozen", type=Path,
                   help="file listing instance ids to run; the same file must be "
                        "passed to collect.py --frozen")
    p.add_argument("--out", type=Path, default=Path("swebench_results.json"))
    p.add_argument("--poll-interval", type=int, default=10)
    p.add_argument("--task-timeout", type=int, default=900)  # successful runs land in 110-330s; 1800 just buys dead wait
    args = p.parse_args()

    only = set(args.instance) if args.instance else None
    if args.frozen:
        frozen = {l.strip() for l in args.frozen.read_text().splitlines() if l.strip()}
        only = frozen if only is None else only & frozen
    instances = load_instances(args.dataset, args.limit, only)
    if args.frozen and len(instances) != len(frozen):
        missing = frozen - {i["instance_id"] for i in instances}
        sys.exit(f"{len(missing)} frozen instance(s) not in dataset: {sorted(missing)[:5]}")
    if not instances:
        sys.exit("no instances matched")

    # Resume: a run is a sequence of independent LLM calls, and losing an hour of
    # them to a dropped connection is the difference between this being usable
    # and not.
    rows: list[dict] = []
    done: set[str] = set()
    if args.out.exists():
        rows = json.loads(args.out.read_text()).get("results", [])
        done = {r["instance_id"] for r in rows if r["status"] == "ok"}
        if done:
            print(f"resuming: {len(done)} already scored", flush=True)

    nubi = Nubi()
    for i, inst in enumerate(instances, 1):
        iid = inst["instance_id"]
        if iid in done:
            continue
        t0 = time.monotonic()
        try:
            resp = nubi.analyse(inst, args.poll_interval, args.task_timeout)
        except Exception as exc:  # keep the run alive; one bad instance is not the run
            resp = {"_status": f"ERROR: {type(exc).__name__}: {exc}"[:200]}
        row = score(inst, resp)
        row["seconds"] = round(time.monotonic() - t0, 1)
        rows = [r for r in rows if r["instance_id"] != iid] + [row]

        flag = "OK " if row["status"] == "ok" else "ERR"
        marks = "".join(
            m if row[k] else "." for k, m in
            (("file_hit", "F"), ("line_in_hunk", "L"), ("diff_exact", "="))
        )
        print(f"[{i}/{len(instances)}] {flag} {marks} {row['seconds']:6.1f}s {iid}", flush=True)

        args.out.write_text(json.dumps({"summary": summarise(rows), "results": rows}, indent=1))

    s = summarise(rows)
    print("\n" + json.dumps(s, indent=1))
    stray = [e for e in s["entry_agents"] if e != EXPECTED_ENTRY_AGENT]
    if stray:
        print(
            f"\nWARNING: {len(stray)} entry agent(s) other than "
            f"{EXPECTED_ENTRY_AGENT}: {stray}. This run measured a different code "
            "path and must not be compared with runs that entered "
            f"{EXPECTED_ENTRY_AGENT} directly.",
            file=sys.stderr,
        )
    print(f"\nwrote {args.out}")


if __name__ == "__main__":
    main()
