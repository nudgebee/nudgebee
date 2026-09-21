# Ownership — Service Guide

Answers "who owns this resource?" for K8s workloads/namespaces/clusters and cloud resources. One owner (a user **or** a group) per resource.

**The central idea:** only *manual* assignments are stored. Rule-based ownership and inheritance are computed **at read time, on every resolve** — never materialized into a table. There is no sync job, no backfill, and no cache. Editing a rule changes every affected resource's owner on the next read.

**Storage:** PostgreSQL, two tables — `resource_owners` (manual assignments) and `ownership_rules` (lazily-evaluated rules).

---

## Code Layout

All paths below are under `api-server/services/ownership/`.

```
api-server/services/ownership/
├── model.go        constants (resource types, match scopes, domains, source/via),
│                   DB row structs, and every RPC request/response DTO
├── service.go      authz gates, the 9 RPC entry points, and the resolver core —
│                   resolveOne / resolveDeps. Read this first.
├── store.go        all SQL: owner + rule CRUD, workload/cloud-resource metadata
│                   loaders, conflict detection, the per-tenant advisory lock
├── rules.go        k8s rule matching + scope specificity
├── cloud_rules.go  cloud rule matching + scope specificity, cloudResourceMeta
└── *_test.go       pure-function tests — no DB, driven through fake resolveDeps
```

