/**
 * forwardAction must forward every role the caller holds as x-user-roles, so
 * the AI Gateway gates on roles rather than the single x-user-role label.
 */
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import { forwardAction } from '@lib/rpcGateway';

const base = {
  method: 'metrics_list',
  params: {},
  tenantId: 't1',
  userId: 'u1',
  traceparent: '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01',
  requestId: 'r1',
};

const vars = (allowed_roles: string[]) => ({ role: 'tenant_admin_readonly', allowed_roles, permissions: [], user_id: 'u1', tenant_id: 't1' });

describe('forwardAction x-user-roles header', () => {
  const originalFetch = global.fetch;
  let fetchMock: jest.Mock;
  beforeEach(() => {
    process.env.SERVICE_API_SERVER_URL = 'http://api-server';
    fetchMock = jest.fn(() => Promise.resolve(new Response('{}', { status: 200, headers: { 'content-type': 'application/json' } })));
    global.fetch = fetchMock as unknown as typeof fetch;
  });
  afterEach(() => {
    global.fetch = originalFetch;
  });

  const sentHeaders = () => fetchMock.mock.calls[0][1].headers as Record<string, string>;

  it('forwards all allowed roles comma-joined', async () => {
    await forwardAction({ ...base, sessionVariables: vars(['tenant_admin_readonly', 'super_admin']) });
    expect(sentHeaders()['x-user-roles']).toBe('tenant_admin_readonly,super_admin');
  });

  it('forwards the "-" sentinel for an empty list', async () => {
    // A proxy may drop an empty header; "-" keeps the gateway off the x-user-role label fallback.
    // Tenant-agnostic action: the only kind a caller with no roles can reach.
    await forwardAction({ ...base, method: 'users_list_tenants', sessionVariables: vars([]) });
    expect(sentHeaders()['x-user-roles']).toBe('-');
  });
});
