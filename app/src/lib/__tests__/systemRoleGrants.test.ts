import fs from 'fs';
import path from 'path';
import * as yaml from 'js-yaml';
import { deriveSystemRoleGrants, DERIVED_SYSTEM_ROLE_KEYS, type ActionDef } from '@lib/systemRoleGrants';

// Guards the seed_system_roles migration's seeded grants against drift. The grant
// rows in the migration are GENERATED from actions.yaml via
// systemRoleGrants.deriveSystemRoleGrants. If actions.yaml changes a built-in
// role's `permissions:` (adding/removing an action), the derived grant set
// changes but the committed migration does not — this test fails, forcing the
// migration to be regenerated so the seeded grants stay faithful to the live
// operation surface. (See systemRoleGrants.ts for why this matters.)

// Paths are resolved from __dirname, not process.cwd(): cwd is wherever the test
// runner was started, so running from the repo root (monorepo tooling, some IDE
// runners) silently resolved these against the wrong directory — and readActions
// returns [] on a missing file, so the drift assertion below would have compared
// an empty inventory and PASSED. __dirname is fixed relative to this file.
const APP_DIR = path.join(__dirname, '..', '..', '..');

function readActions(rel: string): ActionDef[] {
  const p = path.join(APP_DIR, rel);
  if (!fs.existsSync(p)) return [];
  const doc = yaml.load(fs.readFileSync(p, 'utf8')) as { actions?: ActionDef[] };
  return doc?.actions || [];
}

// The migration tree (path is relative to repo root, one level above app/).
// Files are matched on the name, not a fixed V number — they get renumbered
// every time the branch is re-merged onto a newer main, and a pinned number
// turned that into a recurring test edit.
const MIGRATIONS_DIR = path.join(APP_DIR, '..', 'api-server', 'migrations', 'migrations', 'app');

function readMigration(pattern: RegExp): string {
  const file = fs.readdirSync(MIGRATIONS_DIR).find((f) => pattern.test(f));
  if (!file) throw new Error(`migration matching ${pattern} not found`);
  return fs.readFileSync(path.join(MIGRATIONS_DIR, file), 'utf8');
}

function readMigrationUpSql(): string {
  return readMigration(/_V\d+_seed_system_roles\.up\.sql$/);
}

// Modules the retirement migration drops from custom_role_permissions. The seed
// migration is applied and immutable (atlas.sum), so a module that stops being
// grantable is removed by a LATER migration rather than by editing the seed —
// the effective seeded state is `seed − retired`. Parsed from that migration's
// `DELETE ... WHERE "module" IN (...)` so retiring another module needs no edit
// here.
function readRetiredModules(): Set<string> {
  const sql = readMigration(/_V\d+_retire_featureflags_product_grants\.up\.sql$/);
  // Whitespace-tolerant and case-insensitive: the statement spans lines today,
  // but a reformat (SQL formatter, editor, a future edit that joins the lines)
  // must not turn "the migration was reformatted" into "the seed drifted".
  const clause = /DELETE\s+FROM\s+"public"\."custom_role_permissions"\s+WHERE\s+"module"\s+IN\s*\(([^)]*)\)/i.exec(sql);
  if (!clause) throw new Error('retirement migration has no recognizable module DELETE');
  return new Set([...clause[1].matchAll(/'(\w+)'/g)].map((m) => m[1]));
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
    const retired = readRetiredModules();
    const seeded = new Set([...parseSeededGrants(readMigrationUpSql())].filter((g) => !retired.has(g.split('|')[1])));

    const missingFromMigration = [...derived].filter((g) => !seeded.has(g)).sort();
    const staleInMigration = [...seeded].filter((g) => !derived.has(g)).sort();

    expect({ missingFromMigration, staleInMigration }).toEqual({ missingFromMigration: [], staleInMigration: [] });
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
