import fs from 'fs';
import path from 'path';
import yaml from 'js-yaml';
import { deriveSystemRoleGrants, DERIVED_SYSTEM_ROLE_KEYS, type ActionDef } from '@lib/systemRoleGrants';

// Guards the seed_system_roles migration's seeded grants against drift. The grant
// rows in the migration are GENERATED from actions.yaml via
// systemRoleGrants.deriveSystemRoleGrants. If actions.yaml changes a built-in
// role's `permissions:` (adding/removing an action), the derived grant set
// changes but the committed migration does not — this test fails, forcing the
// migration to be regenerated so the seeded grants stay faithful to the live
// operation surface. (See systemRoleGrants.ts for why this matters.)

function readActions(rel: string): ActionDef[] {
  const p = path.join(process.cwd(), rel);
  if (!fs.existsSync(p)) return [];
  const doc = yaml.load(fs.readFileSync(p, 'utf8')) as { actions?: ActionDef[] };
  return doc?.actions || [];
}

// Locate the seed_system_roles migration (path is relative to repo root, one level
// above app/). Matched on the name, not a fixed V number — the file gets renumbered
// every time it is re-merged onto a newer main, and a pinned number turned that
// into a recurring test edit.
function readMigrationUpSql(): string {
  const dir = path.join(process.cwd(), '..', 'api-server', 'migrations', 'migrations', 'app');
  const file = fs.readdirSync(dir).find((f) => /_V\d+_seed_system_roles\.up\.sql$/.test(f));
  if (!file) throw new Error('seed_system_roles up migration not found');
  return fs.readFileSync(path.join(dir, file), 'utf8');
}

// Parse the seeded grant tuples ('system_key', 'module', 'Class') from the SQL.
// The Read|Write|Execute class disambiguates grant rows from the role-seed rows.
function parseSeededGrants(sql: string): Set<string> {
  const re = /\('(\w+)',\s*'([\w]+)',\s*'(Read|Write|Execute)'\)/g;
  const out = new Set<string>();
  let m: RegExpExecArray | null;
  while ((m = re.exec(sql)) !== null) {
    out.add(`${m[1]}|${m[2]}|${m[3]}`);
  }
  return out;
}

describe('systemRoleGrants ↔ seed_system_roles migration', () => {
  const actions = [...readActions('src/lib/actions.yaml'), ...readActions('src/ee/actions.yaml')];

  it('derives a non-empty grant set for every assignable built-in role', () => {
    const rows = deriveSystemRoleGrants(actions);
    expect(rows.length).toBeGreaterThan(100);
    for (const key of ['tenant_admin', 'account_admin', 'k8s_namespace_admin']) {
      expect(rows.some((r) => r.systemKey === key)).toBe(true);
    }
  });

  it('matches the committed migration exactly (regenerate the seed if this fails)', () => {
    const derived = new Set(deriveSystemRoleGrants(actions).map((r) => `${r.systemKey}|${r.module}|${r.class}`));
    const seeded = parseSeededGrants(readMigrationUpSql());

    const missingFromMigration = [...derived].filter((g) => !seeded.has(g)).sort();
    const staleInMigration = [...seeded].filter((g) => !derived.has(g)).sort();

    expect({ missingFromMigration, staleInMigration }).toEqual({ missingFromMigration: [], staleInMigration: [] });
  });

  // The real, enforceable seed-level invariant: the CanManage-gated pairs (the
  // ONLY backend consumers of customPermissions) must not appear on any system
  // role except tenant_admin — otherwise flipping to unified would widen that
  // role's CanManage access vs today (e.g. account_admin gaining featureflags:Write).
  it('strips CanManage-gated pairs from every system role except tenant_admin', () => {
    const canManageGated = [
      'tenants:Write',
      'featureflags:Write',
      'integrations:Write',
      'notifications:Write',
      'messagingplatforms:Write',
      'applications:Write',
    ];
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
