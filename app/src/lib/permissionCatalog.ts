// Permission catalog + action classifier — the single source of truth that maps
// every RPC action (app/src/lib/actions.yaml) to a (module, verb-class) permission
// used by the dynamic-RBAC custom-role system.
//
// Consumed by:
//   - rpcRoutes.ts        — sets route.permission at boot (the gateway gate)
//   - /api/permissions/catalog — enumerates the matrix the Roles UI renders
//   - Go handler sites    — pass the SAME (module, class) literals to CanManage()
//
// IMPORTANT: classification MUST be deterministic and total. classifyAction()
// returns null only for an unrecognized action; the gate then FAILS CLOSED
// (custom roles cannot grant it — only built-in roles can). A unit test
// (permissionCatalog.test.ts) asserts every action in actions.yaml classifies,
// so adding a new action with an unfamiliar verb fails CI until an override is
// added here. That is intentional: silent mis-classification is the dangerous
// failure mode for an authorization gate.

export type PermissionClass = 'Read' | 'Write' | 'Execute';

// Verb → class. The verb is normally the second underscore-token of an action
// name (`<module>_<verb>_<description>`). Mirrors docs/rpc-action-naming.md.
const VERB_CLASS: Record<string, PermissionClass> = {
  // Read
  get: 'Read',
  list: 'Read',
  aggregate: 'Read',
  count: 'Read',
  check: 'Read',
  search: 'Read',
  download: 'Read',
  // Write
  create: 'Write',
  update: 'Write',
  upsert: 'Write',
  delete: 'Write',
  insert: 'Write',
  save: 'Write',
  set: 'Write',
  clear: 'Write',
  reset: 'Write',
  erase: 'Write',
  correct: 'Write',
  record: 'Write',
  pin: 'Write',
  assign: 'Write',
  // ai_memory_confirm / _unconfirm flip a stored memory's confirmed flag — a
  // state mutation on an existing row, same shape as `pin`.
  confirm: 'Write',
  unconfirm: 'Write',
  // Execute
  apply: 'Execute', // apply = execute already-prepared changes (e.g. recommendations_apply)
  execute: 'Execute',
  replay: 'Execute',
  // recommendation_resolution_retry re-runs a failed resolution attempt — the
  // same "run it again" shape as `replay`.
  retry: 'Execute',
  cancel: 'Execute',
  pause: 'Execute',
  resume: 'Execute',
  publish: 'Execute',
  unpublish: 'Execute',
  sync: 'Execute',
  generate: 'Execute',
  enable: 'Execute',
  disable: 'Execute',
  trigger: 'Execute',
  schedule: 'Execute',
  export: 'Execute',
  import: 'Execute',
  convert: 'Execute',
  resolve: 'Execute',
  refresh: 'Execute',
  reresolve: 'Execute',
  setup: 'Execute',
  complete: 'Execute',
  optimize: 'Execute',
  scan: 'Execute',
  dryrun: 'Execute',
  configure: 'Execute',
  forward: 'Execute',
  run: 'Execute',
  cleanup: 'Execute',
  promote: 'Execute',
};

// Singular/plural & synonym normalization so the permission matrix stays tidy
// (one logical module, not `notification` AND `notifications`). The normalized
// value is what gets stored in custom_role_permissions.module and what the Go
// CanManage() literals use — both sides must agree, so they all route through
// here.
const MODULE_ALIASES: Record<string, string> = {
  anomaly: 'anomalies',
  application: 'applications',
  audit: 'audits',
  event: 'events',
  // Feature flags are tenant settings, not a domain of their own. Their two
  // reads (featureflags_list, features_list) are app-bootstrap calls EVERY role
  // has to make to render feature-gated UI, so a separate `featureflags:Read`
  // checkbox was a grant an admin could only ever get wrong; the one write
  // (featureflag_upsert) is a Tenant Settings mutation. Folded into `tenants` —
  // the class still derives from the verb, so the reads land on tenants:Read and
  // the upsert on tenants:Write, NOT on Read. Keep in lockstep with
  // feature_flag_v2's PermissionModule (api-server/services/query/metadata.go).
  featureflag: 'tenants',
  featureflags: 'tenants',
  features: 'tenants',
  integration: 'integrations',
  log: 'logs',
  metric: 'metrics',
  notification: 'notifications',
  recommendation: 'recommendations',
  // resource_* (details/groupings/spend_trend) read `cloud_resourses` + `spends`
  // — the same cloud-resource domain as the cloud_* actions. The split is an
  // action-naming artifact, not two domains, so fold them into one grant.
  resource: 'cloud',
  tenant: 'tenants',
  ticket: 'tickets',
  usergroup: 'usergroups',
  workflow: 'workflows',
};

