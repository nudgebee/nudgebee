import fs from 'fs';
import path from 'path';
import * as yaml from 'js-yaml';
import { classifyAction, buildCatalog, normalizeModule, type PermissionClass } from '@lib/permissionCatalog';

// Pull every action name from actions.yaml (+ the EE overlay) directly, so this
// test stays decoupled from rpcRoutes internals and exercises the real action
// inventory the gateway gates on.
function readActionNames(rel: string): string[] {
  const p = path.join(process.cwd(), rel);
  if (!fs.existsSync(p)) return [];
  const doc = yaml.load(fs.readFileSync(p, 'utf8')) as { actions?: Array<{ name?: string }> };
  return (doc?.actions || []).map((a) => a.name).filter((n): n is string => !!n);
}

const ACTION_NAMES = [...readActionNames('src/lib/actions.yaml'), ...readActionNames('src/ee/actions.yaml')];

describe('permissionCatalog', () => {
  it('finds the action inventory', () => {
    expect(ACTION_NAMES.length).toBeGreaterThan(300);
  });

  // The core guarantee: every gated action classifies to a (module, class),
  // EXCEPT the deliberately non-grantable ones below. An action that cannot be
  // classified fails the gate closed (only built-in roles can invoke it) — so a
  // new unfamiliar verb MUST be added to the verb map or ACTION_OVERRIDES. This
  // test forces that, instead of silently shipping an action no custom role can
  // ever be granted.
  //
  // The exact-match assertion cuts both ways: an unclassifiable NEW action fails
  // here, and so does silently making one of these grantable again.
  const NON_GRANTABLE = [
    // auth: session plumbing, the module's only two actions.
    'auth_check_session',
    'auth_delete_session',
    // nudgebee: build-info read, nothing to administer.
    'nudgebee_list_versions',
    // product: the platform changelog. `tenant_agnostic: true` makes it
    // reachable by every signed-in user, so there is no grant to offer.
    'product_updates_list',
    // signup: pre-auth onboarding, server-side only. These carry no
    // `permissions:` block, so no built-in role can invoke them via the gateway
    // — a custom grant must not be able to either.
    'signup_check_token',
    'signup_complete',
    'signup_create',
    'signup_delete',
    'signup_update_status',
    // userauths: NextAuth account-link plumbing, called by the auth callbacks.
    'userauths_create',
    'userauths_delete',
    'userauths_update_accessed',
    // userroles / roles: role ASSIGNMENT is privilege administration — these
    // handlers write user_roles / group_roles rows from the request, so a grant
    // here is a grant to mint tenant_admin for yourself. Non-grantable by module
    // so the gate fails closed and the role editor stops offering them. Note
    // `users`/`usergroups` are deliberately NOT here: `users:Write` is a
    // supported delegation (with the privilege half carved out by
    // mayAssignTenantRole), and `usergroups` is inert because every handler is
    // IsTenantAdmin()-only.
    'roles_list',
    'userroles_sync',
    'userroles_upsert_account_group',
    'userroles_upsert_group',
    'userroles_upsert_k8s_namespace_group',
    // customroles WRITES: authoring a role and assigning it are the same
    // privilege administration as the userroles_* actions above — a
    // customroles:Write grant would let its holder tick every module on the very
    // role that carries it. Excluded by NAME so the module stays grantable at
    // Read, which IS honoured (canReadCustomRoles) and gives a read-only Roles tab.
    'customroles_create',
    'customroles_update',
    'customroles_delete',
    'customroles_update_user_assignments',
    'customroles_update_group_assignments',
    'customroles_update_group_account_assignments',
    // tenant_list_all: cross-tenant enumeration, super-admin only. Excluded by
    // name (its module `tenants` stays grantable for tenant-scoped reads).
    'tenant_list_all',
    // webhook: rewrites the tenant-wide subject-mapping table that decides which
    // subject every future alert is attributed to. Tenant-admin / super-admin
    // operations work, not a delegable grant — non-grantable by module.
    'webhook_subject_mappings_sync',
  ];

  it('classifies every action in actions.yaml except the non-grantable ones', () => {
    const unclassified = [...new Set(ACTION_NAMES)].filter((name) => classifyAction(name) === null);
    expect(unclassified.sort()).toEqual([...NON_GRANTABLE].sort());
  });

  // tenant_list_all enumerates every tenant. It must never be reachable via a
  // custom grant — only super_admin (gateway bypass) and super_admin_readonly
  // (built-in `permissions:` role match, which doesn't consult classifyAction).
  // Its module must stay grantable for the tenant-scoped reads.
  it('keeps tenant_list_all super-admin only while tenants stays grantable', () => {
    expect(classifyAction('tenant_list_all')).toBeNull();
    expect(classifyAction('tenants_list')).toEqual({ module: 'tenants', class: 'Read' });
    const tenants = buildCatalog(ACTION_NAMES).find((e) => e.module === 'tenants');
    expect(tenants).toBeDefined();
    expect((tenants!.actions.Read ?? []).map((a) => a.name)).not.toContain('tenant_list_all');
  });

  // A `customroles:Write` grant would be self-escalating (edit the role you
  // hold, tick every module). Read must stay grantable — it is what backs the
  // delegated read-only Roles tab — and Write must not exist at all, so the role
  // editor never renders a Write checkbox that silently does nothing.
  it('keeps customroles grantable at Read only', () => {
    expect(classifyAction('customroles_list')).toEqual({ module: 'customroles', class: 'Read' });
    expect(classifyAction('customroles_get')).toEqual({ module: 'customroles', class: 'Read' });
    for (const write of [
      'customroles_create',
      'customroles_update',
      'customroles_delete',
      'customroles_update_user_assignments',
      'customroles_update_group_assignments',
      'customroles_update_group_account_assignments',
    ]) {
      expect(classifyAction(write)).toBeNull();
    }
    const customroles = buildCatalog(ACTION_NAMES).find((e) => e.module === 'customroles');
    expect(customroles).toBeDefined();
    expect(customroles!.classes).toEqual(['Read']);
  });

  it('keeps non-grantable modules out of the catalog', () => {
    const modules = new Set(buildCatalog(ACTION_NAMES).map((e) => e.module));
    for (const m of ['auth', 'nudgebee', 'product', 'relay', 'signup', 'userauths', 'webhook']) {
      expect(modules.has(m)).toBe(false);
    }
  });

  // Feature flags are tenant settings, not their own domain. The reads every
  // role has to make land on tenants:Read; the one write must NOT — folding a
  // mutation into a Read grant would let a read-only role toggle flags.
  it('folds the featureflags module into tenants, keeping the write on tenants:Write', () => {
    expect(classifyAction('featureflags_list')).toEqual({ module: 'tenants', class: 'Read' });
    expect(classifyAction('features_list')).toEqual({ module: 'tenants', class: 'Read' });
    expect(classifyAction('featureflag_upsert')).toEqual({ module: 'tenants', class: 'Write' });
    expect(new Set(buildCatalog(ACTION_NAMES).map((e) => e.module)).has('featureflags')).toBe(false);
  });

  // The platform changelog is available to every signed-in user via
  // `tenant_agnostic: true`, so there is no grant to hand out for it.
  it('makes the product module non-grantable', () => {
    expect(classifyAction('product_updates_list')).toBeNull();
  });

  // resource_* and cloud_* are one cloud-resource domain (both read
  // `cloud_resourses`); the alias folds them into a single `cloud` grant.
  it('folds the resource module into cloud', () => {
    expect(normalizeModule('resource')).toBe('cloud');
    expect(classifyAction('resource_details_v2')).toEqual({ module: 'cloud', class: 'Read' });
    expect(classifyAction('resource_spend_trend_v2')).toEqual({ module: 'cloud', class: 'Read' });
    expect(new Set(buildCatalog(ACTION_NAMES).map((e) => e.module)).has('resource')).toBe(false);
  });

  it('produces only Read/Write/Execute classes', () => {
    const valid: PermissionClass[] = ['Read', 'Write', 'Execute'];
    for (const name of ACTION_NAMES) {
      const c = classifyAction(name);
      if (c) expect(valid).toContain(c.class);
    }
  });

  it('normalizes singular/plural modules consistently', () => {
    expect(normalizeModule('notification')).toBe('notifications');
    expect(normalizeModule('tenant')).toBe('tenants');
    expect(normalizeModule('integration')).toBe('integrations');
    expect(normalizeModule('application')).toBe('applications');
  });

  it('classifies representative actions correctly', () => {
    expect(classifyAction('workflows_create_schedule')).toEqual({ module: 'workflows', class: 'Write' });
    expect(classifyAction('accounts_list')).toEqual({ module: 'accounts', class: 'Read' });
    expect(classifyAction('anomaly_execute')).toEqual({ module: 'anomalies', class: 'Execute' });
    // MODULE_OVERRIDES re-homes channel↔account mapping under messagingplatforms.
    expect(classifyAction('notification_channel_mapping_create')).toEqual({ module: 'messagingplatforms', class: 'Write' });
    expect(classifyAction('notification_channel_account_mapping_v2')).toEqual({ module: 'messagingplatforms', class: 'Read' });
    // Agent health + cluster catalog are re-homed under the accounts module.
    expect(classifyAction('agents_list_health')).toEqual({ module: 'accounts', class: 'Read' });
    expect(classifyAction('k8s_cluster_groupings_v2')).toEqual({ module: 'accounts', class: 'Read' });
    expect(classifyAction('k8s_pods_v2')).toEqual({ module: 'k8s', class: 'Read' });
    expect(classifyAction('ticket_add_comment')).toEqual({ module: 'tickets', class: 'Write' });
  });

  // The editor explains a grant by listing the actions behind it. Only ~40% of
  // actions carry a yaml `comment:`, so the raw-name fallback is the common
  // case and must stay lossless — never an invented description.
  it('carries the actions behind each class, describing them by comment with a name fallback', () => {
    const [entry] = buildCatalog([
      { name: 'events_list', comment: 'List events with filters' },
      { name: 'event_groupings_v2' }, // no comment → falls back to the name
      { name: 'event_update', comment: '  ' }, // blank comment → also falls back
    ]);
    expect(entry.module).toBe('events');
    expect(entry.classes).toEqual(['Read', 'Write']);
    expect(entry.actions.Read).toEqual([
      { name: 'event_groupings_v2', description: 'event_groupings_v2' },
      { name: 'events_list', description: 'List events with filters' },
    ]);
    expect(entry.actions.Write).toEqual([{ name: 'event_update', description: 'event_update' }]);
    expect(entry.actions.Execute).toBeUndefined();
  });

  it('accepts bare action names and lists every classified action exactly once', () => {
    const catalog = buildCatalog(ACTION_NAMES);
    const listed = catalog.flatMap((e) => e.classes.flatMap((c) => e.actions[c] ?? []));
    const classifiable = [...new Set(ACTION_NAMES)].filter((n) => classifyAction(n) !== null);
    expect(listed.length).toBe(classifiable.length);
    // Every entry's advertised classes have a non-empty action list.
    for (const e of catalog) {
      for (const c of e.classes) expect((e.actions[c] ?? []).length).toBeGreaterThan(0);
    }
  });

  it('builds a non-empty, sorted catalog with a valid scope on every entry', () => {
    const catalog = buildCatalog(ACTION_NAMES);
    expect(catalog.length).toBeGreaterThan(20);
    const modules = catalog.map((c) => c.module);
    expect(modules).toEqual([...modules].sort((a, b) => a.localeCompare(b)));
    for (const entry of catalog) {
      expect(entry.classes.length).toBeGreaterThan(0);
      expect(['tenant', 'account']).toContain(entry.scope);
    }
    // Both scope groups must be populated so the editor renders both sections.
    expect(catalog.some((e) => e.scope === 'tenant')).toBe(true);
    expect(catalog.some((e) => e.scope === 'account')).toBe(true);
  });
});
