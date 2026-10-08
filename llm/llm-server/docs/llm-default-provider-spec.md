# LLM default provider: tenant default and account defaults

Status: agreed; /challenge PROCEED on the seventh run (2026-10-07) · Owner: hsundar-git

## Problem

An account's LLM is sometimes not stored anywhere. llm-server and rag-server work it out at request
time from how many configs the account has, or from which config is the tenant default. So a change
that looks unrelated changes what an account uses:

- An account works on its only config, which is not marked default. Adding a second config, also
  not marked, breaks it: two configs, no default, so it falls back to the platform LLM, or fails if
  there is none.
- An account with several unmarked configs uses whichever one is the tenant default. Ticking
  "Default LLM provider" on another config, perhaps while setting up a different account, moves it.
- New accounts are linked to the tenant default and marked default at creation, but only on the
  three code paths that run that hook.

## The model

A config belongs to the tenant. It can be scoped to specific accounts. A tenant and an account can
each have several active configs.

There are two defaults, both explicit:

| | Tenant default | Default for the selected accounts |
|---|---|---|
| Label | Use as the tenant default | Use as the default for the selected accounts |
| Shown | Always | Only when at least one account is selected |
| How many | At most one per tenant | At most one per account |
| Stored as | `integration_config_values` row `default_llm_provider = 'true'` on the config | `integrations_cloud_accounts.default_llm_provider = true` on the account's link |
| Used by | Every account without its own default, whether or not the config is linked to it | Only the selected accounts |

Every config is tenant-wide by default. Selecting accounts restricts it to those accounts. Scope only
decides which accounts can mark a config as their own default and see it in the model picker.

- **Removing every account from a restricted config** (editing it, with or without ENV) makes it
  tenant-wide. Popup F says so before saving. Popup D still blocks the save if an account would be
  left with no LLM.
- **Deleting an account** removes its links. A config left with no accounts because of that deletion
  is **disabled**, not turned tenant-wide: nobody chose to widen it. The exception is the tenant
  default: it stays enabled and becomes tenant-wide, because every account without its own default
  already uses it, so nothing changes for anyone.
- **Re-enabling a config that has no accounts** makes it tenant-wide, so it shows popup F.

Who can change LLM configs: saving, deleting and disabling integrations are already `tenant_admin`
only at the gateway (`actions.yaml`). api-server also checks tenant admin or super admin on the LLM
path, in case that role is ever widened. `integrations_check_llm_defaults` is `tenant_admin` too.

## Model picker (what a user can choose in Nubi)

The list of configs an account's users can pick (`getLLMIntegrationsForAccount`, `llm_common.go`),
with duplicates removed:

1. active configs linked to the account (its own default is always one of these);
2. active configs saved with no accounts (tenant-wide);
3. the tenant default, even when it is linked only to other accounts.

Picking from this list is the user's explicit choice and never depends on defaults. A config that is
another account's default but not the tenant default is not in the list. The same list backs pinned
sources (`resolveFromPinnedSource`) and the usage breakdown (`conversation_usage_metrics.go`).

## Picking (llm-server and rag-server)

For each request, use the first that exists:

1. The account's own default: an enabled config linked to the account and marked default for it.
2. The tenant default: the tenant's enabled config marked as tenant default.
3. The platform LLM (ENV). Each service uses its own ENV settings: llm-server its `LLM_PROVIDER` and
   `LLM_MODEL_NAME`, rag-server its own LLM settings, which are configured separately.
4. Otherwise fail with: "No LLM is configured for this account. Set a tenant default or a default
   for this account."

No step depends on how many configs an account has. The "only provider" and "tenant default among
my configs" steps are removed from `selectAccountLLMIntegration` (`llm_common.go`) and
`get_default_llm_integration` (`rag-server/rag/core/utils/db_query.py`). rag-server's config loader
also reads the tenant default when it is not linked, and skips disabled configs.

