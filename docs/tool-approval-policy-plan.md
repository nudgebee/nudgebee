# Tool approval policies

Status: implemented, self-reviewed after rebase, and locally validated in the dedicated worktree; runtime testing is pending. Rebased onto origin/main 530be5bb1f on 2026-09-20.
Dedicated worktree: `/Users/shiv/Workspace/nudgebee-tool-approval-policy`, branch `codex/tool-approval-policy`.

## Outcome

Administrators can define rules that automatically approve matching tool invocations. Rules waive the interactive confirmation only. Account permissions, tool availability, capability restrictions, validation, and execution safeguards continue to apply. Each automatic approval is attributable to a stored rule and visible in audit history.

## UI

Recommend renaming AI & Tools → Egress Filter to Security, with Egress Filter and Tool Approvals subsections. The supplied screenshot shows Egress Filter, not ingress. Preserve the existing egress deep link with an alias; update global search and reconcile e2e selectors.

Do not inherit the existing `egressfilter` permission for the entire Security container. Each subsection retains its own backend-enforced permission. Proposed initial ownership: tenant administrators manage approval rules, with explicit account selection; ordinary users cannot grant themselves automatic approval.

The rules screen provides name, enabled state, account scope, tool selector, optional argument conditions, and a sample-input match preview. Show tool-wide rules explicitly as “All arguments”. Support add, edit, disable, delete, and audit links. Default to exact matching; regex is an explicit advanced choice with a full-match explanation. The server performs preview and save validation using the same matcher.

## Rule contract

- Server-derived tenant identity; explicit account scope, stable rule ID, revision, creator/updater and timestamps.
- Configurable tools are additionally bound to the resolved configuration ID. A configured invocation cannot match an unbound rule, and an unresolved target never receives automatic approval. Shell command rules require an explicit working directory.
- Canonical tool identity with exact or regex matching for non-shell command tools. `shell_execute` supports exact simple commands only in v1; raw shell regex is rejected. Bind dynamic/MCP tools to their integration identity to avoid approving an unrelated tool with the same display name. Client-provided tool identities require an explicit trust decision before inclusion in v1.
- Optional argument conditions identify a JSON field path, operator (`equals` or `regex`), and value. Conditions within one rule are AND; matching enabled rules are OR. Whole-tool grants require an explicit choice.
- Match decoded fields, not serialized JSON or an LLM interpretation. Missing fields, incompatible types, malformed JSON and invalid patterns never match. Exact comparisons preserve JSON types; regex applies to string values. Raw-string tools need an explicit whole-input target.
- Regex uses Go's bounded-time regexp engine, full-value matching, and limits on pattern size, input size, condition count and rule count. No automatic whitespace rewriting of command arguments.
- Match final rewritten execution input. Unconstrained fields are intentionally unrestricted and the editor must make this visible. A broad command regex grants every command it matches, including compounds; it is not a shell safety classifier.
- Empty rules and disabled rollout preserve current behavior. Lookup failure does not auto-approve: use the normal confirmation path. Revocation must be visible across replicas before a later approval decision; prefer fresh reads initially over introducing a stale cache.

## Current implementation seams

### Command-aware rules (user clarification)

Commands are a first-class policy subject, not merely an arbitrary string argument. Distinguish “allow this tool”, “allow this exact command”, and “allow this command with constrained arguments”. The latter two retain the tool/execution-channel and account constraints; matching a command never grants access to another execution channel implicitly.

For example, allow `kubectl rollout restart deployment/api -n staging` for the selected cluster. This must not also approve another namespace, another deployment, `--context production`, or an appended `&& kubectl delete ...`. A rule for a command family needs explicit variable positions (such as a deployment name regex), permitted flags, and target constraints; unknown extra flags do not silently pass.

The initial command implementation should support exact, single simple commands, plus structured argument matching only for command forms whose parsing is authoritative. Compare executable and parsed argument boundaries without discarding execution-affecting flags, environment, working directory or target selection. Quoting equivalence is acceptable only where parsing proves equivalence. Resolve omitted target defaults or require explicit targets; an unchanged command string can otherwise act on a different cluster or directory. Account scope alone does not prove a command's target.

Reject command-policy matching for unsupported shell syntax: chains (`&&`, `||`, `;`), pipelines, substitutions, redirects, wrappers (`sh -c`, `sudo`, `env`), scripts/interpreters, or expansion whose meaning is not resolved. These continue through the existing approval behavior. Do not approve a compound expression because its first command or one segment matches. Later compound support must account for every executable segment and shell side effect, not just tokenize on spaces or split on semicolons. Regex may constrain supported argument values; unrestricted regex over raw shell text is deferred for command rules. Explicit tool-wide grants remain visibly broader and are a separate rule type.

Existing `tools/tool_shell_gate.go` routes shell CLI classification through `ShellWrappable` and handles known compounds; `tools/tool_cli_static_classifier.go` supplies operation classifiers. Reuse established command extraction/identity where valid, but these classifiers (including their LLM fallback and wrapper stripping) are not proof that a command matches an approval grant. Policy matching must be deterministic and must retain context that classification can ignore. Verify direct CLI tools and `shell_execute` with the same command corpus, while retaining explicit channel scope.

Add acceptance cases for quoted arguments, escaped separators, repeated/overriding flags, namespace/context/region/profile changes, environment assignments, working directory changes, substitutions, pipelines, redirects, scripts, and supported versus unsupported command syntax. The preview explains which command/argument/target matched, or why a command cannot receive automatic approval.

This clarification supersedes generic raw-string/regex matching above for executable command fields. Non-command tool arguments retain the general structured matcher. Before coding, finalize the supported command grammar and target-resolution contract; do not generalize a shell classifier into an authorization parser.