**Outside that package (related):**
- [api-server/services/api/actions_ownership.go](../api-server/services/api/actions_ownership.go) — HTTP entry points, all 9 `ownership_*` actions dispatched from [actions.go:216](../api-server/services/api/actions.go#L216)
- [app/src/lib/actions.yaml](../app/src/lib/actions.yaml) — action registry (~L5350–5462), maps each action to `/rpc/ownership`
- [api-server/services/knowledge_graph/sources/ownership_enricher.go](../api-server/services/knowledge_graph/sources/ownership_enricher.go) — mints `NudgebeeGroup` nodes, `MEMBER_OF` and `OWNS` edges by calling `ownership.Resolve` unmodified
- [api-server/migrations/migrations/app/](../api-server/migrations/migrations/app) — `V755` (resource_owners), `V756` (ownership_rules), `V757` (resource_domain)
- Frontend: [app/src/api1/ownership/index.js](../app/src/api1/ownership/index.js) (RPC), [app/src/components/ownership/](../app/src/components/ownership) (badge, panel, pickers, modals), [OwnershipRules.jsx](../app/src/components/user-management/OwnershipRules.jsx) (rules admin). Mounted on the K8s workload and namespace drilldowns (`OwnershipPanel`), and the cloud-account summary (`AccountOwner` in `CloudAccountSummary.tsx`).

---

## Domain Model

### `resource_key` encodings

The resolver is keyed by `(resource_type, resource_key)`. Getting the key format wrong is the single most common bug — it fails silently as "no owner".

| `resource_type` | `resource_key` |
|---|---|
| `workload` | `k8s_workloads.cloud_resource_id` (uuid) |
| `namespace` | `<cloud_account_id>/<namespace>` |
| `cloud_account` (also the "cluster" key) | `cloud_accounts.id` |
| `cloud_resource` | `cloud_resourses.id` (uuid) |
| `service` | KG `unique_key` — provisional, see gotchas |

Note that a workload's key and a cloud resource's key are both `cloud_resourses.id` values — one field can address either domain.

### Rule scopes

`resource_domain` (`k8s` | `cloud`) partitions rules so each resolver only loads its own rows. Operands are packed into `match_key` / `match_value` with no extra columns:

| Domain | `match_scope` | `match_key` | `match_value` | Account required |
|---|---|---|---|---|
| k8s | `label` | label key | label value | no |
| k8s | `namespace` | — | namespace name | no |
| k8s | `workload` | comma-joined workload names | namespace | **yes** |
| cloud | `cloud_tag` | tag key | tag value | no |
| cloud | `cloud_type` | — | `type` or `service_name` | no |
| cloud | `cloud_region` | — | region | no |
| cloud | `cloud_resource` | comma-joined `cloud_resourses.id` | — | **yes** |

Comma-joining is lossless because neither k8s names nor uuids contain commas.

---

## Resolution — the core

[`resolveOne`](../api-server/services/ownership/service.go) dispatches by `resource_type`. Two invariants govern every path:

1. **Manual beats rule at each level; a lower level beats a higher one.**
2. **Read-time orphan guard.** An owner only resolves if its resource still exists and is active. This is why every metadata loader filters on `k8s_workloads.is_active` / `cloud_resourses.is_active IS NOT FALSE`. Deleted resources have no owner without anything being cleaned up.

**Workload:** exists? → manual-on-workload (`via=self`) → workload/label-scope rule (`via=self`) → manual-on-namespace (`via=namespace`) → namespace-scope rule (`via=namespace`) → manual-on-account (`via=cluster`) → unowned.

**Namespace:** active? → manual → namespace rule → manual-on-account.

**Cloud resource:** active? → manual → cloud rule → manual-on-account (`via=cluster`).

**`cloud_account` / `service`:** manual only.

Namespace-scope rules are deliberately excluded from the workload-level rule pass (`workloadScopeRules`) so a *manual* namespace owner still outranks a namespace *rule*. Moving them would silently invert that precedence.

### Precedence within rules

By **scope specificity**, not by the `priority` column:

- k8s: `workload` > `label` > `namespace`
- cloud: `cloud_resource` > `cloud_tag` > `cloud_type` > `cloud_region`

Within one scope the oldest rule wins — loaders sort `ORDER BY created_at ASC, id ASC`.

### Why there is no priority

Instead of ranking overlapping rules, **overlaps are rejected at write time** (`findConflictingRule`). Same-scope, account-overlapping rules are compared: exact value equality for namespace/region/type, id- or name-set intersection for workload/cloud_resource, and for `label`/`cloud_tag` the check **queries live workloads/resources** to see whether any real object matches both rules. The check and the write run inside `pg_advisory_xact_lock(hashtext(tenant + ":ownership_rules"))` so two concurrent upserts cannot both pass.

### Batch vs single

Both paths run the same `resolveOne`; the seam is a `resolveDeps` struct of closures.

- `buildBatchDeps` (used by `Resolve`) bulk-loads owners, rules, requested metadata and the active-namespace set **once**, then resolves purely in memory. Cloud rules load lazily, so a pure-k8s batch never queries them.
- `singleDeps` (used by `GetOwner`) does point lookups, also loading rules lazily — skipped entirely when a direct manual owner is found.

This closure seam is what makes the resolver unit-testable without a database. Preserve it.

---

## RPC Surface

9 actions, all handled by [actions_ownership.go](../api-server/services/api/actions_ownership.go): `ownership_get`, `_resolve`, `_list`, `_assign`, `_delete`, `_cleanup`, `_list_rules`, `_upsert_rule`, `_delete_rule`.

**AuthZ** ([service.go](../api-server/services/ownership/service.go)): every write plus `list` and `cleanup` calls `requireTenantAdmin` (tenant_admin or super_admin). `get` and `resolve` only require a non-empty tenant. Super-admins must operate inside a selected tenant — an empty `tenantId` would otherwise reach a `$1::uuid` cast and fail.

**Cross-tenant safety:** `ownerExistsInTenant` rejects assigning a foreign user/group. `loadOwnerNames` scopes users through `tenant_users` (the `users` table has no tenant column) and groups through `user_groups.tenant`.

Writes emit audit events (`OWNERSHIP_ASSIGN` / `_DELETE` / `_RULE_UPSERT` / `_RULE_DELETE`, see [audit/model.go](../api-server/services/audit/model.go)).

---

## Things to Know Before Editing

- **The `priority` column is dead weight.** It exists in `ownership_rules`, is persisted by `upsertRuleRow` (default 100) and echoed in `RuleDto`, but **nothing reads it**. Precedence is scope specificity + `created_at`. Don't "fix" a precedence bug by wiring it up without deciding what happens to the conflict-rejection design that replaced it.
- **`Resolve` returns results aligned to the request, but callers should match by `(resource_type, resource_key)`, not by index.** The response carries both. See #35895 — ownership batch reads can treat a truncated result set as complete; index matching would shift every result onto the wrong resource and show a *wrong* owner rather than a missing one.
- **`resource_type='service'` is half-wired.** Assignable and resolvable (manual only), but no rule scope targets it, and the KG enricher deliberately skips it — most Service nodes are built in Phase 2.5, after the enricher runs in Phase 2.1, so it would work for some services and not others.
- **The UI write gate and the backend write gate disagree.** `OwnershipRules.jsx` shows write controls on `canManage('ownership','Write')` and `V833` grants that permission to custom roles, but the backend hard-requires `requireTenantAdmin`. A custom-role holder sees the buttons and gets a 401. Fix one side deliberately; don't paper over it.
- **The KG enricher duplicates `identity_enricher`'s NudgebeeUser node-ID formula on purpose.** Enricher run order is a map iteration, so it can't assume identity_enricher ran first. If that unique-key formula changes, `buildUserNodeIDs` must change with it or every `OWNS`/`MEMBER_OF` edge silently points at nothing.
- **Non-k8s cloud accounts have no KG node for "the whole account"** — only k8s mints a Cluster scaffold. So `cloud_account` ownership on a pure AWS/GCP/Azure account produces no `OWNS` edge. Pre-existing KG modeling gap, not an enricher bug.
- **`cleanupOrphans` is a backstop, not a correctness requirement.** Resolve already ignores orphaned rows via the active-resource guard. Account deletion is handled by the `ON DELETE CASCADE` FK on `cloud_account_id`.
- **A group rename changes its KG node identity.** `NudgebeeGroup` unique keys use the group name (tenant-unique at the DB level), so a rename tombstones the old node and mints a new one — `NodeTypeUserGroup` is in `InfraAuthoritativeNodeTypes` for exactly this reason.

---

## Where to Read First, by Task

| Task | Files in order |
|---|---|
| Understand resolution | [service.go](../api-server/services/ownership/service.go) `resolveOne` → `resolveWorkload` → [rules.go](../api-server/services/ownership/rules.go) `evalRules` |
| Add a new rule scope | [model.go](../api-server/services/ownership/model.go) (constant) → [rules.go](../api-server/services/ownership/rules.go) or [cloud_rules.go](../api-server/services/ownership/cloud_rules.go) (matcher + specificity list) → `validateK8sRule`/`validateCloudRule` in [service.go](../api-server/services/ownership/service.go) → `findConflictingRule` in [store.go](../api-server/services/ownership/store.go) → the rule modal in [OwnershipRuleModal.jsx](../app/src/components/ownership/OwnershipRuleModal.jsx) |
| Add a new ownable resource type | [model.go](../api-server/services/ownership/model.go) (`resource_key` convention + `validResourceTypes`) → a metadata loader in [store.go](../api-server/services/ownership/store.go) → a branch in `resolveOne` → `deriveCloudAccountId` → `collectOwnableResources` in the [KG enricher](../api-server/services/knowledge_graph/sources/ownership_enricher.go) |
| Surface ownership on a new UI screen | [app/src/api1/ownership/index.js](../app/src/api1/ownership/index.js) → `OwnerBadge` for display → `resolveOwners` (batch) for lists, `getOwner` for one resource → copy `AccountOwner` in [CloudAccountSummary.tsx](../app/src/components/cloudaccount/CloudAccountSummary.tsx) for a single badge, or [OwnershipPanel.jsx](../app/src/components/ownership/OwnershipPanel.jsx) for a full chain view |
| Debug "resource shows no owner" | confirm the `resource_key` encoding matches the table above → confirm the resource is `is_active` (the orphan guard hides owners of inactive resources) → check whether a lower level in the chain owns it → check `enabled` on the rule |
| Debug "rule won't save" | `findConflictingRule` in [store.go](../api-server/services/ownership/store.go) — the error names the overlapping rule; for `label`/`cloud_tag` the overlap is decided by querying live resources, so it depends on current cluster state |
| Debug missing `OWNS` edges in the KG | [ownership_enricher.go](../api-server/services/knowledge_graph/sources/ownership_enricher.go) `collectOwnableResources` (does the node carry the identifying property?) → `buildUserNodeIDs` (is the owner an active tenant user?) → the enricher degrades with a warn log rather than failing the build |

---

## Tests

All pure-function, no database — the `resolveDeps` closure seam lets the resolver run against in-memory fakes.

- [resolve_test.go](../api-server/services/ownership/resolve_test.go) — drives `resolveOne` through fake deps for every chain and precedence case
- [rules_test.go](../api-server/services/ownership/rules_test.go) / [cloud_rules_test.go](../api-server/services/ownership/cloud_rules_test.go) — matchers and scope specificity
- [conflict_test.go](../api-server/services/ownership/conflict_test.go) — write-time overlap rejection
- [../knowledge_graph/sources/ownership_enricher_test.go](../api-server/services/knowledge_graph/sources/ownership_enricher_test.go) — `collectOwnableResources`, `buildOwnsEdges`, `buildMemberOfEdges`

Run with `make test` from `api-server/services`. Frontend: `npm run test` from `app` (see `app/src/components/optimise-new/__tests__/OwnershipSection.test.tsx`).