## Is the platform LLM configured?

Only llm-server knows. New endpoint `GET /v1/llm-config/env-status` returns
`{"configured": bool}`, true when `config.Config.LlmProvider` and `config.Config.LlmModel` are both
set (the same condition as the `env-global` layer in `llm_config.go`). api-server calls it; the UI
never calls llm-server directly. It describes llm-server only: popups that say "the platform LLM"
mean llm-server's. rag-server's own settings are not checked.

## What a save, delete or disable does

Every rule below is enforced by api-server, so a direct RPC call gets the same result as the modal.
The modal asks api-server first and shows a popup; the popup never decides anything on its own.

Every code path that changes an account's links or defaults goes through the same check: the save,
delete and status actions. Account deletion is different: it must never be refused because of an
LLM rule. It runs an LLM follow-up step in the same transaction as the generic link delete
(`account/service.go`): a config left with no accounts is disabled, or kept enabled as tenant-wide
if it is the tenant default; the step never blocks, and the tenant's caches are cleared afterwards.
The startup cleanup never changes which config an account uses.


New RPC action `integrations_check_llm_defaults` takes the proposed change (create, update, delete or
disable, with accounts and both ticks) and returns:

- `env_configured`
- the current tenant default (id and name), if any
- for each affected account: what it uses now and what it will use after the change
- `blocked`: accounts that would be left with no LLM, if any

`integrations_create_config`, `integrations_delete_config` and `integrations_update_status` run the
same check inside their transaction and refuse a blocked change with that list. The check takes a
per-tenant advisory lock (`pg_advisory_xact_lock`) so two saves cannot both become "the first
config" or both become the tenant default. No schema change.

The ENV status is read from llm-server **before** the transaction opens and is passed into the
check, so no HTTP call is ever made while the lock is held. If llm-server cannot be reached, the
check treats it as **no ENV**, the strict case: changes that would leave accounts with no LLM are
still blocked, and everything else, including adding or fixing a config, goes through. Admins can
always repair LLM configs while llm-server is down.

### Popups on Save

The form has two plain checkboxes, both unticked. Decisions surface as popups on Save. At most one
popup is shown; when two cases apply, they are combined into one.

| Popup | When | No ENV | ENV set |
|---|---|---|---|
| A: no tenant default | The tenant has no tenant default and the user did not tick it | "No platform LLM is configured. Accounts without their own default will have no LLM." Checkbox "Use as the tenant default" starts **ticked**. If accounts were selected and their default was not ticked: "Use as the default for A, B", unticked, and what A and B will use if neither is ticked. | "Accounts without their own default use the platform LLM. Making this the tenant default moves them to this config." Checkbox starts **unticked**. Same account section. |
| B: replacing the tenant default | The user ticked tenant default and another config X is the tenant default | "Replaces X as the tenant default. N accounts move from X to this config." Replace / Back | Same |
| C: replacing account defaults | The user ticked account default and some selected accounts already have one | "Replaces Y as the default for A." Replace / Back | Same |
| D: blocked | The change leaves accounts with no LLM | "A, B would have no LLM. Set another tenant default first, or keep this as their default." OK only; nothing is saved | Never shown: the platform LLM covers them |
| E: warning | The change moves accounts to a fallback | — | "A, B will move to the tenant default X" (or "to the platform LLM"). Save / Back |
| F: becomes tenant-wide | An edit removes every account from a restricted config | "No accounts are selected, so this config becomes available to every account in the tenant." Save / Back | Same |

No popup when the save changes no account's LLM, for example adding an extra config while a tenant
default exists.

Deleting or disabling a config from the LLM list (`integrations_delete_config`,
`integrations_update_status`) calls the same check and shows the same popups (B, D, E) before acting.
The list never deletes or disables without them.

### Changes that can block (no ENV) or warn (ENV set)

