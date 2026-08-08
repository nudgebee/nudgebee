// Dynamic-RBAC enforcement matrix — the exhaustive companion to
// permissionCatalog.test.ts (which only asserts that actions CLASSIFY).
//
// This suite asserts what a grant actually ADMITS at the gateway, for every
// action in actions.yaml (+ the EE overlay) against every (module, class) cell
// in the permission catalog. It is the offline half of the RBAC test plan: the
// Playwright suite (app-e2e-tests/tests/admin/roles/) drives the same matrix
// through the real UI, but cannot enumerate 60+ modules x 3 classes per run.
//
// Everything is derived from the live action inventory, so a new action / module
// is covered the moment it lands — there are no hardcoded module lists.
//
// jose (via next-auth/jwt) ships browser ESM that jest can't parse, and
// @lib/internal / @lib/sessionRevocation pull it in transitively. None of them
// participate in actionHasCustomGrant, so they are stubbed at the module edge.
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import { actionHasCustomGrant, ACCOUNT_ENFORCED_ACTIONS } from '@lib/rpcGateway';
import { loadRpcRoutes, type RpcRoute } from '@lib/rpcRoutes';
import { buildCatalog, classifyAction, type PermissionClass } from '@lib/permissionCatalog';
import { deriveSystemRoleGrants, DERIVED_SYSTEM_ROLE_KEYS } from '@lib/systemRoleGrants';

const SCOPED = 'scoped:';
const CLASSES: PermissionClass[] = ['Read', 'Write', 'Execute'];

const routes = loadRpcRoutes();
const actionNames = Object.keys(routes);

// Every action that a custom role CAN be granted (route.permission set), and
// every action that is deliberately non-grantable (classifyAction → null).
const grantable = actionNames.filter((n) => routes[n].permission);
const nonGrantable = actionNames.filter((n) => !routes[n].permission);

const catalog = buildCatalog(actionNames);
// The full grant surface an admin can tick in the Roles editor: one entry per
// module x class checkbox that actually renders.
const allCells: string[] = catalog.flatMap((e) => e.classes.map((c) => `${e.module}:${c}`));

const moduleOf = (perm: string) => perm.split(':')[0];
const classOf = (perm: string) => perm.split(':')[1] as PermissionClass;
const isQueryEngine = (r: RpcRoute) => r.handler.endsWith('/rpc/query');

// A module guaranteed to differ from `module`, used for cross-module isolation.
const allModules = [...new Set(catalog.map((e) => e.module))];
const otherModule = (module: string) => allModules.find((m) => m !== module)!;

describe('RBAC enforcement matrix — inventory', () => {
  it('loads the full action inventory', () => {
    expect(actionNames.length).toBeGreaterThan(400);
  });

  it('renders a non-trivial grant surface (module x class cells)', () => {
    expect(allModules.length).toBeGreaterThan(50);
    expect(allCells.length).toBeGreaterThan(100);
  });

  // Whatever the editor renders must be reachable, and whatever is reachable
  // must be renderable — otherwise an admin either ticks a dead checkbox or
  // can never grant a live action.
  it('every grantable action maps to a cell the Roles editor renders', () => {
    const cells = new Set(allCells);
    const orphans = grantable.filter((n) => !cells.has(routes[n].permission!));
    expect(orphans).toEqual([]);
  });

  it('every rendered cell is backed by at least one action', () => {
    const backed = new Set(grantable.map((n) => routes[n].permission!));
    expect(allCells.filter((c) => !backed.has(c))).toEqual([]);
  });
});

