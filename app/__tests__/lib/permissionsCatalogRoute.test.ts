// The /api/permissions/catalog route: what the Roles & Permissions views are
// allowed to see. Covers the tenant-admin gate and the built-in roles the view
// deliberately hides.
//
// jose (via next-auth/jwt) is browser ESM that jest can't parse, and the route
// pulls it in through rpcGateway's authenticateRequest. Only the auth result
// matters here, so the whole module is stubbed.
const authenticateRequest = jest.fn();
jest.mock('@lib/rpcGateway', () => ({ authenticateRequest: (req: unknown) => authenticateRequest(req) }));

import handler from '@pages/api/permissions/catalog';
import type { NextApiRequest, NextApiResponse } from 'next';

function mockRes() {
  const res: Record<string, unknown> = {};
  res.statusCode = 200;
  res.status = jest.fn((code: number) => {
    res.statusCode = code;
    return res;
  });
  res.json = jest.fn((body: unknown) => {
    res.body = body;
    return res;
  });
  res.setHeader = jest.fn();
  return res as unknown as NextApiResponse & { statusCode: number; body: any };
}

const asTenantAdmin = () => authenticateRequest.mockResolvedValue({ jwt: { roles: ['tenant_admin'] } });

async function call(method = 'GET') {
  const res = mockRes();
  await handler({ method } as NextApiRequest, res);
  return res;
}

beforeEach(() => jest.clearAllMocks());

describe('/api/permissions/catalog', () => {
  it('refuses an unauthenticated caller', async () => {
    authenticateRequest.mockResolvedValue(null);
    expect((await call()).statusCode).toBe(401);
  });

  it('refuses a caller who is not a tenant/super admin', async () => {
    authenticateRequest.mockResolvedValue({ jwt: { roles: ['account_admin'] } });
    // Serving the full action inventory would leak the product's whole operation
    // surface, so this route is admin-only regardless of custom grants.
    expect((await call()).statusCode).toBe(403);
  });

  it('serves the catalog to a tenant admin', async () => {
    asTenantAdmin();
    const res = await call();
    expect(res.statusCode).toBe(200);
    expect(res.body.catalog.length).toBeGreaterThan(50);
    expect(res.body.systemRoles.length).toBeGreaterThan(0);
  });

  it('hides the K8s Namespace built-in roles from the view', async () => {
    asTenantAdmin();
    const keys = (await call()).body.systemRoles.map((r: { systemKey: string }) => r.systemKey);
    expect(keys).not.toContain('k8s_namespace_admin');
    expect(keys).not.toContain('k8s_namespace_admin_readonly');
    // The roles that ARE surfaced must survive the filter.
    expect(keys).toEqual(expect.arrayContaining(['tenant_admin', 'tenant_admin_readonly', 'account_admin', 'account_admin_readonly']));
  });

  it('rejects non-GET', async () => {
    asTenantAdmin();
    expect((await call('POST')).statusCode).toBe(405);
  });
});
