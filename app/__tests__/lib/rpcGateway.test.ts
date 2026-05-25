// Tests for the GraphQL → RPC conversion path used by /api/graphql when the
// Hasura bypass is enabled. The handler in src/pages/api/graphql.ts delegates
// every conversion concern to rpcGateway.ts (parseOperation, applySelection,
// buildSessionVariables, tryBypassGraphQL), so exercising those functions
// directly covers the variations the api1/* layer emits in practice:
//
//   - simple scalar args, list args, nested object args (where: {...})
//   - field aliases, multiple top-level fields, mutations
//   - selection-set pruning of upstream JSON
//   - list-input coercion (recommendations_v2.order_by — single object → list)
//   - role gating (forbidden) and super_admin bypass
//   - fall-through reasons (subscription, fragment, unknown action)

import { parse, type SelectionSetNode } from 'graphql';
import type { JWT } from 'next-auth/jwt';

// next-auth/jwt and @lib/internal both transitively load `jose` as ESM, which
// Jest's Babel transform doesn't handle out of the box. rpcGateway.ts only
// uses these in authenticateRequest (which we don't exercise here), so stubs
// are enough.
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decrypt: jest.fn() }));

import { applySelection, buildSessionVariables, parseOperation, tryBypassGraphQL } from '@lib/rpcGateway';

// Pull a top-level field's selection set out of a query string. The bypass
// uses these to prune upstream payloads, so we re-build them the same way the
// real parser does.
function selectionSetOf(query: string, fieldIndex = 0): SelectionSetNode | undefined {
  const doc = parse(query, { noLocation: true });
  const op = doc.definitions.find((d) => d.kind === 'OperationDefinition');
  if (!op || op.kind !== 'OperationDefinition') {
    throw new Error('no operation');
  }
  const sel = op.selectionSet.selections[fieldIndex];
  if (sel.kind !== 'Field') {
    throw new Error('not a field');
  }
  return sel.selectionSet;
}

