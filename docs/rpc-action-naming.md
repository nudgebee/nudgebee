# RPC action naming convention — verb taxonomy

> Extracted from [`CLAUDE.md`](../CLAUDE.md) § "RPC action naming convention". The naming pattern (`<module>_<verb>_<description>_[<version>]`) and module/version rules live there. This file is the full verb taxonomy.

Pick the verb that matches the operation's *intent and return shape*, not the verb that "sounds close." When two verbs both fit, prefer the more specific one.

**Read operations**

| Verb | Use when | Returns | Example |
|---|---|---|---|
| `get` | Fetch **one** record by id or unique key | single object or 404 | `users_get_current`, `recommendations_get` |
| `list` | Fetch a **collection** (with filters / pagination / ordering, including relevance ordering) | array | `accounts_list`, `insights_list` |
| `aggregate` | Group / sum / count / bucket | aggregation object | `accounts_aggregate` |
| `count` | Just a number | int | `alerts_count` |
| `check` | Validate / probe, returns yes/no/status | bool or status enum | `clusters_check_health`, `usergroups_check_name_exists` |

**Write operations**

| Verb | Use when | Example |
|---|---|---|
| `create` | Insert a new record | `agents_create_token` |
| `update` | Modify an existing record | `tickets_update_status` |
| `upsert` | Insert-or-update | `settings_upsert` |
| `delete` | Remove a record | `agents_delete` |
| `apply` | Execute already-prepared changes (i.e. caller has the diff/plan in hand) | `recommendations_apply` |

> **Decision tree — `create` vs `update` vs `upsert` vs `apply`:**
> - **Caller knows the record doesn't exist** → `create`. Fails if duplicate.
> - **Caller knows the record exists** → `update`. Fails if missing.
> - **Caller is indifferent / idempotent** → `upsert`. Always succeeds if input is valid.
> - **Caller has a pre-computed diff/plan and the server just executes it** → `apply` (e.g. `recommendations_apply` takes a ready-made set of changes). `apply` is closer to `execute` than to `update` — the server's job is to enact, not to compute.
> - **Status / flag change** → `update` (see status-change note below). Don't reach for `apply` just because the input looks like a state transition.

> **`convert` is a one-off, not a general verb.** Used only for `applications_convert_profile` (transforms a profile payload between two representations — no record is created, updated, or deleted; the operation is a pure transformation that returns the converted form). Don't generalize: a `convert` that writes a new record is `create`; a `convert` that mutates an existing record in place is `update`; a `convert` that exchanges format for client display only is `get_*_as_<format>`. If you find yourself wanting a second `convert`, audit whether `create` / `update` / `get` would fit before adding it.

**Action / job operations**

| Verb | Use when | Example |
|---|---|---|
| `execute` | Run a defined operation (sync or async) | `anomaly_execute`, `playbook_execute` |
| `replay` | Re-run an existing prior execution (lineage/state from the original matters) | `workflow_replay_execution` |
| `cancel` | Halt a long-running operation gracefully (allows cleanup) | `ai_cancel_investigation`, `workflow_cancel_execution` |
| `pause` / `resume` | State-machine transitions on a running operation (preferred over `update_state` when the transition has side-effects like checkpointing/freezing schedules) | `workflow_pause`, `workflow_resume` |
| `publish` / `unpublish` | Versioning + rollout operations (different from a simple field update because rollout semantics apply) | `workflow_publish_version` |
| `sync` | Trigger an external-data sync | `accounts_sync` |
| `generate` | Compute & return a fresh artifact (recommendation, token, report) | `ml_generate_node_recommendations` |
| `enable` / `disable` | Toggle a boolean flag (no state-machine complexity) | `integrations_enable` |

> **Note on messaging/notification dispatch:** There is no `send` verb. For programmatic delivery use `execute` (`notifications_execute_alert`). For rollout-style fan-out use `publish`. Test/verify-the-setup actions use `check` (`notifications_check_connection`).