// Explicit class for the handful of actions whose name doesn't expose a known
// verb in any token (no `<verb>`, no `_vN`/`grouping` query suffix). Keyed by
// the RAW action name. Keep this list minimal — prefer fixing the action name.
const ACTION_OVERRIDES: Record<string, PermissionClass> = {
  aws_cloud_formation: 'Read', // returns a CloudFormation onboarding URL
  aws_org_status: 'Read',
  critiques_trend_all: 'Read', // `trend` is not a taxonomy verb; reads a critique time series like critiques_aggregate_all
  database_performance_insights: 'Read',
  event_revert_threshold_suggestion: 'Execute', // operational inverse of event_apply_threshold_suggestion (apply=Execute)
  integrations_autogen_options: 'Read',
  log_group: 'Read',
  notifications_google_chat_permission_status: 'Read',
  // Proxy for the whole live-cluster surface. Classified at the READ bar so a
  // k8s:Read grant admits it at the gateway; relay.go re-checks read vs write
  // per action_name (writes need k8s:Write / built-in Update). See MODULE_OVERRIDES.
  relay_forward_request: 'Read',
  ticket_add_comment: 'Write',
  traces_counts: 'Read',
  traces_label_values: 'Read',
  upgrade_plan_fetch_all: 'Read',
  // `validate` is not a taxonomy verb, so this fell through unclassified and no
  // custom grant could reach it — breaking the editor's save flow for a
  // workflows:Write holder. Read-classified to match its alias `workflow_check`
  // (same handler, no side effects); Write implies Read at the gateway.
  workflow_validate: 'Read',
};

// Actions whose module the token-0 heuristic gets wrong, keyed by RAW action
// name → normalized module. The class still derives from the verb/`_v2` rule —
// this only re-homes the module. Keep minimal; prefer renaming the action.
const MODULE_OVERRIDES: Record<string, string> = {
  // Channel↔account mapping is Slack/Teams/etc. platform config, so it's
  // governed by the "Message Platform" (messagingplatforms) permission, not
  // notifications. Read (`_v2`) + the three write mutations move together.
  notification_channel_account_mapping_v2: 'messagingplatforms',
  notification_channel_mapping_create: 'messagingplatforms',
  notification_channel_mapping_update: 'messagingplatforms',
  notification_channel_mapping_delete: 'messagingplatforms',
  // Agent connectivity status is surfaced with the accounts catalog, so it's
  // governed by the accounts permission (accounts:Read) rather than a separate
  // agents grant. Kept in sync with get_agent_health_v2's PermissionModule.
  agents_list_health: 'accounts',
  // Cluster catalog/summary backs the cluster picker — governed by accounts
  // (accounts:Read) alongside the accounts list. Kept in sync with
  // k8s_cluster_groupings_v2's PermissionModule.
  k8s_cluster_groupings_v2: 'accounts',
  // The live-cluster relay proxy is re-homed from its own (non-grantable) `relay`
  // module to `k8s`: the resources it reads/mutates (get_resource for the
  // PVC/PV/Services tabs, workload mutations) are the k8s surface. This makes a
  // k8s:Read grant admit relay_forward_request (ACTION_OVERRIDES fixes the class
  // to Read); relay.go enforces read/write per action_name against the k8s grant.
  // NOTE: coarse — a k8s:Read grant thereby reaches EVERY relay read action_name
  // (get_resource plus all enrichers and external observability fetches), not just
  // cluster resources.
  relay_forward_request: 'k8s',
};