- Unticking, disabling or deleting the tenant default, while some account has no default of its own.
- Unticking an account default, removing an account from the selection, or disabling or deleting a
  config that is an account's default, while there is no tenant default.

## Removed

The account-creation hook that links the tenant default to every new account and marks it default:
`account/service.go` (`AttachTenantDefaultLLMProvider` call), `localagent/reconcile.go`, and the SQL
copy in `collector-server/cloud-collector/providers/aws/event_org_registration.go`, together with
`api-server/services/llm/tenant_default.go`. New accounts use the tenant default through picking
step 2, whatever path created them.

## Caches

`onIntegrationConfigChanged` (`api-server/services/api/integration_cache.go`) returns early when
the save names no accounts, so a tenant-wide config clears nothing today. It must clear every
account in the tenant whenever a tenant-wide config or the tenant default changes. llm-server's own
clear already covers every account in the tenant. rag-server expires on its TTL
(`rag_llm_provider_cache_ttl`).

## Fallback references

`validateFallbackRef` (`api-server/services/integrations/llm.go`) needs an account and rejects an
empty one. For a config with no accounts, it checks the reference against the tenant instead: the
referenced config must be tenant-wide (no accounts) or the tenant default.

In the modal, the "existing model" fallback list is loaded through the selected accounts
(`listModels` per account, `AddLLMConfigModal.jsx:791`) and is disabled with "Select an account
first" when none is selected. With no accounts selected it instead lists the models of the tenant's
tenant-wide configs and the tenant default, the same set `validateFallbackRef` accepts. llm-server's
model list gets a tenant mode for this: called without an account, it returns that set for the
caller's tenant. That mode is tenant admin only, the same users who can save tenant-wide configs.

## Existing data: one-time cleanup