describe('RBAC enforcement matrix — tenant-global grants', () => {
  // The whole point of a grant: holding exactly the action's key admits it.
  it.each(CLASSES)('admits every %s-classified action for the exact grant', (cls) => {
    const subset = grantable.filter((n) => classOf(routes[n].permission!) === cls);
    expect(subset.length).toBeGreaterThan(0);
    const denied = subset.filter((n) => !actionHasCustomGrant(n, routes[n], [routes[n].permission!]));
    expect(denied).toEqual([]);
  });

  it('denies every action when the caller holds no grants', () => {
    const admitted = actionNames.filter((n) => actionHasCustomGrant(n, routes[n], []));
    expect(admitted).toEqual([]);
  });

  // Write implies Read (documented relaxation): a `<module>:Write` holder can
  // perform the module's reads without a separate Read tick.
  it('Write grant admits the same module’s Read actions', () => {
    const reads = grantable.filter((n) => classOf(routes[n].permission!) === 'Read');
    const denied = reads.filter((n) => !actionHasCustomGrant(n, routes[n], [`${moduleOf(routes[n].permission!)}:Write`]));
    expect(denied).toEqual([]);
  });

  // ...but the implication is one-directional. Read must never reach Write.
  it('Read grant never admits the same module’s Write actions', () => {
    const writes = grantable.filter((n) => classOf(routes[n].permission!) === 'Write');
    const leaked = writes.filter((n) => actionHasCustomGrant(n, routes[n], [`${moduleOf(routes[n].permission!)}:Read`]));
    expect(leaked).toEqual([]);
  });

  // Execute is exact on both sides — neither Read nor Write reaches it, and it
  // reaches nothing else.
  it('Read/Write grants never admit the same module’s Execute actions', () => {
    const execs = grantable.filter((n) => classOf(routes[n].permission!) === 'Execute');
    expect(execs.length).toBeGreaterThan(0);
    const leaked = execs.filter((n) => {
      const m = moduleOf(routes[n].permission!);
      return actionHasCustomGrant(n, routes[n], [`${m}:Read`]) || actionHasCustomGrant(n, routes[n], [`${m}:Write`]);
    });
    expect(leaked).toEqual([]);
  });

  it('Execute grant never admits the same module’s Write actions', () => {
    const writes = grantable.filter((n) => classOf(routes[n].permission!) === 'Write');
    const leaked = writes.filter((n) => actionHasCustomGrant(n, routes[n], [`${moduleOf(routes[n].permission!)}:Execute`]));
    expect(leaked).toEqual([]);
  });

  // Module isolation: a grant on one module must not spill into any other.
  it('a grant on a different module never admits the action', () => {
    const leaked = grantable.filter((n) => {
      const perm = routes[n].permission!;
      const foreign = otherModule(moduleOf(perm));
      return CLASSES.some((c) => actionHasCustomGrant(n, routes[n], [`${foreign}:${c}`]));
    });
    expect(leaked).toEqual([]);
  });

  // Extra grants must not change the verdict for an action the caller already
  // holds — the check is set-membership, not first-match.
  it('is unaffected by unrelated grants held alongside the matching one', () => {
    const noise = allCells.slice(0, 25);
    const denied = grantable.filter((n) => !actionHasCustomGrant(n, routes[n], [...noise, routes[n].permission!]));
    expect(denied).toEqual([]);
  });
});

describe('RBAC enforcement matrix — non-grantable actions fail closed', () => {
  it('has a non-empty non-grantable set', () => {
    expect(nonGrantable.length).toBeGreaterThan(0);
  });

  // The strongest statement in this file: no combination of every checkbox an
  // admin can tick — tenant-global OR account-scoped — admits a non-grantable
  // action. Privilege administration (userroles_*), pre-auth signup, session
  // plumbing and cross-tenant listing stay built-in-role-only.
  it('no cell in the entire catalog admits a non-grantable action', () => {
    const everyGrant = [...allCells, ...allCells.map((c) => SCOPED + c)];
    const leaked = nonGrantable.filter((n) => actionHasCustomGrant(n, routes[n], everyGrant));
    expect(leaked).toEqual([]);
  });

  it('keeps the privilege-administration surface non-grantable', () => {
    // Named explicitly so removing one from NON_GRANTABLE_MODULES fails here and
    // not only in a matrix that would still be "green" with one fewer entry.
    const privileged = ['userroles_sync', 'roles_list', 'tenant_list_all'];
    const stillPresent = privileged.filter((n) => routes[n]);
    expect(stillPresent.length).toBeGreaterThan(0);
    for (const n of stillPresent) {
      expect(routes[n].permission).toBeUndefined();
    }
  });
});

