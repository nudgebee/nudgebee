// System-role grant derivation — the single source of truth that maps each
// built-in role to the (module × class) grant set it should be SEEDED with in
// the unified entity-scoped grant model (custom_roles where is_system=true).
//
// The derivation is FAITHFUL: a system role's operation surface is exactly the
// set of actions actions.yaml currently grants that role (its `permissions:`
// entries), classified through permissionCatalog.classifyAction. This is what
// keeps the post-migration behavior identical — the grant set a built-in role
// resolves to equals the actions it can invoke today.
//
// Consumed by:
//   - __tests__/systemRoleGrants.test.ts — asserts the committed V766 migration
//     seed SQL matches this derivation exactly (fails CI if actions.yaml drifts
//     from the seeded grants). To REGENERATE the migration's grant block after an
//     intentional actions.yaml change, run deriveSystemRoleGrants over
//     actions.yaml (+ src/ee/actions.yaml) and emit one
//     ('<system_key>', '<module>', '<class>') VALUES row per grant.
//
// NOTE: super_admin / super_admin_readonly are intentionally NOT derived here —
// they bypass the gateway gate entirely (rpcGateway: isSuperAdmin short-circuit)
// and are never assigned via actions.yaml permissions. The migration seeds them
// as system roles for model uniformity but with no grant rows; their privileges
// come from the bypass, not from grants. tenant_usage / account_usage appear in
// actions.yaml but are absent from the `roles` table (no user can hold them), so
// they are dead and not seeded.

import { classifyAction, permissionKey, type PermissionClass } from '@lib/permissionCatalog';

// The assignable built-in roles whose grants we derive from actions.yaml. Order
// is the canonical seed order (mirrors the resolver's array_position ordering).
export const DERIVED_SYSTEM_ROLE_KEYS = [
  'tenant_admin',
  'tenant_admin_readonly',
  'account_admin',
  'account_admin_readonly',
  'k8s_namespace_admin',
  'k8s_namespace_admin_readonly',
] as const;

export type DerivedSystemRoleKey = (typeof DERIVED_SYSTEM_ROLE_KEYS)[number];

// Display labels for the built-in/system roles (matches the V766 seed names).
// Used by the Roles & Permissions UI to render the built-in roles read-only.
export const SYSTEM_ROLE_LABELS: Record<string, string> = {
  tenant_admin: 'Tenant Admin',
  tenant_admin_readonly: 'Tenant Admin (Read-only)',
  account_admin: 'Account Admin',
  account_admin_readonly: 'Account Admin (Read-only)',
  k8s_namespace_admin: 'K8s Namespace Admin',
  k8s_namespace_admin_readonly: 'K8s Namespace Admin (Read-only)',
};

// (module:class) pairs whose Go handlers gate on CanManage(module,class) =
// IsTenantAdmin() || HasPermission(module,class) — the ONLY backend consumers of
// customPermissions (see api-server tenant/service.go; no handler calls
// HasPermission directly). Today only tenant_admin passes these, via the
// IsTenantAdmin() branch — built-in roles carry no custom grants. Seeding any of
// these onto another system role would flip CanManage false→true and WIDEN that
// role's access vs today. So they are stripped from every system role EXCEPT
// tenant_admin (which keeps them purely for faithful UI display; its CanManage
// already passes via IsTenantAdmin, independent of the grant). Keep this set in
// lockstep with the CanManage call sites in api-server/services/tenant/service.go.
const CANMANAGE_GATED = new Set<string>([
  'tenants:Write',
  'featureflags:Write',
  'integrations:Write',
  'notifications:Write',
  'messagingplatforms:Write',
  'applications:Write',
]);

export type ActionDef = { name?: string; permissions?: Array<{ role?: string }> };

// A single derived grant row: (system_key, module, class).
export type SystemRoleGrant = { systemKey: string; module: string; class: PermissionClass };

// Derive the full set of system-role grant rows from the action inventory.
// Returns rows sorted deterministically (role order, then module, then class)
// so the emitted SQL and the test comparison are stable.
export function deriveSystemRoleGrants(actions: ActionDef[]): SystemRoleGrant[] {
  const CLASS_ORDER: PermissionClass[] = ['Read', 'Write', 'Execute'];
  // role -> set of "module:class"
  const byRole = new Map<string, Set<string>>();
  for (const key of DERIVED_SYSTEM_ROLE_KEYS) byRole.set(key, new Set());

  for (const action of actions) {
    if (!action.name) continue;
    const c = classifyAction(action.name);
    if (!c) continue; // unclassifiable actions are not grantable (fail closed)
    const key = permissionKey(c.module, c.class);
    for (const p of action.permissions || []) {
      if (p.role && byRole.has(p.role)) {
        byRole.get(p.role)!.add(key);
      }
    }
  }

  const rows: SystemRoleGrant[] = [];
  for (const systemKey of DERIVED_SYSTEM_ROLE_KEYS) {
    const entries = [...byRole.get(systemKey)!]
      // Strip CanManage-gated pairs from every role except tenant_admin (R2:
      // preserves identical handler behavior — only tenant_admin passes today).
      .filter((k) => systemKey === 'tenant_admin' || !CANMANAGE_GATED.has(k))
      .map((k) => {
        const [module, cls] = k.split(':');
        return { module, class: cls as PermissionClass };
      });
    entries.sort((a, b) => (a.module === b.module ? CLASS_ORDER.indexOf(a.class) - CLASS_ORDER.indexOf(b.class) : a.module.localeCompare(b.module)));
    for (const e of entries) rows.push({ systemKey, module: e.module, class: e.class });
  }
  return rows;
}