// Modules that are never grantable via a custom role, and so never appear in the
// role editor or the built-in-role viewer. classifyAction returns null for their
// actions, which makes the gateway fail closed (route.permission undefined →
// actionHasCustomGrant returns false) — so a grant row for one of these, however
// it reached the DB, can never admit a call.
//
// Excluding them at the classifier (rather than filtering buildCatalog) is what
// keeps the three consumers consistent: the editor matrix, the read-only system-
// role viewer (which renders deriveSystemRoleGrants, not the catalog), and the
// gateway gate all derive from classifyAction.
//
//   nudgebee  — nudgebee_list_versions, a build-info read; nothing to administer.
//   product   — product_updates_list, the platform changelog behind the "What's
//               New" drawer. It carries no tenant or account dimension and every
//               signed-in user is meant to see it, so it is declared
//               `tenant_agnostic: true` in actions.yaml and skips the gateway
//               gate entirely. A grantable `product:Read` checkbox on top of
//               that would have been a no-op an admin could still fail to tick.
//   signup    — pre-auth tenant-onboarding flow, driven server-side from
//               pages/api/auth/signup*.ts. Its actions carry NO `permissions:`
//               block on purpose, so route.allowedRoles is empty and no built-in
//               role (not even tenant_admin) can invoke them through the gateway.
//               Leaving them classifiable would let a custom grant admit calls
//               that no role can make today.
//   userauths — NextAuth account-link plumbing (userauths_create/_delete/
//               _update_accessed), called by the auth callbacks, not by admins.
//   auth      — session plumbing (auth_check_session/auth_delete_session), the
//               module's only two actions. Same empty-`permissions:` situation as
//               signup: unreachable by any built-in role today, so a grant must
//               not resurrect them.
//   userroles  — role ASSIGNMENT (`userroles_sync`, `userroles_upsert_*`). This is
//               privilege administration: the handler writes user_roles rows from
//               the request, so a grant here is a grant to mint `tenant_admin` for
//               yourself. Non-grantable so the classifier fails closed and the role
//               editor stops advertising it; SyncUserRoles independently requires a
//               tenant admin (defence in depth — neither check alone is relied on).
//               Deliberately NARROWER than "all identity modules": `users` stays
//               grantable because `users:Write` is a supported delegation (profile
//               edits + user creation via canAdministerUsers), with the privilege
//               half carved out by mayAssignTenantRole. `usergroups` is the same
//               shape: usergroup_create / usergroup_update /
//               usergroups_update_members gate on CanManage("usergroups","Write")
//               (api-server/services/tenant/authz_usergroups.go), with the
//               privilege half carved out by mayChangePrivilegedGroupMembership —
//               a grant holder cannot change membership of a group carrying a
//               tenant-wide or custom role, which is how they would otherwise mint
//               themselves an admin. Assigning a role TO a group stays here in the
//               non-grantable `userroles` module. `customroles` stays grantable at READ only;
//               its write actions are excluded by name (NON_GRANTABLE_ACTIONS).
//   roles      — the read-only built-in role catalog shares the privilege-admin
//               surface; nothing here is meant to be delegated.
//   relay      — the default (fail-closed) module for any future `relay_*` action.
//               The one relay action today, relay_forward_request, is deliberately
//               re-homed to `k8s` via MODULE_OVERRIDES so a k8s:Read grant can reach
//               the PVC/PV/Services live-cluster reads; relay.go re-checks read vs
//               write per action_name against the k8s grant (CanReadAccountData /
//               HasScopedPermission). Keeping `relay` here means a NEW relay action
//               is non-grantable until it too is explicitly re-homed. Built-in roles
//               are unaffected — they pass via actions.yaml `permissions:`.
//   webhook   — the module's only action, webhook_subject_mappings_sync, reads every
//               resolved incident of the tenant out of the connected Datadog /
//               PagerDuty / Zenduty account and rewrites the tenant-wide
//               webhook_subject_mappings table from them. That table decides which
//               subject every future alert is attributed to, so a bad sync silently
//               misroutes the tenant's incidents. It is tenant-admin (and super-admin)
//               operations work, not a delegable grant, so the module is non-grantable
//               and stops appearing as a tenant-level permission in the role editor.
//               SyncWebhookSubjectMappings re-checks the same two roles server-side.