describe('RBAC enforcement matrix — account-scoped grants', () => {
  // Account-scoped grants come from "Group G holds Role R on Account A". They
  // are only safe where the account is re-enforced downstream: the query engine
  // (every /rpc/query read) plus the explicit verified-handler ledger.
  const scopedEligible = grantable.filter((n) => isQueryEngine(routes[n]) || ACCOUNT_ENFORCED_ACTIONS.has(n));
  const scopedIneligible = grantable.filter((n) => !isQueryEngine(routes[n]) && !ACCOUNT_ENFORCED_ACTIONS.has(n));

  it('has both eligible and ineligible actions to test', () => {
    expect(scopedEligible.length).toBeGreaterThan(0);
    expect(scopedIneligible.length).toBeGreaterThan(0);
  });

  it('admits query-engine + ledger actions for a scoped grant', () => {
    const denied = scopedEligible.filter((n) => !actionHasCustomGrant(n, routes[n], [SCOPED + routes[n].permission!]));
    expect(denied).toEqual([]);
  });

  // The fail-closed half: a scoped grant must NEVER admit a custom-handler
  // action, because the gateway cannot know which account that handler will
  // touch. This is what stops an account-scoped role acting tenant-wide.
  it('never admits custom-handler actions for a scoped grant', () => {
    const leaked = scopedIneligible.filter((n) => actionHasCustomGrant(n, routes[n], [SCOPED + routes[n].permission!]));
    expect(leaked).toEqual([]);
  });

  it('applies Write⇒Read to scoped grants on eligible actions only', () => {
    const eligibleReads = scopedEligible.filter((n) => classOf(routes[n].permission!) === 'Read');
    const denied = eligibleReads.filter((n) => !actionHasCustomGrant(n, routes[n], [`${SCOPED}${moduleOf(routes[n].permission!)}:Write`]));
    expect(denied).toEqual([]);

    const ineligibleReads = scopedIneligible.filter((n) => classOf(routes[n].permission!) === 'Read');
    const leaked = ineligibleReads.filter((n) => actionHasCustomGrant(n, routes[n], [`${SCOPED}${moduleOf(routes[n].permission!)}:Write`]));
    expect(leaked).toEqual([]);
  });

  it('never lets a scoped Read grant reach a Write/Execute action', () => {
    const mutations = grantable.filter((n) => classOf(routes[n].permission!) !== 'Read');
    const leaked = mutations.filter((n) => actionHasCustomGrant(n, routes[n], [`${SCOPED}${moduleOf(routes[n].permission!)}:Read`]));
    expect(leaked).toEqual([]);
  });

  it('never lets a scoped grant on another module admit the action', () => {
    const leaked = grantable.filter((n) => {
      const foreign = otherModule(moduleOf(routes[n].permission!));
      return CLASSES.some((c) => actionHasCustomGrant(n, routes[n], [`${SCOPED}${foreign}:${c}`]));
    });
    expect(leaked).toEqual([]);
  });

  // relay_forward_request is the one non-query-engine action on the ledger; it
  // is the live-cluster proxy, so a regression here silently widens k8s access.
  it('keeps relay_forward_request on the verified-handler ledger as k8s:Read', () => {
    expect(routes['relay_forward_request']?.permission).toBe('k8s:Read');
    expect(actionHasCustomGrant('relay_forward_request', routes['relay_forward_request'], ['scoped:k8s:Read'])).toBe(true);
    expect(actionHasCustomGrant('relay_forward_request', routes['relay_forward_request'], ['scoped:k8s:Write'])).toBe(true);
    expect(actionHasCustomGrant('relay_forward_request', routes['relay_forward_request'], ['scoped:cloud:Read'])).toBe(false);
  });
});