- `llm/llm-server/agents/core/auth_agent.go`: `IsAgentToolAuthorizedToProcessRequest` resolves authorized tools, infers operation type and checks write access. Do not add an allowlist early return here.
- `llm/llm-server/agents/core/executor_planner.go`: `doAction` rewrites input, checks authorization, then processes write confirmation. Insert policy evaluation in this confirmation decision after honoring explicit rejection and before generating a followup. Preserve subsequent config and schema checks.
- `confirmationKeyForAction` and `resumeConfirmationKey` support per-action confirmations, with legacy tool-name scope. Never insert a policy match into the legacy tool-name confirmation map: it would approve later nonmatching calls.
- `writeConfirmationRequired` already recognizes workflow-author exemptions via `ToolConfirmationExempt`; keep those semantics separate from rule matches.
- `followup.go`: `PendingFollowupInfo` provides manual chat-action audit context. An automatic decision has no pending user followup, so it needs a direct audit producer. Existing general-purpose automatic-approval audit storage was not established by this inspection; trace the audit writer/schema before choosing reuse versus extension.
- `app/src/components/llm/admin/aiToolsConfig.js` owns the existing tab; `app/src/lib/navSearchPages.ts` owns search entries.

## Decision ordering and lifecycle

1. Resolve the tool and final input; enforce existing authorization.
2. Determine whether this invocation needs confirmation under current behavior.
3. Honor explicit rejection; preserve valid existing manual confirmation semantics.
4. If still awaiting a decision, evaluate current applicable rules for this invocation only.
5. Persist an idempotent matched-rule decision before execution; otherwise generate the ordinary confirmation followup. If decision persistence fails, do not execute using the rule exemption. The decision row is the canonical audit record; the shared audit stream is a projection emitted only by the first insert.
6. Continue configuration, validation and execution. Approval does not imply execution success.

Keep already-pending prompts on the existing resume flow in v1; editing rules does not itself resume conversations. Recheck rules for new invocation decisions, including retries. Verify both ReAct4 and ReAct3 fallback, delegated tools, parallel calls, and resumed conversations. Input changes after a decision require re-evaluation. Cache hits that return prior results are not new executions or new automatic approvals.

## Audit

Record decision source `policy_rule`, rule ID/revision, tenant/account, requesting user, conversation/message/agent/tool-call IDs, canonical tool identity, timestamp, and a redacted argument summary plus input fingerprint. Attribute the decision to policy, not a fabricated user “yes”. Correlate subsequent execution outcome separately. Use the invocation identity for idempotency across retries and replicas. Record policy CRUD changes, including actor and revision, in a metadata-only ledger inside the same transaction as each mutation; the shared audit stream is a secondary projection. Avoid raw secrets in both rules and audit payloads; discourage matching secret-bearing fields.

## Adversarial pass

Verdict: PROCEED with this design for implementation planning.

1. **An allowlist inside auth becomes privilege escalation.** Resolve by applying it only to confirmation after access checks; prove denied users remain denied.
2. **Regex or tool-name approval silently widens authority.** Resolve with explicit full-value semantics, structured conditions, integration identity, visible scope, and invocation-only decisions. Do not advertise broad regex as safe command parsing.
3. **Revocation/resume/audit diverge across replicas.** Resolve with fresh policy decisions, immutable revision attribution, durable audit before exemption, and lifecycle tests. Do not reuse conversation-wide confirmation entries for matches.

## Delivery

### Stage 1: complete admin-managed capability

Implement persistence/migration, backend CRUD and match preview, permission gates, shared matcher, executor integration, durable audit, and Security UI together as one coherent feature. Ship disabled by default with a rollout gate. Read service guidance, architecture decisions, RPC naming and design-system conventions before implementation. Finalize audit reuse and dynamic/client-tool scope during the initial contract pass.

Acceptance evidence:

- Matching invocation runs without a prompt and exposes rule ID/revision in audit.
- Nonmatching invocation waits for approval; rejecting it prevents execution.
- Matching rule never bypasses account denial, unavailable tools or capability restrictions.
- Wrong tenant/account, disabled/deleted rule, invalid input, missing fields and policy lookup failure do not receive policy approval.
- Exact and regex previews agree with execution; appended commands, newline variants, changed targets and additional unconstrained arguments have explicit tested semantics.
- Two parallel calls with different arguments are evaluated independently; a match does not approve the next nonmatching call.
- Manual approve/reject and configuration prompts still resume correctly under both planners; replica restart/retry does not duplicate decision audit or execution.
- Rule edits/deletes take effect for subsequent decisions across replicas; audit failure cannot cause unaudited policy execution.
- Security preserves Egress Filter behavior, permission visibility, old deep links and global search access.

Run affected service validation and UI/e2e impact checks. Demonstrate the matching, nonmatching, forbidden and revoked flows in a local/dev environment with audit evidence before claiming runtime validation.

The executable test sequence, runtime matrix, evidence bundle, and rollout gate are defined in [`tool-approval-policy-test-plan.md`](./tool-approval-policy-test-plan.md).

### Stage 2: “Always allow this command”

Add an option to the existing approval card for users authorized to manage rules. Show exact tool, arguments and account scope before saving. Create an exact rule through the same API, then resume the current invocation through the normal approval lifecycle. Do not infer a regex, grant tool-wide scope, or create a second policy store. Preserve ordinary one-time approval for other users.

## Proposed defaults to confirm during implementation scoping

Tenant-admin-managed rules, explicit account scope, exact matching by default, optional regex, and no personal rule layer in v1. Decide whether integration-bound MCP tools belong in the first delivery after tracing their identity contract; client-side execution needs separate treatment. This plan does not change existing tool classification or legacy manual confirmation scope.