//   billing    — the Billing tab was removed from the admin panel (PR #32989,
//               billingFilter.tsx is no longer imported from ee/init.ts), so
//               there is no UI surface left for a billing:Read grant to unlock.
//               Excluding the module here also drops it from the built-in-role
//               summary (deriveSystemRoleGrants runs through classifyAction
//               too); tenant_admin/tenant_admin_readonly are unaffected — they
//               still invoke billing_* via actions.yaml `permissions:`.
const NON_GRANTABLE_MODULES = new Set<string>([
  'auth',
  'billing',
  'nudgebee',
  'product',
  'relay',
  'roles',
  'signup',
  'userauths',
  'userroles',
  'webhook',
]);

// Individual actions that are never grantable, even though their module is.
// Same fail-closed mechanism as NON_GRANTABLE_MODULES, keyed by raw action name
// for the cases where only one action in an otherwise grantable module must be
// held back.
//
//   tenant_list_all — enumerates EVERY tenant, cross-tenant. It is super-admin
//     only: actions.yaml grants it to super_admin/super_admin_readonly, and the
//     handler re-checks IsSuperAdmin()/IsSuperAdminReadonly(). Without this
//     entry it classifies as `tenants:Read`, so a custom tenants:Read grant
//     would clear the gateway for it (the handler still denies, but the role
//     editor would advertise cross-tenant listing as grantable). Excluding it
//     keeps tenants:Read to the tenant-scoped reads it actually covers.
//     Built-in super_admin_readonly is unaffected — it passes on the
//     `permissions:` role match, which does not go through classifyAction.
//
//   customroles_* WRITES — role authoring and role ASSIGNMENT are privilege
//     administration. A `customroles:Write` grant would let its holder edit the
//     very role that carries it and tick every module in the matrix, i.e. grant
//     themselves the whole tenant. The handlers are already IsTenantAdmin()-only
//     (ee/customrole/service.go), so the grant was inert — but the role editor
//     still advertised a Write checkbox that silently did nothing. Excluding the
//     write actions by name removes that checkbox while keeping the module
//     grantable at Read, which IS honoured (canReadCustomRoles) and gives a
//     read-only view of the Roles tab.
const NON_GRANTABLE_ACTIONS = new Set<string>([
  'tenant_list_all',
  'customroles_create',
  'customroles_update',
  'customroles_delete',
  'customroles_update_user_assignments',
  'customroles_update_group_assignments',
  'customroles_update_group_account_assignments',
]);

export function normalizeModule(rawModule: string): string {
  return MODULE_ALIASES[rawModule] || rawModule;
}

// The 'ai' domain is very broad, so we split it into granular sub-modules
// based on the entity being operated on (tools, agents, memory, etc.).
function refineAiModule(name: string): string {
  if (name.includes('_tool')) return 'ai_tools';
  if (name.includes('_agent')) return 'ai_agents';
  if (name.includes('_kb') || name.includes('_rag')) return 'ai_kbs';
  if (name.includes('_memory')) return 'ai_memory';
  if (name.includes('_budget')) return 'ai_budget';
  if (name.includes('_conversation') || name.includes('_feedback') || name.includes('_reference')) return 'ai_conversations';
  if (name.includes('_model')) return 'ai_models';
  if (name.includes('_function')) return 'ai_functions';
  if (name.includes('_gc')) return 'ai_guardrails';
  if (name.includes('_rca')) return 'ai_rca';
  if (name.includes('_query') || name.includes('_workflow') || name.includes('_recommendation')) return 'ai_generation';
  return 'ai_misc';
}

// Scope groups the permissions UI renders under separate headings:
//   - 'tenant'  → tenant-wide configuration / identity (not tied to one cloud
//                 account): settings, integrations, notifications, users, roles…
//   - 'account' → operations on per-account / per-cluster operational data:
//                 k8s, cloud, traces, metrics, recommendations, workflows…
// Custom grants gate the *operation surface* only — an account-scoped grant
// still needs a built-in account role for the user to actually see data
// (see docs/architecture-decisions.md). The split is purely organizational so
// admins understand what each grant does. Classification is grounded in each
// module's backend handler authz (IsTenantAdmin/CanManage → tenant;
// HasAccountAccess/ListAccountIds → account).
export type ModuleScope = 'tenant' | 'account';

