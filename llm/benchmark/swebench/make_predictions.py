#!/usr/bin/env python3
"""Convert run_swebench.py results into the official SWE-bench predictions file.

The evaluator that produces the only comparable number — `resolved`, what the
leaderboard reports — takes predictions in this shape:

    {"instance_id": ..., "model_name_or_path": ..., "model_patch": ...}

then applies each patch at base_commit, applies the test patch, runs the tests,
and grades with the repo-specific parser:

    swebench eval verified -p predictions.jsonl --run-id <id>

Runs before 2026-08-18 did not persist `model_patch` (the scorer kept only
verdicts about the diff, not the diff), so this falls back to re-reading it from
the NuBi conversation each result already points at. Newer runs carry the patch
inline and need no network at all.

    python make_predictions.py --results swebench_postfix.json --out predictions.jsonl
"""

import argparse
import json
import os
import sys
import urllib.request
from pathlib import Path


def patch_from_conversation(conv_id: str) -> str | None:
    """Re-read git_diff from a NuBi conversation. Needs NUBI_* env set."""
    url = os.environ.get("NUBI_URL", "").rstrip("/")
    account = os.environ.get("NUBI_ACCOUNT_ID", "")
    if not url or not account:
        return None
    headers = {
        os.environ.get("NUBI_TOKEN_HEADER", "X-ACTION-TOKEN"): os.environ.get("NUBI_TOKEN", ""),
        "x-tenant-id": os.environ.get("NUBI_TENANT_ID", ""),
        "Content-Type": "application/json",
    }
    req = urllib.request.Request(
        f"{url}/v1/completions/chat_get",
        data=json.dumps({"conversation_id": conv_id, "account_id": account}).encode(),
        headers=headers,
    )
    try:
        with urllib.request.urlopen(req, timeout=90) as r:
            payload = json.load(r)
    except Exception as exc:
        print(f"  ! fetch failed for {conv_id}: {exc}", file=sys.stderr)
        return None

    # Present-but-null `data` returns None from .get(k, default), and the
    # next attribute access dies.
    data = payload.get("data") or payload
    for msg in data.get("llm_conversation_messages") or []:
        for agent in msg.get("llm_conversation_agents") or []:
            if agent.get("agent_name") != "code_analyzer" or not agent.get("response"):
                continue
            try:
                # Never strip() a diff. A trailing context line is a single
                # space, and stripping it leaves the hunk one line short of the
                # count in its own @@ header — git then rejects the whole patch
                # with "corrupt patch at line N". The official harness has apply
                # fallbacks that hide this; a plain `git apply` does not.
                diff = json.loads(agent["response"]).get("git_diff") or ""
                return diff if diff.strip() else None
            except (json.JSONDecodeError, TypeError):
                return None
    return None


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--results", required=True, type=Path)
    p.add_argument("--out", type=Path, default=Path("predictions.jsonl"))
    p.add_argument("--model-name", default="nubi-code-analyzer")
    args = p.parse_args()

    results = json.loads(args.results.read_text())["results"]
    written, missing, fetched = 0, [], 0

    with args.out.open("w") as fh:
        for r in results:
            iid = r["instance_id"]
            patch = r.get("model_patch")
            if not patch and r.get("produced_diff") and r.get("conversation_id"):
                patch = patch_from_conversation(r["conversation_id"])
                if patch:
                    fetched += 1
            if not patch:
                missing.append(iid)
                if not r.get("conversation_id"):
                    # Never reached the agent. Omitting it leaves it out of the
                    # predictions, and SWE-bench files an instance with no
                    # prediction as `incomplete` — which is exactly what it was.
                    continue
                # The agent ran and produced nothing. That is `empty_patch` in
                # SWE-bench's taxonomy, and it only lands there if a prediction
                # exists carrying an empty model_patch. Omitting it instead would
                # mislabel an agent failure as "never ran" — same score, wrong
                # diagnosis, and the diagnosis is the point of the buckets.
                patch = ""
            fh.write(json.dumps({
                "instance_id": iid,
                "model_name_or_path": args.model_name,
                # Exactly "" for the empty case: SWE-bench tests
                # `model_patch in ["", None]`, so a lone newline would be
                # read as a real patch and then fail to apply.
                "model_patch": "" if not patch else (patch if patch.endswith("\n") else patch + "\n"),
            }) + "\n")
            written += 1

    print(f"wrote {written} predictions to {args.out}" + (f" ({fetched} re-read from conversations)" if fetched else ""))
    if missing:
        print(f"no patch for {len(missing)}: {', '.join(missing)}")
    print(f"\nnext:\n  pip install swebench\n  swebench eval verified -p {args.out} --run-id <id>")


if __name__ == "__main__":
    main()