> **Note on status / flag changes:** Don't invent verbs for state transitions on a flag field (`activate`/`deactivate`/`toggle`/`approve`/`reject`/`acknowledge`/`snooze`). Use `update` — the action name describes the field being updated, not the direction. Examples: `events_update_resolution`, `events_update_classification`, `events_update_rule_override`, `tenant_update_registration_status`. Reserve `pause`/`resume`/`cancel` for state-machine transitions on a *running operation* (where the transition has side-effects like checkpointing) and `enable`/`disable` only when there's a long-standing convention on the resource (e.g. `integrations_enable`).

> **Note on integration onboarding URLs:** Cloud-integration handlers that return a deep-link URL for the user to complete onboarding in the provider console (CloudFormation, ARM template, GCP Deployment Manager) are `get` actions, not `create`. Name them `<provider>_get_onboard_<service>_url` (e.g. `aws_get_onboard_eventbridge_url`, `azure_get_onboard_eventgrid_url`). The `onboard` qualifier disambiguates the URL's purpose; `get` reflects that no persistent record is created server-side — the actual integration record gets created by a separate callback (`*_create_*`) once the user completes the provider-side flow.

> **Note on tenant-scope and cross-tenant ops.** Tenant scope is intrinsic to every action (see [[project_auth_model]]). NB has **no super-admin UI that does cross-tenant operations** — super-admin in UI just unlocks elevated *within-tenant* permissions (e.g. `LLMConsumptionTab` budget toggle). The genuinely cross-tenant operations all live **server-side**: NextAuth callbacks, `/api/auth/signup*`, `/api/auth/token`, and SAML use a synthetic admin context to look up users by email before login, route domains to tenants, etc.
>
> The `admin_*` module convention introduced in PR #31324 was based on the wrong premise (it assumed a super-admin cross-tenant UI exists). The only `admin_*` action that shipped (`admin_list_tenants`) had zero UI callers after the SwitchTenant.jsx fix in PR #31372 — deleted. **Don't introduce `admin_*` actions.** When NB eventually formalizes the "server-side-only cross-tenant" surface, the right convention will be either a dedicated `internal_*` module or a separate backend mount (`/internal/*`) — see [[project_action_rename_followups]] item 6a for the open design question.

**Avoid**

- `trigger_*` — redundant with `execute` / `sync`; pick one.
- `fetch_*` — use `get` or `list`.
- `find_*`, `search_*` — use `get` (by id) or `list` (collection, including relevance-ordered).
- `do_*`, `handle_*`, `process_*` — too vague to convey intent.
- Bare `query` as a verb — that's the transport, not the operation. Be specific: `list` or `aggregate`.
- `test_*`, `validate_*` — use `check` (umbrella verb for probe/validate/test operations).
- `save_*` — use `create` (write-only) or `upsert` (idempotent write). If the operation marks/bookmarks a record by inserting into a side table, name the action after the side table: `<module>_create_<side_table>` (e.g. `ai_create_saved_conversation`, not `ai_save_conversation`).
- `onboard_*` / `*_onboard` — use `create` (for integration onboarding) or `register` (for self-service signup).
- `map_*` / `unmap_*`, `link_*` / `unlink_*` — model the mapping as a first-class resource and use `create` / `delete` on it (e.g. `ai_create_kb_mapping`, `ai_delete_kb_mapping`).
- `stop_*` — use `cancel`.

**Examples (good):**
- `ai_get_tools`
- `accounts_list`
- `runbooks_create_playbook`
- `accounts_sync`
- `workflows_delete_schedule`
- `ai_get_tools_v2` (only because `ai_get_tools` still exists)

**Hasura-style table queries (carve-out):** A subset of read operations are named `<module>_<entity>_v[N]` (e.g. `k8s_pods_v2`, `cloud_resource_v2`) and predate this convention. They are internally consistent and **not** renamed in bulk. New table queries should follow the verb taxonomy above (`<module>_list`, `<module>_aggregate`, etc.).