describe('parseOperation', () => {
  it('parses a simple query with no arguments', () => {
    const result = parseOperation('query GetMe { admin_get_integrations_v2 { items { id } } }', undefined);
    expect(result).toMatchObject({
      ok: true,
      type: 'query',
      fields: [{ name: 'admin_get_integrations_v2', alias: 'admin_get_integrations_v2', input: {} }],
    });
  });

  it('resolves variables to runtime values', () => {
    const query = `
      query GetIntegrations($limit: Int!, $offset: Int!) {
        admin_get_integrations_v2(limit: $limit, offset: $offset) { items { id } }
      }`;
    const result = parseOperation(query, { limit: 25, offset: 50 });
    expect(result).toEqual({
      ok: true,
      type: 'query',
      fields: [expect.objectContaining({ name: 'admin_get_integrations_v2', input: { limit: 25, offset: 50 } })],
    });
  });

  it('parses Int / Float / Boolean / Null / Enum / String literals', () => {
    const query = `
      query Mixed {
        admin_get_integrations_v2(
          limit: 10
          where: { score: { _gt: 1.5 }, active: { _eq: true }, deleted_at: { _is_null: null }, kind: { _eq: ALERT } }
        ) { items { id name } }
      }`;
    const result = parseOperation(query, undefined);
    expect(result.ok).toBe(true);
    if (!result.ok) {
      return;
    }
    expect(result.fields[0].input).toEqual({
      limit: 10,
      where: {
        score: { _gt: 1.5 },
        active: { _eq: true },
        deleted_at: { _is_null: null },
        kind: { _eq: 'ALERT' },
      },
    });
  });

  it('parses ListValue and nested ObjectValue arguments', () => {
    const query = `
      query Listy {
        recommendations_v2(
          where: { account_id: { _in: ["a", "b"] } }
          order_by: [{ column: "created_at", direction: "desc" }]
          columns: ["id", "name"]
        ) { items { id } }
      }`;
    const result = parseOperation(query, undefined);
    expect(result.ok).toBe(true);
    if (!result.ok) {
      return;
    }
    expect(result.fields[0].input).toEqual({
      where: { account_id: { _in: ['a', 'b'] } },
      order_by: [{ column: 'created_at', direction: 'desc' }],
      columns: ['id', 'name'],
    });
  });

  it('honors field aliases on the response key', () => {
    const query = `query A { firstThing: admin_get_integrations_v2 { items { id } } }`;
    const result = parseOperation(query, undefined);
    expect(result.ok).toBe(true);
    if (!result.ok) {
      return;
    }
    expect(result.fields[0]).toMatchObject({ name: 'admin_get_integrations_v2', alias: 'firstThing' });
  });

  it('parses multiple top-level fields in declaration order', () => {
    const query = `
      query Multi {
        a: admin_get_integrations_v2 { items { id } }
        b: admin_get_user_groups_v2 { items { id } }
      }`;
    const result = parseOperation(query, undefined);
    expect(result.ok).toBe(true);
    if (!result.ok) {
      return;
    }
    expect(result.fields.map((f) => f.alias)).toEqual(['a', 'b']);
  });

  it('parses mutation operations', () => {
    const query = `mutation Doit { runbooks_create_playbook(name: "x") { id } }`;
    const result = parseOperation(query, undefined);
    expect(result).toMatchObject({ ok: true, type: 'mutation' });
  });

  it('rejects subscriptions', () => {
    const result = parseOperation('subscription S { admin_get_integrations_v2 { items { id } } }', undefined);
    expect(result).toEqual({ ok: false, reason: 'subscription_unsupported' });
  });

  it('rejects fragment definitions', () => {
    const query = `
      query A { admin_get_integrations_v2 { ...IntegFields } }
      fragment IntegFields on IntegrationResponse { items { id } }
    `;
    const result = parseOperation(query, undefined);
    expect(result).toEqual({ ok: false, reason: 'fragments_unsupported' });
  });

  it('rejects multiple operations in one document', () => {
    const query = `
      query A { admin_get_integrations_v2 { items { id } } }
      query B { admin_get_user_groups_v2 { items { id } } }
    `;
    const result = parseOperation(query, undefined);
    expect(result).toEqual({ ok: false, reason: 'multi_operation' });
  });

  it('rejects inline fragments at the top level', () => {
    const query = `query A { ... on Query { admin_get_integrations_v2 { items { id } } } }`;
    const result = parseOperation(query, undefined);
    expect(result).toEqual({ ok: false, reason: 'non_field_selection_InlineFragment' });
  });

  it('returns parse_failed with detail for invalid GraphQL', () => {
    const result = parseOperation('query { not closed', undefined);
    expect(result.ok).toBe(false);
    if (result.ok) {
      return;
    }
    expect(result.reason).toMatch(/^parse_failed:/);
  });
});

describe('applySelection', () => {
  it('returns scalars and null/undefined as-is', () => {
    expect(applySelection(undefined, 42)).toBe(42);
    expect(applySelection(undefined, 'hi')).toBe('hi');
    const sel = selectionSetOf('query A { a { id } }');
    expect(applySelection(sel, null)).toBeNull();
    expect(applySelection(sel, undefined)).toBeUndefined();
  });

  it('keeps only fields named in the selection and drops extras', () => {
    const sel = selectionSetOf('query A { a { id name } }');
    const out = applySelection(sel, { id: '1', name: 'x', secret: 'leak', extra: 'leak' });
    expect(out).toEqual({ id: '1', name: 'x' });
  });

  it('skips fields the upstream omitted (no synthetic null)', () => {
    const sel = selectionSetOf('query A { a { id name } }');
    expect(applySelection(sel, { id: '1' })).toEqual({ id: '1' });
  });

  it('uses the alias as the output key', () => {
    const sel = selectionSetOf('query A { a { renamed: id } }');
    expect(applySelection(sel, { id: '1' })).toEqual({ renamed: '1' });
  });

  it('prunes each element of an array', () => {
    const sel = selectionSetOf('query A { a { id } }');
    const out = applySelection(sel, [
      { id: '1', drop: 'x' },
      { id: '2', drop: 'y' },
    ]);
    expect(out).toEqual([{ id: '1' }, { id: '2' }]);
  });

  it('recurses into nested objects', () => {
    const sel = selectionSetOf('query A { a { items { id name } page { total } } }');
    const out = applySelection(sel, {
      items: [{ id: '1', name: 'a', extra: 1 }],
      page: { total: 10, hidden: true },
      ignored: 'x',
    });
    expect(out).toEqual({ items: [{ id: '1', name: 'a' }], page: { total: 10 } });
  });
});