Runs in llm-server code, not a migration. No new table. There is no backfill (see "Accepted
consequences").

The record: a row in `integration_config_values` on each LLM config, `name = 'defaults_migrated'`,
`value = 'v1'`. The name does not start with `llm_`, so llm-server never reads it as a provider
setting. Saves only delete a value that is sent empty, so later edits keep it.

The job runs on the elected leader pod (llm-server's existing leader scheduler, `common/scheduler.go`)
shortly after startup, in the background; pods serve as usual meanwhile. For each LLM config without
the record, one transaction:

1. Insert the record with `ON CONFLICT (integration_id, name) DO NOTHING`. If nothing was inserted,
   another pod did it; skip.
2. Cleanup: delete this config's links created by the account-creation hook. A link is treated as
   hook-created when all hold: the account was created after the config was last saved
   (`cloud_accounts.created_at > integrations.updated_at`, which a save always moves forward), the
   config is the tenant default, the link is marked default, and it is the account's only LLM link.
   The account then uses the same config through the tenant default, so nothing changes for it.
3. Commit.

New configs saved after the release get the record when they are saved, so the job never touches them.

Switching over: there is no per-request check and no copy of the old rules. Serving before the job
finishes is safe: an account whose hook-created link is still there has that config as its own
default, which is the same config it gets through the tenant default once the link is deleted.

It is a leader interval job that skips configs already done, so a failure is retried on the next
tick without a restart. If it fails it logs an error and the pod keeps serving.
A failed cleanup changes no account's LLM, so it must never stop llm-server from starting or block a
rollout.

Removal: the startup job, the `defaults_migrated` record and the save path that writes it are removed
in the release after this one has reached prod.

### Accepted consequences

- **No backfill.** On an install without ENV, an account whose only config is not marked default
  stops using it on release day: it uses the tenant default, or fails if there is none. An admin
  fixes it by marking the config as the account's default.
- **Rolling update.** While old llm-server pods are still serving, an account whose hook-created
  link was deleted has no LLM link under the old rules, so old pods send it to ENV, or fail on an
  install without ENV, until they are replaced (minutes).

## Shared test cases

One file, `llm/llm-server/agents/core/testdata/llm_default_cases.json`, holds the picking cases: the
tenant's configs and their scope, the tenant default, each account's own default, whether ENV is
set, and per account the expected config and the expected model-picker list. Three test suites read
it, so the three implementations cannot drift:

| Service | Tests | Path from the service folder |
|---|---|---|
| llm-server | `selectAccountLLMIntegration`, `getLLMIntegrationsForAccount` | `agents/core/testdata/llm_default_cases.json` |
| rag-server | `get_default_llm_integration` | `../llm-server/agents/core/testdata/llm_default_cases.json` |
| api-server | `integrations_check_llm_defaults` (what each account uses after a change) | `../../llm/llm-server/agents/core/testdata/llm_default_cases.json` |

CI checks out the whole repository before changing into the service folder, so the relative paths
resolve.

## Out of scope

- LLM config v2 (`LLM_CONFIG_V2`). The UI changes in this spec go into the current modal,
  `AddLLMConfigModal.jsx`, and the current LLM list. The v2 screen is not touched.

- Embeddings configs. rag-server stores them per account in the same way; a follow-up applies this
  model to them.
- Pinned conversations and fallback references to a config that is deleted. Unchanged behaviour.

## Acceptance criteria

- [ ] A tenant with no configs and no ENV: adding a config shows popup A with "tenant default"
      ticked; saving makes it the tenant default, and Nubi works on every account.
- [ ] With ENV set: popup A starts unticked; saving unticked leaves accounts on the platform LLM.
- [ ] An account with its own default keeps using it after another config is added to it.
- [ ] An existing account whose only config is not marked default uses the tenant default from
      release day (accepted: no backfill).
- [ ] Making config Z the tenant default (popup B) moves only accounts without their own default.
- [ ] An account with its own default keeps it when the tenant default changes.
- [ ] A new account, created by any path, uses the tenant default with no link created for it.
- [ ] No ENV: deleting or disabling the tenant default while an account depends on it is refused
      (popup D), by the modal and by a direct `integrations_delete_config` call.
- [ ] ENV set: the same delete shows popup E and, if confirmed, the accounts use the platform LLM.
- [ ] A config with no accounts can be saved and is listed with an "Organisation" scope.
- [ ] Removing every account from a restricted config shows popup F and makes it tenant-wide.
- [ ] Deleting the last account a config is restricted to disables that config, unless it is the
      tenant default, which stays enabled and becomes tenant-wide.
- [ ] Re-enabling a config with no accounts shows popup F.
- [ ] Deleting an account never fails because of an LLM rule.
- [ ] A tenant admin saving a config with no accounts can pick an existing tenant-wide model, or the
      tenant default's model, as its fallback.
- [ ] With llm-server unreachable, an admin can still add or fix a config; removals that would leave
      accounts with no LLM are refused.
- [ ] Deleting or disabling the tenant default from the LLM list shows popup D (no ENV) or E (ENV
      set), the same as from the modal.
- [ ] Account B, with no config of its own, sees the tenant default X in its model picker even when
      X is linked only to account A, and can pin a conversation to it.
- [ ] api-server refuses an LLM save, delete or disable from a user who is not a tenant admin or
      super admin, even if the gateway role were widened.
- [ ] llm-server and rag-server pick the same config for the same account in every case above.
- [ ] After deploy on dev: the 8 hook-created links are gone, those accounts use the tenant
      default, and every LLM config has `defaults_migrated`.
- [ ] The llm-server, rag-server and api-server tests all pass on the shared cases file.
- [ ] Two pods starting together run the cleanup once per config: one "cleaned up" log line per
      config across both pods.

## Rollout

Two PRs, one release: the backend PR (api-server, llm-server, rag-server, hook removal) and the UI
PR are merged and promoted together. Today's modal must never run against the new picking rules: it
has no popup A and only warns when an account already has other configs, so on an install without
ENV it would save an account's first config with no default and leave the account with no LLM.