const MODULE_SCOPE: Record<string, ModuleScope> = {
  // Tenant-wide configuration & identity.
  tenants: 'tenant',
  users: 'tenant',
  usergroups: 'tenant',
  userroles: 'tenant',
  customroles: 'tenant',
  roles: 'tenant',
  integrations: 'tenant',
  notifications: 'tenant',
  messagingplatforms: 'tenant',
  config: 'tenant',
  audits: 'tenant',
  // Non-grantable (see NON_GRANTABLE_MODULES) — kept here, like `roles` and
  // `userroles` above, so the scope of the module is still recorded even though
  // the role editor never renders it.
  webhook: 'tenant',
  // ownership: handlers gate on requireTenantAdmin()/requireTenant(), no
  // HasAccountAccess — tenant-wide governance, not per-account data.
  ownership: 'tenant',
  // billing: module excluded via NON_GRANTABLE_MODULES below (Billing tab was
  // removed from the admin panel, PR #32989). Kept active here (rather than
  // commented out) so moduleScope('billing') still resolves correctly to
  // 'tenant' for any caller that queries it directly.
  billing: 'tenant',
  // egressfilter_* (llm-server /v1/egressfilter/config) resolve the tenant from
  // the security context and never take an account — tenant-wide DLP policy.
  egressfilter: 'tenant',
  // llm_* is the AI Gateway admin plane (routing rules, tier mappings, rate
  // limits, data-privacy settings). Every handler forces the tenant from the
  // session-injected x-tenant-id and has no account dimension, so an
  // account-scoped binding for it would be inert (its handlers are neither
  // /rpc/query nor on the gateway's ACCOUNT_ENFORCED_ACTIONS ledger, so the
  // scoped grant fails closed). Filing it under 'tenant' is what stops an admin
  // binding it to a group+account and believing it took effect.
  llm: 'tenant',
  // Per-account / per-cluster operational data.
  accounts: 'account',
  aws: 'account',
  azure: 'account',
  gcp: 'account',
  cloud: 'account',
  clusters: 'account',
  // critiques_* read per-account critique series (the LLM-server completions
  // family resolves the provider per account).
  critiques: 'account',
  database: 'account',
  // executions_* are workflow runs — same per-account surface as `workflows`.
  executions: 'account',
  k8s: 'account',
  agents: 'account',
  anomalies: 'account',
  applications: 'account',
  autooptimize: 'account',
  alertmanager: 'account',
  events: 'account',
  insights: 'account',
  kg: 'account',
  knowledge: 'account',
  logs: 'account',
  metrics: 'account',
  ml: 'account',
  // ai: most handler families (completions, knowledgebases, agents, …) require
  // account_id and gate on HasAccountAccess; budget/memory config is the
  // minority. observability: handlers resolve the logs/metrics/traces provider
  // strictly per account.
  ai: 'account',
  observability: 'account',
  recommendations: 'account',
  security: 'account',
  slo: 'account',
  spend: 'account',
  tickets: 'account',
  traces: 'account',
  upgrade: 'account',
  upgradeplans: 'account',
  workflows: 'account',
  // workload_*_criticality route to /rpc/triage — per-cluster workload metadata.
  workload: 'account',
};

// Default unknown modules to 'account' — the more conservative bucket (an
// account-scoped grant is inert without a built-in account role, whereas
// mislabeling a tenant-config module as account merely misfiles it in the UI).
export function moduleScope(module: string): ModuleScope {
  return MODULE_SCOPE[module] || 'account';
}

export type Classification = { module: string; class: PermissionClass };