describe('buildSessionVariables', () => {
  const baseJwt: Partial<JWT> = {
    id: 'user-1',
    sub: 'sub-1',
    tenant: { id: 'tenant-1' } as unknown as JWT['tenant'],
    roles: ['account_admin', 'k8s_namespace_admin'],
    accountIds: ['acc-1', 'acc-2'],
    readOnlyAccountIds: ['acc-3'],
    namespacedAccountIds: [],
    namespacedReadOnlyAccountIds: [],
  };

  it('picks the highest-priority role and populates snake_case session vars', () => {
    const vars = buildSessionVariables(baseJwt as JWT);
    expect(vars.role).toBe('account_admin');
    expect(vars.allowed_roles).toEqual(['account_admin', 'k8s_namespace_admin']);
    expect(vars.user_id).toBe('user-1');
    expect(vars.tenant_id).toBe('tenant-1');
    // Dead Hasura-RLS account-ids fields were dropped in the wire cleanup
    // (#30920) — they had zero backend consumers. SessionVariables now only
    // carries the 4 fields above.
    expect((vars as unknown as Record<string, unknown>)['user_account_ids']).toBeUndefined();
  });

  it('appends super_admin to allowed-roles for super-admin sessions', () => {
    const vars = buildSessionVariables({ ...baseJwt, isSuperAdmin: true } as JWT);
    expect(vars.allowed_roles).toEqual(['account_admin', 'k8s_namespace_admin', 'super_admin']);
  });

  it('falls back to tenant_admin_readonly when roles is empty', () => {
    const vars = buildSessionVariables({ ...baseJwt, roles: [] } as JWT);
    expect(vars.role).toBe('tenant_admin_readonly');
    expect(vars.allowed_roles).toEqual([]);
  });

  it('uses sub when id is absent', () => {
    const { id: _id, ...rest } = baseJwt;
    const vars = buildSessionVariables(rest as JWT);
    expect(vars.user_id).toBe('sub-1');
  });

  it('defaults user_id to empty string when both id and sub are missing', () => {
    const { id: _id, sub: _sub, ...rest } = baseJwt;
    const vars = buildSessionVariables(rest as JWT);
    expect(vars.user_id).toBe('');
  });
});

