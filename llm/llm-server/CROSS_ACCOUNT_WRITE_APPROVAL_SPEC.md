# Cross-account writes: approve for the account the write lands on

Part of #38588 ("Target-scoped write approval"). Status: `/challenge` REVISE → revised → PROCEED (2026-10-03). Access rule changed afterwards by the owner's decision (see Design 3).

## Problem

Nubi can now write on another cluster or account from a conversation (kubectl, helm, shell, aws/gcloud/az, postgres/redis/rabbit, vm). Approval was built for one account per conversation, and a map of the code (2026-10-03) found:

| # | Gap | Where |
|---|---|---|
| G1 | postgres/redis/rabbit approvals are keyed by tool name: one "yes" covers writes on **any** account or instance for the rest of the conversation | no `ConfirmationKey`; `executor_planner.go:2037-2044` falls back to the tool name |
| G2 | The approval card does not say which account. `FollowupRequest` has no target field; the text says "cluster resources" for every tool; postgres shows only the SQL; a delegated child's integration target never appears | `followup.go:50-101`, `FollowupSheet.jsx` |
| G3 | After approval, the call re-resolves its target **by name**. A connection renamed or replaced between approval and resume runs the approved write on a different account | `factory_agent.go:503-526`, `shell_target.go:597` |
| G4 | Write access is required on the **conversation's** account, not the one written to: a user with write on k8s-prod and read on k8s-dev cannot write on k8s-prod from a k8s-dev conversation | `auth_agent.go:173-180` |
| G5 | `vm_execute` writes on another account check only read access there | `tool_vm.go:48-56` |

Out of scope:
- Read/write classification (#39880).
- "Fix it on another cluster" for remediation/recommendations, which stay origin-bound (separate feature).
- Write scripts whose target the text does not name, which stay refused.
- Per-command rather than per-target approval granularity (unchanged: one approval covers that tool's writes on that account, as on the conversation's own account today).

## Design (revised after `/challenge` 2026-10-03: REVISE → pin, relaxation and UI fields dropped)

One executor function decides the key, the card text and the target check for every write, from the call's **resolved** target (the account id `ResolveToolCallTarget` / `perCallWriteTarget` / `shellWriteTarget` already compute), not per-tool string parsing.

1. **Key (G1, G3).** A write on another account has the key `tool@<resolved account id>` (the integration is named on the card but not keyed: it may be picked only after the approval, and one approval covers a tool's writes on an account, as on the conversation's own); per-tool `ConfirmationKey` only adds its operation part (vm resource, server command). kubectl, helm and the cloud CLIs move to this one scheme. A call on the conversation's own account keeps today's key. Resume already re-runs `doAction`, which recomputes the key from a fresh resolution, so a connection replaced between approval and resume gets a new key and is asked again (no separate pin, no change to the persisted `ToolConfirmations` map).
2. **Card (G2).** Every write's card names where it lands — another account and its integration, or the conversation's own account and the integration already chosen there (picked, or the only one); added after review on 2026-10-03, since an own-account database write also did not say which database. Live testing showed the approvals history panel hides the question line, so the destination is also stored as structured data on the approval (`target {provider, kind, name, integration}`, `crossAccount`) and shown there as a label. When several integrations exist, the user picks one before the approval, not after, so the card names it. The executor writes it into the confirmation question ("Run on Kubernetes cluster **k8s-prod** …"; postgres "on k8s-prod · prod"), replacing "cluster resources". 
3. **Access (G4, G5).** A write needs Create on the account it lands on. For a write on another account that **replaces** the conversation-account check (owner decision 2026-10-03: "as far as the user has access" to the target); reading the conversation's account is still required, as for being in the conversation at all. `vm_execute` gets the same target write check as the other target-following tools. A write on the conversation's own account is unchanged.

## Acceptance criteria (each proven by the named artifact)

| # | Scenario (from a k8s-dev conversation) | Expected | Proof |
|---|---|---|---|
| A1 | "Scale deployment X on k8s-prod to 2 replicas" | Card names **k8s-prod**; after Yes it runs on k8s-prod (ran-on chip), k8s-dev untouched | e2e natural question + `kubectl get` on both clusters |
| A2 | Approve a postgres write on k8s-prod, then a postgres write on k8s-dev `dev-pg` | Asked again | unit (key) |
| A3 | Approval given, then the k8s-prod connection is replaced before resume | Not executed; asked again | unit (key recomputed on resume) |
| A4 | User with write on k8s-prod, read only on k8s-dev | Write on k8s-prod allowed; write on k8s-dev refused | unit with scoped security context |
| A4b | User with read only on k8s-prod | Write on k8s-prod refused before the card | unit with scoped security context |
| A5 | `vm_execute` write on a VM account the user can only read | Refused | unit |
| A6 | Conversation's own account, any tool | Same key and checks as today; the card now names the account (and integration when chosen) | unit (own-account card) + existing tests |

## Rollout

One PR in llm-server, which also moves the `postgres` agent to follow the target like its tool (#39906 missed it). Keys and card text only add prompts. The access rule (A4) relaxes the conversation-account requirement for writes that land elsewhere, so it ships behind `llm_server_target_write_access` (default on; turn off to restore the old rule). Deferred: per-command approval for prod-like accounts.
