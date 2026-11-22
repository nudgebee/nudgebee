/**
 * @jest-environment node
 */
import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '@pages/api/integrations/discord/install';
import { resolveRequestAuth } from '@lib/sessionToken';

jest.mock('@lib/sessionToken', () => ({ resolveRequestAuth: jest.fn() }));

const mockAuth = resolveRequestAuth as jest.Mock;
const fetchMock = jest.fn();

function makeRes() {
  const res = {
    statusCode: 0,
    body: undefined as any,
    status(code: number) {
      res.statusCode = code;
      return res;
    },
    json(payload: any) {
      res.body = payload;
      return res;
    },
  };
  return res as unknown as NextApiResponse & typeof res;
}

async function install(jwt: Record<string, unknown>) {
  mockAuth.mockResolvedValue({ token: '', jwt: { tenant: { id: 'tenant-1' }, email: 'sam@contoso.com', ...jwt } });
  const res = makeRes();
  await handler({ method: 'POST', headers: {}, body: { bot_token: 'bot' } } as unknown as NextApiRequest, res);
  return res;
}

beforeEach(() => {
  mockAuth.mockReset();
  fetchMock.mockReset();
  fetchMock.mockResolvedValue({ status: 200, json: async () => ({ success: true }) });
  global.fetch = fetchMock as never;
});

// The same rule as before it moved to the shared helper.
describe('POST /api/integrations/discord/install role gate', () => {
  it.each([
    ['a tenant admin', { roles: ['tenant_admin'] }],
    ['a super admin', { roles: [], isSuperAdmin: true }],
    ['the messaging-platforms write permission', { roles: [], permissions: ['messagingplatforms:Write'] }],
  ])('lets %s install', async (_who, jwt) => {
    const res = await install(jwt);
    expect(res.statusCode).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['a read-only admin', { roles: ['tenant_admin_readonly'] }],
    ['a user with no role', {}],
    ['a super admin flag that is not true', { roles: [], isSuperAdmin: 'true' }],
  ])('refuses %s', async (_who, jwt) => {
    const res = await install(jwt);
    expect(res.statusCode).toBe(403);
    expect(res.body).toEqual({ error: 'forbidden' });
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