describe('tryBypassGraphQL', () => {
  const originalFetch = global.fetch;
  const originalEnv = { ...process.env };

  const adminJwt: JWT = {
    id: 'user-1',
    sub: 'user-1',
    tenant: { id: 'tenant-1' },
    roles: ['tenant_admin'],
    accountIds: ['acc-1'],
    readOnlyAccountIds: [],
    namespacedAccountIds: [],
    namespacedReadOnlyAccountIds: [],
  } as unknown as JWT;

  beforeEach(() => {
    process.env.SERVICE_API_SERVER_URL = 'http://test-api';
  });

  afterEach(() => {
    global.fetch = originalFetch;
    process.env = { ...originalEnv };
  });

  function mockFetchOnce(payload: unknown, init: { ok?: boolean; status?: number; contentType?: string } = {}): jest.Mock {
    const ok = init.ok ?? true;
    const status = init.status ?? 200;
    const contentType = init.contentType ?? 'application/json';
    const fetchMock = jest.fn().mockResolvedValue({
      ok,
      status,
      headers: { get: (k: string) => (k.toLowerCase() === 'content-type' ? contentType : null) },
      json: async () => payload,
      text: async () => JSON.stringify(payload),
    });
    (global as { fetch: unknown }).fetch = fetchMock;
    return fetchMock;
  }

  it('falls through when the operation is a subscription (Hasura proxy still owns these)', async () => {
    const result = await tryBypassGraphQL({
      query: 'subscription S { admin_get_integrations_v2 { items { id } } }',
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });
    expect(result).toEqual({ handled: false, reason: 'subscription_unsupported' });
  });

  it('falls through for unknown actions instead of returning a partial response', async () => {
    const result = await tryBypassGraphQL({
      query: 'query A { not_a_real_action_xyz { id } }',
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });
    expect(result).toEqual({ handled: false, reason: 'unknown_action:not_a_real_action_xyz' });
  });

  it('forwards a query, prunes the upstream payload, and returns it under the field alias', async () => {
    const fetchMock = mockFetchOnce({
      items: [{ id: '1', name: 'a', secret: 'leak' }],
      page: { total: 1, hidden: true },
    });

    const result = await tryBypassGraphQL({
      query: `
        query A {
          aliased: admin_get_integrations_v2(limit: 10) {
            items { id name }
            page { total }
          }
        }`,
      variables: undefined,
      jwt: adminJwt,
      clientAuthorization: 'Bearer abc',
      traceparent: 'tp',
      requestId: 'rid',
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('http://test-api/hasura/query');
    const parsedBody = JSON.parse(init.body);
    expect(parsedBody.action.name).toBe('admin_get_integrations_v2');
    expect(parsedBody.input).toEqual({ limit: 10 });
    expect(parsedBody.session_variables.role).toBe('tenant_admin');
    expect(init.headers.Authorization).toBe('Bearer abc'); // forward_client_headers: true
    expect(init.headers['x-tenant-id']).toBe('tenant-1');
    expect(init.headers.traceparent).toBe('tp');

    expect(result).toEqual({
      handled: true,
      status: 200,
      body: {
        data: {
          aliased: { items: [{ id: '1', name: 'a' }], page: { total: 1 } },
        },
      },
    });
  });

  it('coerces a single value into a list when the schema declares the arg as a list', async () => {
    // recommendations_v2.order_by is `[QuerySortByRequest!]` in actions.graphql,
    // but frontend callers pass a single object relying on GraphQL coercion.
    // The bypass has to apply the same coercion so the upstream Go handler
    // can decode the input.
    const fetchMock = mockFetchOnce({ items: [] });

    await tryBypassGraphQL({
      query: `
        query R {
          recommendations_v2(order_by: { column: "created_at", direction: "desc" }) {
            items { id }
          }
        }`,
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body.input.order_by).toEqual([{ column: 'created_at', direction: 'desc' }]);
  });

  it('does not wrap when the value is already a list', async () => {
    const fetchMock = mockFetchOnce({ items: [] });

    await tryBypassGraphQL({
      query: `
        query R {
          recommendations_v2(order_by: [{ column: "id", direction: "asc" }]) {
            items { id }
          }
        }`,
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body.input.order_by).toEqual([{ column: 'id', direction: 'asc' }]);
  });

  it('returns a forbidden error in the GraphQL errors array when role is not permitted', async () => {
    const fetchMock = jest.fn();
    (global as { fetch: unknown }).fetch = fetchMock;

    const viewerJwt = { ...adminJwt, roles: ['viewer'] } as JWT;
    const result = await tryBypassGraphQL({
      query: 'query R { recommendations_v2 { items { id } } }',
      variables: undefined,
      jwt: viewerJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    expect(fetchMock).not.toHaveBeenCalled(); // role gate runs before any upstream call
    expect(result.handled).toBe(true);
    if (!result.handled) {
      return;
    }
    expect(result.body.data).toEqual({ recommendations_v2: null });
    expect(result.body.errors).toHaveLength(1);
    expect(result.body.errors?.[0]).toMatchObject({
      path: ['recommendations_v2'],
      extensions: { code: 'FORBIDDEN', role: 'viewer' },
    });
    expect(result.body.errors?.[0].message).toMatch(/Role 'viewer' is not permitted/);
  });

  it('lets super_admin sessions bypass per-action role gates', async () => {
    const fetchMock = mockFetchOnce({ items: [] });
    const superJwt = { ...adminJwt, roles: ['some_role_not_allowed'], isSuperAdmin: true } as JWT;

    const result = await tryBypassGraphQL({
      query: 'query R { recommendations_v2 { items { id } } }',
      variables: undefined,
      jwt: superJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(result.handled).toBe(true);
  });

  it('allows the call if the user holds ANY allowed role, not just the active/default one', async () => {
    // User has [tenant_admin_readonly, account_admin]. pickDefaultRole
    // picks tenant_admin_readonly (higher priority). admin_get_integrations_v2
    // allows tenant_admin + tenant_admin_readonly, so the active-role gate
    // would have passed here — but the regression we care about is actions
    // whose allow-list does NOT contain the active role but DOES contain a
    // role the user holds. Use ai_create_agent (allows tenant_admin +
    // account_admin only) — user's default `tenant_admin_readonly` is not
    // in the list, but `account_admin` is. Pre-fix this 403'd; post-fix it
    // should pass through to the upstream.
    const fetchMock = mockFetchOnce({ id: 'a-1' });
    const dualRoleJwt = {
      ...adminJwt,
      roles: ['tenant_admin_readonly', 'account_admin'],
    } as JWT;

    const result = await tryBypassGraphQL({
      query: 'mutation C { ai_create_agent(name: "x") { id } }',
      variables: undefined,
      jwt: dualRoleJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(result.handled).toBe(true);
  });

  it('reports upstream errors per-field instead of failing the whole operation', async () => {
    const fetchMock = jest.fn().mockResolvedValue({
      ok: false,
      status: 502,
      headers: { get: () => 'application/json' },
      json: async () => ({ error: 'boom' }),
      text: async () => '{"error":"boom"}',
    });
    (global as { fetch: unknown }).fetch = fetchMock;

    const result = await tryBypassGraphQL({
      query: 'query A { admin_get_integrations_v2(limit: 1) { items { id } } }',
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    expect(result.handled).toBe(true);
    if (!result.handled) {
      return;
    }
    expect(result.body.data).toEqual({ admin_get_integrations_v2: null });
    expect(result.body.errors?.[0]).toMatchObject({
      path: ['admin_get_integrations_v2'],
      message: expect.stringMatching(/^Upstream error from admin_get_integrations_v2 at .* \(HTTP 502\)/),
    });
  });

  it('fans out multiple top-level fields in parallel and merges results by alias', async () => {
    const fetchMock = jest.fn();
    fetchMock
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => 'application/json' },
        json: async () => ({ items: [{ id: '1' }] }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        headers: { get: () => 'application/json' },
        json: async () => ({ items: [{ id: '2' }] }),
      });
    (global as { fetch: unknown }).fetch = fetchMock;

    const result = await tryBypassGraphQL({
      query: `
        query Two {
          a: admin_get_integrations_v2 { items { id } }
          b: admin_get_user_groups_v2 { items { id } }
        }`,
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(result.handled).toBe(true);
    if (!result.handled) {
      return;
    }
    expect(result.body.data).toEqual({ a: { items: [{ id: '1' }] }, b: { items: [{ id: '2' }] } });
    expect(result.body.errors).toBeUndefined();
  });

  it('forwards the original query string as request_query so handlers parsing it still work', async () => {
    const fetchMock = mockFetchOnce({ items: [] });
    const query = 'query A { admin_get_integrations_v2(limit: 5) { items { id } } }';

    await tryBypassGraphQL({
      query,
      variables: undefined,
      jwt: adminJwt,
      traceparent: 'tp',
      requestId: 'rid',
    });

    const body = JSON.parse(fetchMock.mock.calls[0][1].body);
    expect(body.request_query).toBe(query);
  });

  // ---- Required-variable validation -------------------------------------
  // Hasura rejects calls missing a required ($x: String!) variable at parse
  // time. Without this guard the bypass would silently send `undefined`,
  // which JSON-stringifies away — the upstream Go decoder would either
  // zero-value the field or return a confusing 400. Falling through to the
  // Hasura proxy preserves Hasura's standard error format.
  describe('required-variable validation (#1)', () => {
    it('falls through when a required variable is not provided', async () => {
      const result = await tryBypassGraphQL({
        query: `
          query A($accountId: String!) {
            admin_get_integrations_v2(limit: 1) { items { id } }
          }`,
        variables: {},
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      expect(result).toEqual({ handled: false, reason: 'missing_required_variable:$accountId' });
    });

    it('falls through when a required variable is explicitly undefined', async () => {
      const result = await tryBypassGraphQL({
        query: `
          query A($accountId: String!) {
            admin_get_integrations_v2(limit: 1) { items { id } }
          }`,
        variables: { accountId: undefined },
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      expect(result.handled).toBe(false);
    });

    it('accepts explicit null when the variable type is nullable', async () => {
      // $where: SomeWhere (no `!`) → nullable, so caller is allowed to omit
      // it. Bypass must not reject this case.
      const fetchMock = mockFetchOnce({ items: [] });
      const result = await tryBypassGraphQL({
        query: `
          query A($where: IntegrationWhereRequest) {
            admin_get_integrations_v2(where: $where) { items { id } }
          }`,
        variables: {},
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(result.handled).toBe(true);
    });
  });

  // ---- Variable default values ------------------------------------------
  // GraphQL spec: when an operation declares `$x: Int = 10` and the caller
  // omits `x`, the default is substituted. Without this, the bypass silently
  // drops the field and the upstream behaves as if it was unset.
  describe('variable default values (#5)', () => {
    it('substitutes a default value when the caller omits the variable', async () => {
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A($limit: Int = 25) {
            admin_get_integrations_v2(limit: $limit) { items { id } }
          }`,
        variables: {},
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.limit).toBe(25);
    });

    it('lets a caller-provided value override the default', async () => {
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A($limit: Int = 25) {
            admin_get_integrations_v2(limit: $limit) { items { id } }
          }`,
        variables: { limit: 5 },
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.limit).toBe(5);
    });

    it('substitutes object defaults', async () => {
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A($where: IntegrationWhereRequest = {account_id: {_eq: "acc-default"}}) {
            admin_get_integrations_v2(where: $where) { items { id } }
          }`,
        variables: {},
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where).toEqual({ account_id: { _eq: 'acc-default' } });
    });
  });

  // ---- Nested list-input coercion ---------------------------------------
  // Hasura's spec-mandated single-value→list coercion runs recursively
  // through input types. Top-level coercion is not enough: `_in`, `_not_in`,
  // `_and`, `_or` and similar operators are all list-typed inside Where
  // request inputs, and frontend code paths that forget the Array.isArray
  // guard would silently send malformed input upstream.
  describe('nested list-input coercion (#2)', () => {
    it('wraps a scalar passed to nested `_in` into a single-element list', async () => {
      // IntegrationWhereRequest.name is QueryWhereStringRequest, whose `_in`
      // field is `[String!]`. Caller passes a bare string; bypass must wrap.
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A {
            admin_get_integrations_v2(where: { name: { _in: "slack" } }) {
              items { id }
            }
          }`,
        variables: undefined,
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where.name._in).toEqual(['slack']);
    });

    it('wraps a scalar variable passed through to nested `_in`', async () => {
      const fetchMock = mockFetchOnce({ items: [] });
      // $name is declared `String` (scalar) but the schema field `_in:
      // [String!]` is a list. Hasura coerces; the bypass must too.
      await tryBypassGraphQL({
        query: `
          query A($name: String) {
            admin_get_integrations_v2(where: { name: { _in: $name } }) {
              items { id }
            }
          }`,
        variables: { name: 'slack' },
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where.name._in).toEqual(['slack']);
    });

    it('does not double-wrap when nested `_in` is already a list', async () => {
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A {
            admin_get_integrations_v2(where: { name: { _in: ["a", "b"] } }) {
              items { id }
            }
          }`,
        variables: undefined,
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where.name._in).toEqual(['a', 'b']);
    });

    it('wraps a single object passed to nested `_and`/`_or` (list of WhereRequest)', async () => {
      // IntegrationWhereRequest._and is `[IntegrationWhereRequest]`. Hasura
      // accepts a single object; bypass must wrap it as a one-element list.
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A {
            admin_get_integrations_v2(
              where: { _and: { name: { _eq: "slack" } } }
            ) {
              items { id }
            }
          }`,
        variables: undefined,
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where._and).toEqual([{ name: { _eq: 'slack' } }]);
    });

    it('preserves null at nested list positions (does not coerce null to [null])', async () => {
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A {
            admin_get_integrations_v2(where: { name: { _in: null } }) {
              items { id }
            }
          }`,
        variables: undefined,
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where.name._in).toBeNull();
    });

    it('passes unknown nested fields through without modification', async () => {
      // Forward-compat: when actions.graphql is a step behind the upstream,
      // a field the schema doesn't know about must not be dropped or wrapped.
      const fetchMock = mockFetchOnce({ items: [] });
      await tryBypassGraphQL({
        query: `
          query A {
            admin_get_integrations_v2(where: { future_unknown_field: "raw" }) {
              items { id }
            }
          }`,
        variables: undefined,
        jwt: adminJwt,
        traceparent: 'tp',
        requestId: 'rid',
      });
      const body = JSON.parse(fetchMock.mock.calls[0][1].body);
      expect(body.input.where.future_unknown_field).toBe('raw');
    });
  });
});