describe('RBAC enforcement matrix — built-in (system) roles', () => {
  const actions = actionNames.map((n) => ({ name: n, permissions: [...routes[n].allowedRoles].map((role) => ({ role })) }));
  const derived = deriveSystemRoleGrants(actions);
  const cells = new Set(allCells);

  it('derives a grant set for every built-in role', () => {
    for (const key of DERIVED_SYSTEM_ROLE_KEYS) {
      expect(derived.filter((r) => r.systemKey === key).length).toBeGreaterThan(0);
    }
  });

  // The read-only built-in viewer renders these rows against the same catalog
  // the editor uses; a grant outside the catalog would render an empty row.
  it('every derived built-in grant is a cell in the catalog', () => {
    const orphans = derived.filter((r) => !cells.has(`${r.module}:${r.class}`));
    expect(orphans).toEqual([]);
  });

  // The "*_readonly" built-in roles are NOT read-only in the strict sense —
  // actions.yaml grants them a handful of mutation-classified actions, so the
  // built-in role viewer legitimately shows Write/Execute ticks for them. Most
  // are self-service (own API tokens, own default tenant, chat conversations,
  // ticket creation) and several are re-gated in their handler
  // (users_update_status → canAdministerUsers).
  //
  // This is a CHARACTERIZATION pin, not an aspiration: it freezes the current
  // mutation surface of each read-only role so that granting one a NEW mutation
  // fails here and has to be justified, rather than sliding in unnoticed.
  const readonlyMutationModules: Record<string, string[]> = {
    tenant_admin_readonly: [
      'ai_conversations:Write',
      'ai_generation:Execute',
      'ai_memory:Execute',
      'ai_memory:Write',
      'ai_misc:Execute',
      'ai_rca:Write',
      'applications:Execute',
      'events:Execute',
      'llm:Write',
      'ml:Execute',
      'ownership:Execute',
      'tickets:Write',
      'users:Write',
      'workflows:Execute',
    ],
    account_admin_readonly: [
      'ai_conversations:Write',
      'ai_generation:Execute',
      'ai_memory:Execute',
      'ai_memory:Write',
      'ai_misc:Execute',
      'ai_rca:Write',
      'applications:Execute',
      'events:Execute',
      'llm:Write',
      'ml:Execute',
      'ownership:Execute',
      'recommendations:Execute',
      'tickets:Write',
      'users:Write',
      'workflows:Execute',
    ],
    k8s_namespace_admin_readonly: [
      'ai_conversations:Write',
      'ai_generation:Execute',
      'ai_misc:Execute',
      'ai_rca:Write',
      'applications:Execute',
      'llm:Write',
      'ml:Execute',
      'ownership:Execute',
      'tickets:Write',
      'users:Write',
      'workflows:Execute',
    ],
  };

  it.each(Object.keys(readonlyMutationModules))('%s mutation surface is unchanged', (key) => {
    const actual = [...new Set(derived.filter((r) => r.systemKey === key && r.class !== 'Read').map((r) => `${r.module}:${r.class}`))].sort();
    expect(actual).toEqual(readonlyMutationModules[key].slice().sort());
  });

  it('tenant_admin is a superset of tenant_admin_readonly', () => {
    const admin = new Set(derived.filter((r) => r.systemKey === 'tenant_admin').map((r) => `${r.module}:${r.class}`));
    const ro = derived.filter((r) => r.systemKey === 'tenant_admin_readonly').map((r) => `${r.module}:${r.class}`);
    expect(ro.filter((k) => !admin.has(k))).toEqual([]);
  });
});

describe('RBAC enforcement matrix — classifier stability', () => {
  // classifyAction is the shared source for the gate, the catalog and the
  // built-in derivation. Any disagreement between them is an authorization bug,
  // so pin the contract rather than trusting three independent call sites.
  it('route.permission equals classifyAction for every action', () => {
    const mismatched = actionNames.filter((n) => {
      const c = classifyAction(n);
      return routes[n].permission !== (c ? `${c.module}:${c.class}` : undefined);
    });
    expect(mismatched).toEqual([]);
  });

  it('only ever emits Read | Write | Execute', () => {
    const bad = grantable.filter((n) => !CLASSES.includes(classOf(routes[n].permission!)));
    expect(bad).toEqual([]);
  });
});