// Classify an action name into a (module, class) permission. Returns null when
// the action cannot be confidently classified (gate fails closed).
export function classifyAction(name: string): Classification | null {
  if (!name) return null;
  // Never grantable by name, whatever its module would classify to.
  if (NON_GRANTABLE_ACTIONS.has(name)) return null;
  const parts = name.split('_');
  if (parts.length < 2) return null;
  let module = normalizeModule(parts[0]);

  // Refine the monolithic 'ai' module into granular sub-modules based on action name.
  if (module === 'ai') {
    module = refineAiModule(name);
  }

  // Per-action module correction (class still derives below from the verb/_v2 rule).
  if (MODULE_OVERRIDES[name]) {
    module = MODULE_OVERRIDES[name];
  }

  // Internal-plumbing modules are not grantable — fail closed before any class
  // is derived, so the gate, the catalog and the system-role derivation agree.
  if (NON_GRANTABLE_MODULES.has(module)) return null;

  // 1. Explicit override wins (lets us force-correct any name).
  const override = ACTION_OVERRIDES[name];
  if (override) return { module, class: override };

  // 2. Primary signal: the second token is the verb in the vast majority of
  //    names.
  const primary = VERB_CLASS[parts[1]];
  if (primary) return { module, class: primary };

  // 3. Hasura-style versioned table queries (`*_v2`) and `*grouping*`
  //    aggregations carry no explicit verb — they are reads.
  if (/_v\d+$/.test(name) || name.includes('grouping')) {
    return { module, class: 'Read' };
  }

  // 4. Fall back to the first known verb anywhere after the module (handles
  //    `notification_channel_mapping_create`, `webhook_subject_mappings_sync`).
  for (let i = 2; i < parts.length; i++) {
    const c = VERB_CLASS[parts[i]];
    if (c) return { module, class: c };
  }

  return null;
}

export function permissionKey(module: string, cls: PermissionClass): string {
  return `${module}:${cls}`;
}

// One action behind a grant, as the role editor shows it. `description` is the
// action's actions.yaml `comment:` when it has one, else the raw action name —
// only ~40% of actions are commented, so the fallback is the common case and is
// deliberately the bare name rather than invented prose.
export type CatalogAction = { name: string; description: string };

export type CatalogEntry = {
  module: string;
  scope: ModuleScope;
  classes: PermissionClass[];
  // The actions each granted class covers, so the editor can explain what
  // ticking Read/Write/Execute actually allows. Keyed by class; only classes
  // present in `classes` appear.
  actions: Partial<Record<PermissionClass, CatalogAction[]>>;
};

// An action as buildCatalog consumes it. A bare string is accepted for callers
// that have no descriptions to offer (tests, ad-hoc tooling).
export type CatalogInput = string | { name: string; comment?: string };

const CLASS_ORDER: PermissionClass[] = ['Read', 'Write', 'Execute'];

// Build the module × class matrix the Roles UI renders, from the action
// inventory (typically the entries of loadRpcRoutes()). Unclassifiable actions
// are skipped — they simply aren't grantable via a custom role.
export function buildCatalog(actions: CatalogInput[]): CatalogEntry[] {
  const byModule = new Map<string, Map<PermissionClass, CatalogAction[]>>();
  // First definition of a name wins, mirroring nothing in particular — the point
  // is that a name is listed once. actions.yaml does contain a duplicate entry
  // (a stray bare `- name: insights_list`), and the live caller passes
  // Object.keys(routes) which is already unique, so this only keeps the function
  // idempotent for callers reading the yaml directly.
  const seen = new Set<string>();
  for (const action of actions) {
    const name = typeof action === 'string' ? action : action.name;
    const comment = typeof action === 'string' ? undefined : action.comment;
    if (seen.has(name)) continue;
    seen.add(name);
    const c = classifyAction(name);
    if (!c) continue;
    if (!byModule.has(c.module)) byModule.set(c.module, new Map());
    const byClass = byModule.get(c.module)!;
    if (!byClass.has(c.class)) byClass.set(c.class, []);
    byClass.get(c.class)!.push({ name, description: comment?.trim() || name });
  }
  return [...byModule.entries()]
    .map(([module, byClass]) => {
      const classes = CLASS_ORDER.filter((cls) => byClass.has(cls));
      const entryActions: Partial<Record<PermissionClass, CatalogAction[]>> = {};
      for (const cls of classes) {
        entryActions[cls] = [...byClass.get(cls)!].sort((a, b) => a.name.localeCompare(b.name));
      }
      return { module, scope: moduleScope(module), classes, actions: entryActions };
    })
    .sort((a, b) => a.module.localeCompare(b.module));
}
