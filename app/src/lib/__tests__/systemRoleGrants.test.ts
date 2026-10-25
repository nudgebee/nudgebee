import fs from 'fs';
import path from 'path';
import * as yaml from 'js-yaml';
import { deriveSystemRoleGrants, DERIVED_SYSTEM_ROLE_KEYS, type ActionDef } from '@lib/systemRoleGrants';

// Checks the built-in roles' derived grant sets, which the Roles editor shows
// read-only (pages/api/permissions/catalog.ts). V833 once seeded a copy of them
// into custom_role_permissions; nothing read it, and V1067 removed it, so there
// is no committed copy left to compare against.

// Paths are resolved from __dirname, not process.cwd(): cwd is wherever the test
// runner was started, so running from the repo root (monorepo tooling, some IDE
// runners) silently resolved these against the wrong directory — and readActions
// returns [] on a missing file, so the assertions below would have run on an
// empty inventory. __dirname is fixed relative to this file.
const APP_DIR = path.join(__dirname, '..', '..', '..');

function readActions(rel: string): ActionDef[] {
  const p = path.join(APP_DIR, rel);
  if (!fs.existsSync(p)) return [];
  const doc = yaml.load(fs.readFileSync(p, 'utf8')) as { actions?: ActionDef[] };
  return doc?.actions || [];
}

describe('systemRoleGrants', () => {
  const actions = [...readActions('src/lib/actions.yaml'), ...readActions('src/ee/actions.yaml')];

  it('derives a non-empty grant set for every assignable built-in role', () => {
    const rows = deriveSystemRoleGrants(actions);
    expect(rows.length).toBeGreaterThan(100);
    for (const key of ['tenant_admin', 'account_admin', 'k8s_namespace_admin']) {
      expect(rows.some((r) => r.systemKey === key)).toBe(true);
    }
  });

  // The real, enforceable seed-level invariant: the CanManage-gated pairs (the
  // ONLY backend consumers of customPermissions) must not appear on any system
  // role except tenant_admin — otherwise flipping to unified would widen that
  // role's CanManage access vs today (e.g. account_admin gaining tenants:Write).
  it('strips CanManage-gated pairs from every system role except tenant_admin', () => {
    const canManageGated = ['tenants:Write', 'integrations:Write', 'notifications:Write', 'messagingplatforms:Write', 'applications:Write'];
    const rows = deriveSystemRoleGrants(actions);
    for (const role of DERIVED_SYSTEM_ROLE_KEYS) {
      if (role === 'tenant_admin') continue;
      const held = rows.filter((r) => r.systemKey === role).map((r) => `${r.module}:${r.class}`);
      for (const pair of canManageGated) {
        expect(held).not.toContain(pair);
      }
    }
  });

  // NOTE: there is intentionally NO broader "account roles must not have
  // privilege modules" assertion. At module granularity, users/usergroups/roles/
  // customroles/userroles conflate self-service + read-only actions
  // (users_update_profile, roles_list, userroles_sync) with privilege-admin ones,
  // so account/k8s roles faithfully carry e.g. users:Write — exactly as
  // actions.yaml grants them today. Those grants are INERT: privilege-admin
  // handlers gate on IsTenantAdmin() directly (never CanManage/HasPermission, the
  // only customPermissions consumers). Custom (tenant-defined) roles are kept
  // from these modules by the grantable-catalog exclusion in PR6.
});
