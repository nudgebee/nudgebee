import fs from 'fs';
import path from 'path';
import * as yaml from 'js-yaml';
import {
  classifyAction,
  buildCatalog,
  normalizeModule,
  isTokenScopeBlockedAction,
  withRunTimeGrants,
  RUN_TIME_GRANTS,
  type PermissionClass,
} from '@lib/permissionCatalog';

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
    // billing: the Billing tab was removed from the admin panel (PR #32989),
    // so there's no UI left for a billing grant to unlock. Non-grantable by
    // module; tenant_admin/tenant_admin_readonly still invoke these via
    // actions.yaml `permissions:`, which doesn't consult classifyAction.
    'billing_list',
    'billing_list_usage_costs',
    'billing_aggregate',
    // AI Gateway administration is tenant-admin only (#38963, #38965).
    'llm_gateway_get_settings',
    'llm_gateway_update_settings',
    'llm_gateway_list_routing_rules',
    'llm_gateway_create_routing_rule',
    'llm_gateway_update_routing_rule',
    'llm_gateway_delete_routing_rule',
    'llm_gateway_list_tiers',
    'llm_gateway_set_tier',
    'llm_gateway_reset_tier',
    'llm_gateway_list_models',
    'llm_gateway_list_rate_limits',
    'llm_gateway_upsert_rate_limit',
    'llm_gateway_delete_rate_limit',
    'llm_gateway_list_user_access',
    'llm_gateway_upsert_user_access',
    // Every key in the tenant, and revoking any of them, is tenant-admin only (#38965).
    'users_list_tenant_tokens',
    'users_delete_tenant_token',
    // Sign-in internals the app server calls as super admin. Their tables have
    // no tenant column, so a users:Read / tenants:Read grant read any tenant's
    // credential hashes, roles and tenants.
    'users_get_auth_by_username',
    'users_get_by_provider_account',
    'users_get_super_admin_role',
    'tenant_by_user_v2',
    // bees_install / bees_uninstall: installing a Worker Bee creates its account
    // and the role it acts with. Privilege administration, like role assignment
    // above; the handler is tenant-admin only.
    'bees_install',
    'bees_uninstall',
    // Creating or deleting a bee's principal, or the user behind it, is the same
    // privilege administration.
    'bees_create_principal',
    'bees_delete_principal',
    'users_create_bee',
    // Changing an installed bee's dial and previewing an install are tenant-admin
    // only in runbook-server.
    'bees_update_modes',
    'bees_check_install',
    // Reloads the shared system templates for every tenant.
    'workflow_sync_templates',
    // Trace aggregates whose tables have no tenant column.
    'traces_groupings_v2',
    'traces_heatmap_v2',
    // branding: tenant branding overrides are super-admin only
    // (ee/branding/service.go), so there is no grant to offer.
    'branding_list_overrides',
    'branding_get_override',
    'branding_upsert_override',
    'branding_apply_previous_override',
    'branding_delete_override',
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

  it('keeps AI Gateway administration non-grantable and usage grantable', () => {
    for (const name of [
      'llm_gateway_get_settings',
      'llm_gateway_update_settings',
      'llm_gateway_list_routing_rules',
      'llm_gateway_create_routing_rule',
      'llm_gateway_update_routing_rule',
      'llm_gateway_delete_routing_rule',
      'llm_gateway_list_tiers',
      'llm_gateway_set_tier',
      'llm_gateway_reset_tier',
      'llm_gateway_list_models',
      'llm_gateway_list_rate_limits',
      'llm_gateway_upsert_rate_limit',
      'llm_gateway_delete_rate_limit',
      'llm_gateway_list_user_access',
      'llm_gateway_upsert_user_access',
    ]) {
      expect(classifyAction(name)).toBeNull();
    }
    for (const name of ['llm_gateway_aggregate_usage', 'llm_gateway_list_requests', 'llm_gateway_list_sessions', 'llm_gateway_get_request_body']) {
      expect(classifyAction(name)).not.toBeNull();
    }
  });

  it('keeps tenant-wide API key actions non-grantable', () => {
    expect(classifyAction('users_list_tenant_tokens')).toBeNull();
    expect(classifyAction('users_delete_tenant_token')).toBeNull();
    expect(isTokenScopeBlockedAction('users_list_tenant_tokens')).toBe(true);
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

  // Delivery history is read like the alerts it delivered, so an events:Read
  // grant reaches it and a notifications grant (rules) does not.
  it('reads delivery history with events:Read', () => {
    expect(classifyAction('notifications_list_deliveries')).toEqual({ module: 'events', class: 'Read' });
    expect(classifyAction('notifications_aggregate_deliveries')).toEqual({ module: 'events', class: 'Read' });
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
    // Changing how Nudgebee connects to a cloud account is account management.
    expect(classifyAction('cloud_account_update')).toEqual({ module: 'accounts', class: 'Write' });
    expect(classifyAction('cloud_update_cloudformation_permissions')).toEqual({ module: 'accounts', class: 'Write' });
    expect(classifyAction('cloud_setup_monitoring_webhook')).toEqual({ module: 'accounts', class: 'Execute' });
    expect(classifyAction('cloud_check_credentials')).toEqual({ module: 'accounts', class: 'Read' });
    expect(classifyAction('cloud_check_monitoring_permission')).toEqual({ module: 'accounts', class: 'Read' });
    // Resource-level cloud actions stay under cloud.
    expect(classifyAction('cloud_execute_command')).toEqual({ module: 'cloud', class: 'Execute' });
    expect(classifyAction('cloud_resource_attributes_upsert')).toEqual({ module: 'cloud', class: 'Write' });
    expect(classifyAction('k8s_pods_v2')).toEqual({ module: 'k8s', class: 'Read' });
    expect(classifyAction('ticket_add_comment')).toEqual({ module: 'tickets', class: 'Write' });
  });

  // llm-server calls security_scan_image directly, not through the gateway, so
  // it sends this action's grant key itself (services_server/service.go). A
  // change to the classification must change that key too.
  it('matches the grant key llm-server sends with security_scan_image', () => {
    const src = fs.readFileSync(path.join(process.cwd(), '..', 'llm/llm-server/services_server/service.go'), 'utf8');
    const sent = src.match(/ScanImageActionPermission\s*=\s*"([^"]+)"/);
    expect(sent).toBeTruthy();
    const c = classifyAction('security_scan_image');
    expect(c).not.toBeNull();
    expect(`${c!.module}:${c!.class}`).toBe(sent![1]);
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

  describe('run-time grants', () => {
    const catalog = buildCatalog(ACTION_NAMES);
    const withRun = withRunTimeGrants(catalog);
    const byModule = new Map(withRun.map((e) => [e.module, e]));

    it('adds Execute to k8s, explained, so a change step needs no Write', () => {
      const before = catalog.find((e) => e.module === 'k8s')!;
      const after = byModule.get('k8s')!;
      // Guard: the class is added only because no action supplies it.
      expect(before.classes).not.toContain('Execute');
      expect(after.classes).toEqual([...before.classes, 'Execute']);
      expect(after.notes?.Execute).toMatch(/without Write/);
      expect(after.actions).toEqual(before.actions);
      expect(after.scope).toBe('account');
      expect(RUN_TIME_GRANTS.map((g) => g.module)).toEqual(['k8s']);
    });

    it('offers no run-time Execute on modules whose steps act through an integration', () => {
      // Database and ticket steps need the integration type's use grant only.
      for (const module of ['database', 'tickets']) expect(byModule.get(module)?.classes).not.toContain('Execute');
    });

    it('leaves every other module as built', () => {
      for (const e of catalog) {
        if (RUN_TIME_GRANTS.some((g) => g.module === e.module)) continue;
        expect(byModule.get(e.module)).toBe(e);
      }
    });

    it('does not override a class an action already supplies', () => {
      const entry = { module: 'k8s', scope: 'account' as const, classes: ['Read', 'Execute'] as PermissionClass[], actions: {} };
      expect(withRunTimeGrants([entry])[0]).toBe(entry);
    });
  });
});
