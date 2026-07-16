# Verification-Loop Redesign — accurate results, no wasted tokens

Status: PLANNED (phases 0–3, flag-gated, local-tested before any deploy)
Companion: `token-cost-optimization-spec.md` (cache/context work — W1–W3). This spec covers
*verification correctness*; the two are independent and composable.

## 1. Incident evidence (why)

Session `C05JKEDPJKH-1784179549.889199` (dev, 2026-07-16): a fix-mode run ("resolve git
conflict markers in 4 files") burned **1.18M + 855K input tokens** across two analyses, with a
healthy per-call profile (8–35K prompts, 71% implicit cache). The waste was **call volume from a
verification doom loop**, reconstructed from workspace-pod logs + `llm_conversation_tool_calls`:

1. Fixer guessed the build dir: `cd api-server && go build ./...` — the Go module lives at
   `api-server/services`, so this fails always ("directory prefix . does not contain main
   module"). The correct command was already computed by `findModuleRoots`
   (tools/repo_index.go) but only travels in the **repo_clone observation** — the fixer has no
   clone tool and never sees it.
2. The fix *worked*: a clean 1m39s `go build ./... 2>&1 | grep -v "go: downloading"` produced
   zero output → grep exit 1 → `isSearchNoMatch` (tools/cli_tool.go) reported
   *"Exit Code: 1 … Command succeeded (no matches found)"*. Earlier, the same pipe made a real
   compile error look like exit-0 success. **Both verification signals inverted by one
   heuristic.**
3. `extractBuildVerificationResults` (agents/orchestrator_agent.go:932) synthesizes a verdict by
   regex-matching cli history: **any** failed build-pattern command in the attempt ⇒
   `overall_passed=false` — early wrong-dir probes doom the verdict even when the final build is
   clean.
4. Review hard gate (agents/code_review_agent.go:80–126) rejected with `requires_revert=true` —
   good edits thrown away; the next attempt re-derived everything (measured: near-identical
   token-for-token loops).
5. Rejection feedback carried **empty error text** (`steps[].error`/`output` read
   `inv.Error`/`inv.Output`, which are empty for cli failures; the real text lives in the
   Observation) — the fixer retried blind.
6. After 3 attempts: *"Max attempts reached — accepting final fix"* — unverified work shipped as
   success. The retries bought nothing; honesty was lost.

## 2. Reference research (what world-class agents do)

Studied: **aider** (local clone), **grok-build** (xAI, local clone), **Claude Code** (docs).
Full notes in the session memory `coding_agent_verification_design_research`. All three converge:

| Principle | aider | grok-build | Claude Code |
|---|---|---|---|
| Exit codes verbatim, never reinterpreted | run_cmd.py:83 | local_terminal.rs:120 | Bash trusted as-is |
| No regex-derived verdicts | harness runs lint/test itself (base_coder.py:1599) | model reads real output; no grading | hooks block per-turn w/ stderr |
| No auto-revert | auto-commit per edit; /undo is human+soft | zero code-revert paths | checkpoints are manual /rewind |
| Attempts exhausted → stop loudly | hard stop at 3 reflections, **no "accept anyway"** | budget guards, falls through to user | max-turns / max-budget; CI outer loop |
| Failure feedback rich + located | tree-sitter context, fuzzy "did you mean" | nearest-match + spill-file path | hook stderr verbatim |
| Repo knowledge = files/config, never guessed | --lint-cmd/--test-cmd + repomap | AGENTS.md discovery, stable prefix | CLAUDE.md only |
| Tier models by role | architect(strong)/editor(cheap, no re-exploration) | separate summarizer model | subagent model pinning |

Our failing parts (regex verdict, blind revert, accept-anyway, fresh-loop retries) exist in
**none** of them. Our good parts (repo_index ≈ repomap, BuildConfig ≈ --lint-cmd,
read-before-edit ≈ grok's gate) match industry but lack the plumbing/feedback halves.

## 3. Design rules (binding for all phases)

- **R1 Tools never lie.** Exit codes, output, truncation are faithful; heuristics may annotate,
  never overwrite.
- **R2 Verification is deterministic and harness-run.** One command, resolved from general repo
  data; verbatim output. The LLM interprets; it does not adjudicate, and neither does a regex.
- **R3 Iterate forward.** No blind revert. Workspace commits per attempt are the undo substrate.
- **R4 Honest terminal state.** `verified | unverified | failed` + evidence, end to end (analysis
  result → llm-server → PR body). Never fabricate success.
- **R5 No scenario content.** Everything data-driven from the repo (manifests, config,
  instruction files). No hardcoded paths, commands, or incident-specific prompt text.
- **R6 Flag-gated, default-off**, validated on the local replay set before dev rollout.

## 4. Phases

### Phase 0 — Environment truth (tools/, standalone)

**0.1 Per-stage pipeline exit codes** — `tools/cli_tool.go`
- Execute shell commands via `bash -c` capturing `PIPESTATUS` (fallback: current behavior when
  bash absent). Observation reports every stage's code:
  `exit codes: [go build=1, grep=0] (pipeline exit: 0)`.
- Tool status rule: success iff every stage exited 0, **except** grep-family stages with exit 1,
  which are annotated `(no matches)` — annotation only, honest codes always shown.
  `isSearchNoMatch` stops deciding success/failure; it only annotates.
- Tests: extend `tools/cli_nomatch_test.go` — failing-build-piped-to-grep must report failure;
  clean-build-piped-to-grep must report success with grep=1 annotated.

**0.2 Spill-don't-drop truncation** — `tools/cli_tool.go`, `tools/tracked_wrapper.go`
- When output exceeds the cap: head+tail excerpt + full output written to
  `<workspace>/.nb-tool-output/<step>.log`, path stated in the observation (grok pattern).
- Tests: unit test that the spill file exists and the observation names it.

### Phase 1 — Deterministic verification, honest results (agents/)

**1.1 Harness-run verifier** — new `agents/fix_verifier.go`
- After the fixer produces a diff, the **orchestrator** runs verification:
  1. `sessionCtx.BuildConfig` commands if provided (existing request field);
  2. else `RepoIndex.ModuleRoots` filtered to modules containing changed files (from git diff)
     → `cd <module> && <manifest build cmd>`;
  3. else → status `unverified` (no guessing).
- Runs via CLITool with config timeout (`verify_timeout`, default 300s); workspace pod gets a
  persistent `GOMODCACHE`/npm cache volume so cold builds don't eat the timeout.
- Result struct: `{status, command, exit_codes, output_tail, spill_path}`.
- Flag: `HARNESS_VERIFY` (default off → old behavior).

**1.2 Delete verdict synthesis + demote review to advisory**
- Remove `extractBuildVerificationResults` + the review Pass-0 hard gate. The reviewer receives
  the harness result as *input context*; its LLM findings become advisory (attached to the
  result/PR), not a rejection verdict. Only the harness verification result drives iteration.
- Feedback always embeds the verifier's verbatim `output_tail` — never struct fields that can be
  empty.

**1.3 Iterate-forward; revert becomes explicit**
- Drop `RequiresRevert` as a default rejection side effect. `git commit` the workspace after each
  fixer attempt (`nb-fix attempt N`) — additive history, aider-style. Revert only when review
  cites concrete evidence of harm, implemented as `git revert`/checkout of the specific commit,
  logged with the cited reason.

**1.4 Honest terminal status**
- `analysis_result.verification = {status, command, evidence}` (tri-state per R4).
- Replace "Max attempts reached — accepting final fix" with status `unverified` + evidence.
- llm-server `agent_code2.go` surfaces it; PR body gains a "Verification" section
  (✅ command+passed / ⚠ unverified: reason). UI/Slack render as-is.

### Phase 2 — One continuing fixer loop + session facts (planners/, agents/)

**2.1 Verification gate on submit (same mechanism as the grounding gate)**
- In fix mode, when the fixer calls `submit_analysis`, the planner triggers harness verification
  (1.1). On failure: **do not terminate** — inject the verbatim result as the next observation
  and continue in the *same* GenAISession (cache prefix + state preserved).
- Bounds: existing iteration ceiling + new per-analysis token budget
  (`fix_token_budget`, default ~400K input) → on exhaustion, terminate with `unverified` +
  evidence (R4). Existing diff-convergence guard (`RunMemory.RecordFixAttempt`) stays.
- The outer 3-attempt loop collapses: fixer(with in-loop verify) → advisory review → done. The
  ledger carry-over (#32926) remains as the recovery path for genuine loop restarts.
- Flag: `INLOOP_VERIFY` (default off; requires `HARNESS_VERIFY`).

**2.2 Session facts for every agent**
- Build `RepoIndex` once at clone; store on `SessionContext` (not only in a tool observation).
- Inject a stable "Repository facts" block (module roots + verify commands + instruction-file
  pointers) into specialist **and fixer** prompts via template var — placed in the static prefix
  region per W1 cache rules (byte-stable across calls).
- Fixer template's `{{.BuildConfig}}` branch extends to render ModuleRoots when BuildConfig is
  absent (replaces "find the project root" guesswork instruction).

### Phase 3 — Cost structure + duplicate protection (cross-service)

**3.1 Per-agent model tiering**
- code-analysis `config.go`: `LLM.AgentModels map[string]string` (router/fixer/review →
  cheaper tier; specialist stays on the strong model). Client resolution per agent at
  construction; absent key = single-model behavior (default).
- llm-server `ForwardedLLMConfig` gains optional `per_agent` overrides; resolved through the
  existing `ResolveLLMConfig` layers (env + DB) so tenants can tune it.
- Rationale = aider architect/editor: the fixer receives exact instructions and shouldn't pay
  reasoning-tier prices. Rollout only after Phase 1+2 are measured (a cheap fixer with today's
  broken feedback would just fail cheaper).

**3.2 Close the duplicate-dispatch gap** — llm-server `agents/agent_code2.go`
- Write the per-message failure guard on timeout/error paths (today: only success-ish paths →
  a poll timeout lets the k8s_orchestrator re-POST /analyze while the orphan keeps running).

**3.3 Usage-reporting accuracy** — llm-server + code-analysis
- `/analyze` response returns token usage **per model with call counts** (not one folded sum);
  llm-server inserts one `llm_conversation_token_usage` row per model. Fixes the long-context
  mispricing of the folded 1.18M row and makes "Reqs" meaningful.

## 5. Local test plan (before any commit/deploy)

Environment: local binary run, plain-path repo (no clone) — see memory
`code_analysis_agent_local_e2e` (`LLM_MODEL_NAME` in .env, `GIT_CONFIG_VALUE_0=all`,
`--repo /path/to/local/repo`).

1. **Unit** (per phase, part of `make validate`): cli PIPESTATUS matrix; spill file; verifier
   command resolution (BuildConfig / ModuleRoots∩diff / none); submit-gate continue-not-terminate;
   honest terminal status mapping.
2. **Synthetic doom-loop regression** (generalized from the incident, no scenario hardcoding):
   script seeds a throwaway local monorepo (nested Go module + one non-Go dir) with conflict
   markers in both, runs fix mode, asserts:
   - exactly **one** fixer ReAct loop (call_index never resets within the fix);
   - final status `verified`, verify command ran from the module dir;
   - zero reverts; per-call log total input below a budget assertion (e.g. <300K where the
     incident spent 1.18M).
3. **Replay suite**: re-run the 4-scenario replay set from the token-cost work (S1–S4) to prove
   no explore-mode regressions, plus this session replayed from `llm_conversation_tool_calls`
   params.
4. **Flag matrix**: each phase's flag off ⇒ byte-identical old behavior (golden-log diff on the
   synthetic repo).

Rollout after local pass: enable flags on the dev workspace pod only → watch fix-mode runs in
Loki (`LLM per-call token usage`, `analysis_complete`) → default-on → prod.

## 6. Explicit non-goals

- No new hard gates; no LLM-graded verdicts anywhere.
- No scenario text in prompts; no hardcoded repo paths/commands.
- No changes to explore/followup modes beyond shared tool fixes (Phase 0).
- No mid-history compaction changes here (that's W1/W2 in the token-cost spec).
