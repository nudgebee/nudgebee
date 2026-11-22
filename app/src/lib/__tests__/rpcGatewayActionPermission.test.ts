/**
 * forwardAction must send the action's classified grant key as action.permission,
 * so upstream handlers (Go RequestContext.CanAccessAccount / CanAccessTenant)
 * check custom grants against the same key the gateway admitted the call on.
 */
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import { forwardAction } from '@lib/rpcGateway';
import { classifyAction, permissionKey } from '@lib/permissionCatalog';

const base = {
  params: {},
  tenantId: 't1',
  userId: 'u1',
  traceparent: '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01',
  requestId: 'r1',
  sessionVariables: { role: 'tenant_admin', allowed_roles: ['tenant_admin'], permissions: [], user_id: 'u1', tenant_id: 't1' },
};

describe('forwardAction action.permission', () => {
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

  const sentAction = () => JSON.parse(fetchMock.mock.calls[0][1].body as string).action as { name: string; permission: string };

  it('sends the classified grant key of a grantable action', async () => {
    const classified = classifyAction('metrics_list');
    expect(classified).not.toBeNull();
    await forwardAction({ ...base, method: 'metrics_list' });
    expect(sentAction()).toEqual({ name: 'metrics_list', permission: permissionKey(classified!.module, classified!.class) });
  });

  it('sends an empty key for a non-grantable action', async () => {
    expect(classifyAction('customroles_create')).toBeNull();
    await forwardAction({ ...base, method: 'customroles_create' });
    expect(sentAction()).toEqual({ name: 'customroles_create', permission: '' });
  });
});
