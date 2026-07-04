# Integration Identity Sync

Maps internal Nudgebee users to their external integration accounts
(Slack, GitHub, PagerDuty, ZenDuty) by email, so the platform knows "this Slack
user **is** this Nudgebee user." Unmatched accounts can be mapped manually from the
**Edit User** page, which also shows each user's linked integration profiles.

This is **Phase 1 + 2** of the ownership/identity feature. Phase 3 (resource
ownership) builds on the identity map produced here.

---

## Flow at a glance

```
runbook-server cron (*/30)                       ┌─ Edit User page (admin)
   │ POST /rpc-cron {name:"Identity Sync"         │     reads & maps via RPC
   │                 payload?:{tenant_id}}          │
   ▼                                                ▼
api-server /rpc-cron  ──► user.RunIdentitySync ──► integration_user_accounts (DB)
   (api/cron.go)            (per tenant × provider)      ▲
                                │ fetch + upsert + auto-match + reconcile │
                                ▼                                          │
        Slack / PagerDuty / ZenDuty (external APIs) · GitHub (synced config)
```

## Trigger

- **Scheduled:** `runbook-server/internal/system/cron_triggers.yaml` → `Identity Sync`
  every 30 min → `POST {api-server}/rpc-cron` with `X-ACTION-TOKEN`.
- **Targeted / on-demand:** same call with `payload:{"tenant_id":"…"}` syncs a single
  tenant (testing, ops re-sync). Empty payload syncs all tenants.
- Dispatched by `api/cron.go` `case "Identity Sync"` → `user.RunIdentitySync(ctx, tenantFilter)`.

## Sync engine — `identity_sync.go`

For each tenant (or just the filtered one):

1. **Scope** is derived from the integration's category via `Category().IsTenantScoped()`:
   - **Tenant-scoped** (Messaging/Ticketing — all four providers today) → one row with
     `account_id = NULL` ("All accounts").
   - **Account-scoped** (future provider types) → one row per cloud account from
     `integrations_cloud_accounts`; skipped if scoped to none.
2. **Fetch** each integration's users via `core.UserLister.ListUsers` (25s per-fetch
   timeout so one slow/unreachable integration can't stall the run).
3. **Upsert** one row per `(account × external user)`.
4. **Reconcile** removed-at-source users (see below).
5. **Auto-match by email** (`autoMatchByEmail`) once per tenant, after all integrations.

## Providers — `core.UserLister`

Each integration owns its user-fetch logic in `services/integrations/*.go` and
implements the optional `core.UserLister` interface (same pattern as
`TestableIntegration`). The sync just type-asserts it:

```go
integration, _ := core.GetIntegration(integType)
lister, ok := integration.(core.UserLister)   // skip if no user concept
accounts, _ := lister.ListUsers(ctx, intg.Configs)
```

| Provider | Source | Email? | Auto-match |
|---|---|---|---|
| PagerDuty | `go-pagerduty` SDK `ListUsers` | yes | yes |
| ZenDuty | `GET /account/users/` (Token auth) | yes | yes |
| Slack | `slack.com/api/users.list` (stored bot token) | yes | yes |
| GitHub | reads the integration's synced `users` config (collaborator logins) | **no** | **no — manual only** |

GitHub returns logins only (no email) and reads the already-synced `users` config
rather than the API — this sidesteps PAT-vs-App auth, org-vs-personal-account
detection, and the `/orgs/{user}/members` 404 that personal-account integrations hit.

## Data model — `integration_user_accounts`

One row per `(tenant_id, account_id[nullable], integration_type, integration_id, external_user_id)`.

| Column | Notes |
|---|---|
| `account_id` | `NULL` for tenant-scoped providers; set for account-scoped fan-out |
| `external_email/username/display_name` | from the provider (`email` empty for GitHub) |
| `mapped_user_id` | internal `users.id`, null until matched |
| `mapped_via` | `auto_email` \| `manual` \| null |
| `is_active` | `false` = tombstoned (removed at source); manual rows are kept inactive and reactivate on re-add |

A unique index coalesces a `NULL` `account_id` to a sentinel so dedupe works.

## Auto-match & manual map

- **Auto-match:** `lower(external_email) = lower(users.username)`, scoped to the
  tenant's members, on **active** rows. Sets `mapped_via='auto_email'`. **Never
  overwrites a `manual` mapping.**
- **Manual map/unmap** (`users_create_account_mapping` / `users_delete_account_mapping`):
  scoped to `(account, identity)`, so it affects every instance of that person in that
  account but no other account. The upsert never touches mapping columns, so a re-sync
  can't clobber a link.

## Reconciliation (removed-at-source)

The sync is otherwise upsert-only; reconciliation removes what disappeared, safely:

- **User removed from a source** → `reconcileIntegration` (after a successful, non-empty
  fetch): `manual` rows are **tombstoned** (`is_active=false`, preserved), everything
  else is **hard-deleted**.
- **Integration disabled/deleted** → `sweepDisabledIntegrations` (only when integration
  enumeration succeeded for all types): same manual-tombstone / else-delete rule.
- **User re-added** → the upsert flips `is_active=true` while keeping the mapping, so a
  **manual mapping self-restores** (auto/email rows just re-match).

**Safety guards:** a transient fetch failure (timeout) skips reconcile for that
integration; a transient enumeration error skips the disabled sweep — neither can wipe
valid data.

## RPC actions (`actions_user.go` + `app/src/lib/actions.yaml`)

| Action | Purpose | Permission |
|---|---|---|
| `users_list_integration_accounts(user_id)` | a user's linked profiles, deduped per `(account, provider, identity)` | tenant_admin, tenant_admin_readonly |
| `users_list_unmapped_accounts(integration_type?)` | unmatched pool for the picker | tenant_admin, tenant_admin_readonly |
| `users_create_account_mapping(account_id, user_id)` | manual map | tenant_admin |
| `users_delete_account_mapping(account_id)` | unmap | tenant_admin |

Writes are audited.

## UI — Edit User → "Integration profiles"

`app/src/components/user-management/modal/UserModal.jsx` (edit mode):

- Linked profiles grouped by account ("All accounts" for tenant-scoped), each with the
  provider logo, name/email, an **Auto**/**Manual** badge, and **Unmap**.
- A cascading manual-map picker: **integration type → account (only when >1) → profile → Map**.
- Tenant-admin only for writes; `tenant_admin_readonly` sees it read-only.

## Adding a new user-bearing integration

Implement `ListUsers` on its `services/integrations/<name>.go` struct:

```go
func (x MyIntegration) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error)
```

Identity sync picks it up automatically (via the type-assert). Use
`core.ConfigValue(values, KEY)` to read decrypted config. If the type should be
account-scoped, ensure its category is **not** tenant-scoped so it fans out per
`integrations_cloud_accounts`.

## Files

- Backend: `services/user/identity_{sync,store,model}.go`, `services/integrations/core/external_user.go`,
  `services/integrations/{pagerduty,zenduty,slack,github_issues}.go`,
  `services/api/{cron.go,actions_user.go}`
- Migration: `migrations/migrations/app/*_V752_create_integration_user_accounts.up.sql`
- Cron: `runbook-server/internal/system/cron_triggers.yaml`
- Frontend: `app/src/components/user-management/modal/UserModal.jsx`, `app/src/api1/user/index.js`, `app/src/lib/actions.yaml`
