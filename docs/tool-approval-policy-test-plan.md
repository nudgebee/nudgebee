# Tool approval policy test plan

Status: matcher, API and executor checks are automated unit tests; the DAO was checked against a scratch Postgres 16; on dev, auto-approval, a non-matching write and the flag turned off were checked by hand (evidence in PR #38827). The other runtime scenarios below are manual QA.

This plan tests the property the feature is meant to provide: a matching rule may waive interactive confirmation for one invocation only after ordinary authorization succeeds. It must never grant tool, account, target, or command authority.

## Test environments

Use three levels and keep their evidence distinct:

1. **PR/local:** matcher, target resolution, executor ordering, race tests, UI type/lint checks, migration validation, and database-backed API tests.
2. **Disposable local stack:** Postgres migrations plus llm-server/API/app, with `LLM_SERVER_TOOL_APPROVAL_POLICY_ENABLED` tested both off and on.
3. **Dev runtime:** a non-production tenant, a disposable Kubernetes namespace and object, at least two users with different target permissions, and preferably two llm-server replicas.

Do not use an existing workload for mutation tests. Create a namespace such as `tool-approval-e2e-<run-id>` and a ConfigMap named `approval-probe`; delete the namespace after evidence is captured.

## Gate 1: deterministic contract tests

Add table-driven tests beside `security/toolapproval/matcher_test.go` for:

- exact token matching, quoting equivalence, changed argument order, appended flags, extra tokens, newlines, and working-directory changes;
- chains (`&&`, `||`, `;`, `|`, `&`) approved only when every command matches; command substitutions and subshells always ask; redirects such as `2>&1` stay part of their command;
- typed argument equality and full-match regex behavior, including missing paths, type mismatches, malformed JSON, invalid patterns, oversized input, and command-like argument fields;
- tool, account, configuration ID, enabled state, and unresolved-target mismatches;
- the 50-rule tool limit and 200-rule tenant limit, including the 51st enabled row failing closed before an earlier row can match.

Add executor/target tests proving:

- a saved configuration produces its stable backing ID whether the source populates `ToolConfig.Id` or the `values[id]` field;
- explicit `cluster`, `--context`, cloud-account, region/profile, or equivalent per-call selectors bind the decision to the selected target rather than the conversation default;
- a target-resolution or access-check error occurs before policy lookup and never writes a decision;
- a policy match remains invocation-local and does not populate or reuse the manual tool-confirmation map;
- matched and nonmatched parallel tool calls remain independent;
- retrying the same invocation identity produces one durable decision row.

Required commands:

```bash
cd llm/llm-server
go test -race ./security/toolapproval ./agents/core ./api ./tools
golangci-lint run

cd app
npm run lint2

cd api-server/migrations
atlas migrate validate -c file://atlas.hcl --env default
```

Repository-wide `make validate` remains required. Environment-dependent live-provider failures must be recorded separately and may not be presented as a passing run.

## Gate 2: database and API integration

Run against a fresh migrated Postgres database. Add integration coverage for the actions: list, create (multi-account), upsert, delete, check-match, list-options, and list-tools.

Verify:

- tenant admin can create, update, disable, and delete; tenant read-admin can list and preview but cannot mutate; an ordinary/custom-role user cannot read or mutate;
- tenant and actor identity come from the authenticated request, and an account outside the tenant is rejected;
- tool aliases are canonicalized and unknown tools, non-configurable config IDs, missing required config IDs, and inaccessible config IDs are rejected;
- preview and save perform the same identity and matcher validation;
- `toolapproval_create` for several accounts writes every rule and one ledger row per rule in one transaction; one bad account (other tenant, unavailable tool, invalid target, duplicate name) saves nothing;
- a duplicate name for the same account returns a 400 naming the account, not a 500;
- omitting `conditions` stores `[]`, not JSON `null`;
- stale revisions return conflict for update and delete;
- concurrent creates cannot exceed the tenant/tool limits;
- every successful mutation commits exactly one `llm_tool_approval_rule_events` row with actor and revision, without raw command or condition values;
- forcing the mutation-ledger insert to fail rolls back the rule mutation;
- `RecordDecision` fails if the rule is disabled, deleted, or revised between lookup and insert;
- a retry of the same `(conversation, message, agent, tool-call, fingerprint, rule, revision)` is idempotent, while a changed input or revision creates a distinct decision.

Inspect both tables directly during this gate:

```sql
SELECT action, rule_id, rule_revision, actor_id, tool_name, tool_config_id, created_at
FROM public.llm_tool_approval_rule_events
ORDER BY created_at DESC;

SELECT rule_id, rule_revision, tool_name, tool_config_id, input_fingerprint,
       conversation_id, message_id, agent_id, tool_call_id, created_at
FROM public.llm_tool_approval_decisions
ORDER BY created_at DESC;
```

## Gate 3: UI and routing

Automate the Security screen in the existing Nubi/admin E2E suite:

- the old `#ai-tools/egress-filter` route opens Security → Egress Filter;
- `#ai-tools/egress-filter/tool-approvals` opens Tool Approvals for tenant admin and read-admin roles;
- an egress-only custom role does not see the Tool Approvals subsection;
- read-admin can inspect and preview but cannot add, toggle, edit, or delete;
- create supports selecting several accounts or Select all, creates one account-bound rule per account, rejects per-account name collisions before saving, and keeps existing-rule edits single-account;
- the searchable tool selector lists only enabled execution tools shared by every selected account and groups them by direct, command-derived, or absent configuration requirements;
- configured tools show named execution-target choices instead of accepting pasted IDs, auto-select a sole choice, and require a separate valid target for each selected account;
- shell commands refresh target choices from the command owner (for example Kubernetes or AWS), while local-only commands do not request a target;
- argument conditions can be added and removed as field/operator/typed-value rows; text, number, boolean, JSON equality, and full-value regex survive an edit round trip without changing type;
- create, edit, conflict, disable, and delete states update without losing the selected account or target;
- shell selection hides command regex and requires a working directory;
- invalid tool/target combinations show the server error and cannot be saved;
- preview displays match and nonmatch reasons from the server;
- Egress Filter controls and global-search/deep-link behavior remain unchanged.

Run the selector impact check and the affected Nubi E2E spec. Capture one screenshot each for the rule list, exact-command editor, preview result, and read-only view.

## Local end-to-end scenarios

Run these on the local stack before Gate 4. Each targets a path the unit tests cannot reach. The "Risk" column says what a failure would look like, so an unexpected result can be recognised rather than explained away.

### Rule shape and matching in real chats

| #   | Scenario                                                                                    | Expected                                                                                                  | Risk being probed                                                                                                                        |
| --- | ------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| L1  | Shell exact rule with `working_dir`; ask Nubi to run the command without naming a directory | Record whether the model sends `work_dir`. If it omits it, the rule does not match and the prompt appears | `work_dir` is optional in the shell schema but required on shell rules and compared literally, so shell rules may never fire in practice |
| L2  | Same rule; ask Nubi to run it "in `<working_dir>`"                                          | Waived; one decision row                                                                                  | Baseline that exact matching works end to end                                                                                            |
| L3  | `kubectl_execute` exact rule, command reordered (`-n x get pods` vs `get pods -n x`)        | Prompt                                                                                                    | Token order is significant by design                                                                                                     |
| L4  | Whole-tool rule on a tool with one saved configuration                                      | Waived                                                                                                    | Single-config target resolution (`ListToolConfigs` returns one)                                                                          |
| L5  | Whole-tool rule on a tool with two configurations and none selected in the conversation     | Prompt                                                                                                    | Unresolved target must never match                                                                                                       |
| L6  | Arguments rule; model passes an extra nested `options.command`                              | Prompt                                                                                                    | Nested executable-field rejection                                                                                                        |
| L7  | Rule created, then edited (revision 2); call again                                          | Decision row carries revision 2                                                                           | Revision is read at match time                                                                                                           |

### Accounts and targets

| #   | Scenario                                                                                                 | Expected                                          | Risk being probed                                                                                                                                 |
| --- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| L8 | Conversation in account A; kubectl call targets cluster B with `cluster=`; rule exists on account A only | Prompt | Rules match on the account the call runs on (B), not the conversation account |
| L9 | Same call; rule on account B, or an all-accounts rule | Waived; decision row records account B and its target | Target-account matching and all-accounts rules |
| L10 | User with read-only access to cluster B                                                                  | Denied before any prompt; no decision row         | Authorization runs before policy                                                                                                                  |
| L10a | All-accounts rule, then a new account is added; call on the new account | Waived | All-accounts rules cover accounts added later — the accepted trade-off |
| L11 | Rule created for accounts A and B in one save, then A deleted                                            | B still waives; A prompts                         | Per-account copies are independent                                                                                                                |
| L12 | Cloud account deleted                                                                                    | Its rules, ledger rows and decision rows are gone | Cascade deletes audit history — confirm this is acceptable                                                                                        |

### Conversation flow and resume

| #   | Scenario                                                                                                    | Expected                                                                                     | Risk being probed                                                                                                                                                                                                                                                                             |
| --- | ----------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| L13 | One turn with two writes: A matches a rule, B does not; approve B                                           | A runs exactly once, B runs after approval; neither reports "configuration was not resolved" | On resume, a call with no stored result is re-run only when a config was just resolved or a manual confirmation exists. Policy approvals are deliberately not stored as confirmations, so a waived call that paused before running can take the fail-fast "configuration not resolved" branch |
| L14 | Matched call on a tool that then asks for configuration selection; pick the configuration                   | Runs once after selection; decision rows show whether the resume re-evaluated the policy     | Re-evaluation after a follow-up                                                                                                                                                                                                                                                               |
| L15 | Reject a manual prompt for a call, then create a matching rule and have Nubi retry in the same conversation | Rejection stays terminal for that call                                                       | Manual rejection wins over a later rule                                                                                                                                                                                                                                                       |
| L16 | Rule disabled while a conversation is paused on another follow-up; resume                                   | The waived-but-not-run call now prompts                                                      | Revocation applies on resume                                                                                                                                                                                                                                                                  |
| L17 | Same prompt via a delegated sub-agent and via the ReAct4 planner (`@k8s_orchestrator_react4`)               | Same outcome as the primary planner                                                          | Every planner reaches `doAction`, the single confirmation point — verify, don't assume                                                                                                                                                                                                        |

### Unattended and non-chat entry points

| #   | Scenario                                                                       | Expected                                                                                                                      | Risk being probed                                                  |
| --- | ------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| L18 | Automatic alert investigation (read-only run) that proposes a write matching an enabled rule | Refused as a write and listed under "Not checked (needs write access)"; no decision row | Read-only refusal runs before the policy lookup, so a rule can never permit a write in a run that must not change anything |
| L19 | Slack/Teams chat issuing a matching write                                      | Same behavior as web chat                                                                                                     | Messaging-platform path                                            |
| L20 | Automation with `ai_auto_approve` and a matching rule                          | No prompt either way; no decision row (the tool exemption short-circuits first)                                               | Two waiver mechanisms interacting                                  |

### Flag, admin and failure handling

| #   | Scenario                                                                                  | Expected                                            | Risk being probed                   |
| --- | ----------------------------------------------------------------------------------------- | --------------------------------------------------- | ----------------------------------- |
| L21 | Flag off, rule present                                                                    | Prompt; no decision row; admin tab still usable     | Flag-off is inert at runtime        |
| L22 | Stop Postgres writes to `llm_tool_approval_decisions` (for example revoke INSERT locally) | Prompt                                              | Decision-write failure fails closed |
| L23 | Two browser tabs edit the same rule                                                       | Second save gets a conflict                         | Optimistic revision check           |
| L24 | Tenant read-admin opens the tab                                                           | Can list and preview; cannot create, edit or delete | Role split                          |

## Gate 4: dev runtime scenarios

Start with the feature flag off and prepare a disabled exact shell rule for a disposable command such as:

```text
kubectl annotate configmap approval-probe nudgebee.test/run=allowed --overwrite -n tool-approval-e2e-<run-id>
```

Bind it to the selected cluster configuration ID and the exact workspace directory used by execution. Then execute the following scenarios with a fresh conversation/tool-call identity for each row.

| Scenario                                         | Expected interaction                               | Expected execution                           | Required evidence                                                                       |
| ------------------------------------------------ | -------------------------------------------------- | -------------------------------------------- | --------------------------------------------------------------------------------------- |
| Feature flag off, matching rule present          | Confirmation prompt                                | Only after manual approval                   | No policy decision row                                                                  |
| Flag on, enabled exact match                     | No confirmation prompt                             | Command runs once                            | Decision row and `CHAT_TOOL_POLICY_APPROVE` with the same rule revision and fingerprint |
| One argument changed                             | Confirmation prompt                                | Only after manual approval                   | No policy decision for the changed input                                                |
| Appended command/operator                        | Confirmation prompt                                | Only after manual approval                   | Matcher reason says manual approval is required                                         |
| Wrong working directory                          | Confirmation prompt                                | Only after manual approval                   | No policy decision                                                                      |
| Same command, different cluster/account          | Confirmation prompt                                | Only after authorization and manual approval | Resolved target ID differs; original rule does not match                                |
| User lacks write access to selected target       | Authorization failure before prompt/execution      | Does not run                                 | No policy decision or policy approval audit                                             |
| Rule disabled or deleted                         | Confirmation prompt                                | Only after manual approval                   | Subsequent lookup sees revocation                                                       |
| Rule edited after lookup, before decision insert | Confirmation prompt                                | Does not execute under stale revision        | Decision insert fails closed                                                            |
| Shared audit projection unavailable              | No prompt only if durable decision insert succeeds | May run                                      | Decision row exists; projection failure is logged                                       |
| Decision-table write unavailable                 | Confirmation prompt                                | Does not run under policy                    | No unaudited execution                                                                  |
| Two parallel calls, one matching                 | Prompt only for nonmatch                           | Matching call only bypasses                  | Separate tool-call IDs and decision evidence                                            |
| Retry same tool-call identity                    | No duplicate approval record                       | No duplicate policy decision                 | Unique constraint leaves one decision row                                               |

Repeat the matching, nonmatching, manual rejection, and manual approval paths under every planner/runtime path that can execute these tools. At minimum cover the primary planner and its fallback/resume path. A manual rejection must remain terminal even when a rule would otherwise match.

For a two-replica environment, create on replica A, match through replica B, then disable/delete through A and issue a new invocation through B. The new call must prompt. Restart one replica and repeat a matching invocation to prove decisions depend on Postgres state rather than process memory.

## Evidence bundle

Record one run ID and retain:

- commit SHA, migration revision, feature-flag value, tenant/account/user IDs, tool name, resolved configuration ID, and planner path;
- request/conversation/message/agent/tool-call IDs for each scenario;
- sanitized UI screenshots and relevant llm-server logs;
- mutation-event and decision-row query output with fingerprints but no raw tool input or credentials;
- the resulting disposable object state showing whether execution occurred;
- cleanup confirmation for rules and the disposable namespace.

## Release gate and rollout

The feature is ready for a disabled-by-default merge when Gates 1–3 pass. It is ready to enable for an internal tenant only after Gate 4 passes with durable decision and authorization-denial evidence.

For the initial canary, use an isolated environment because the current flag is process-wide rather than tenant-scoped. Monitor policy lookup failures, decision-write failures, shared-audit projection failures, approval counts by rule/tool, and unexpected confirmation changes. Disable the process flag to roll back runtime matching; prepared rules remain stored and normal confirmation behavior resumes.

Do not claim deployed validation until the running image SHA, applied migration, effective flag, and runtime evidence are all verified.
