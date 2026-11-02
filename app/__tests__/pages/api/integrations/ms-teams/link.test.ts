/**
 * @jest-environment node
 */
import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '@pages/api/integrations/ms-teams/link';
import { resolveRequestAuth } from '@lib/sessionToken';

jest.mock('@lib/sessionToken', () => ({ resolveRequestAuth: jest.fn() }));

const mockAuth = resolveRequestAuth as jest.Mock;
const fetchMock = jest.fn();
const TENANT = 'aaaaaaaa-1111-4111-8111-111111111111';

function makeRes() {
  const res = {
    statusCode: 0,
    body: undefined as any,
    headers: {} as Record<string, string>,
    setHeader(name: string, value: string) {
      res.headers[name] = value;
      return res;
    },
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

function post(body: any = { token: 'link-token', tenant_id: TENANT }, method = 'POST') {
  return { method, headers: { 'x-request-id': 'req-1' }, body } as unknown as NextApiRequest;
}

function signedIn(jwt: Record<string, unknown> = {}) {
  mockAuth.mockResolvedValue({ token: '', jwt: { tenant: { id: TENANT }, email: 'sam@contoso.com', roles: ['tenant_admin'], ...jwt } });
}

function upstream(status: number, body: unknown) {
  fetchMock.mockResolvedValue({ status, json: async () => body });
}

async function run(req = post()) {
  const res = makeRes();
  await handler(req, res);
  return res;
}

beforeEach(() => {
  mockAuth.mockReset();
  fetchMock.mockReset();
  global.fetch = fetchMock as never;
  process.env.NOTIFICATION_SERVICE_URL = 'http://notifications:80';
  process.env.NOTIFICATION_SERVER_TOKEN = 'action-token';
});

describe('POST /api/integrations/ms-teams/link', () => {
  it('refuses other methods', async () => {
    signedIn();
    expect((await run(post(undefined, 'GET'))).statusCode).toBe(405);
  });

  it('answers 401 when signed out', async () => {
    mockAuth.mockResolvedValue(null);
    const res = await run();
    expect(res.statusCode).toBe(401);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each([
    ['a tenant admin', { roles: ['tenant_admin'] }],
    ['a super admin', { roles: [], isSuperAdmin: true }],
    ['the messaging-platforms write permission', { roles: ['account_admin'], permissions: ['messagingplatforms:Write'] }],
  ])('lets %s connect', async (_who, jwt) => {
    signedIn(jwt);
    upstream(200, { status: 'connected' });
    const res = await run();
    expect(res.statusCode).toBe(200);
    expect(res.body).toEqual({ status: 'connected' });
  });

  it.each([
    ['a read-only admin', { roles: ['tenant_admin_readonly'] }],
    ['a user with no role', { roles: [] }],
    ['read permission only', { roles: ['account_admin'], permissions: ['messagingplatforms:Read'] }],
  ])('refuses %s', async (_who, jwt) => {
    signedIn(jwt);
    const res = await run();
    expect(res.statusCode).toBe(403);
    expect(res.body).toEqual({ code: 'role_missing' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each([['bbbbbbbb-2222-4222-8222-222222222222'], [undefined], ['']])('refuses a workspace other than the session one (%s)', async (shown) => {
    signedIn();
    const res = await run(post({ token: 'link-token', tenant_id: shown }));
    expect(res.statusCode).toBe(409);
    expect(res.body).toEqual({ code: 'workspace_changed' });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it('sends the session workspace and email, never the link, as identity', async () => {
    signedIn();
    upstream(200, { status: 'connected' });
    await run();
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('http://notifications:80/api/integrations/link/ms-teams');
    expect(init.method).toBe('POST');
    expect(init.headers).toMatchObject({
      'tenant-id': TENANT,
      'x-user-email': 'sam@contoso.com',
      'x-request-id': 'req-1',
      'X-ACTION-TOKEN': 'action-token',
    });
    expect(JSON.parse(init.body)).toEqual({ token: 'link-token' });
  });

  it('sends an empty token when none is posted', async () => {
    signedIn();
    upstream(400, { code: 'invalid' });
    await run(post({ token: { nested: true }, tenant_id: TENANT }));
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ token: '' });
  });

  it.each([
    [{ code: 'invalid' }],
    [{ code: 'expired' }],
    [{ code: 'wrong_user' }],
    [{ code: 'team_linked_elsewhere' }],
    [{ code: 'other_microsoft_tenant' }],
    [{ code: 'already_connected' }],
  ])('passes a refusal through: %j', async (body) => {
    signedIn();
    upstream(400, body);
    const res = await run();
    expect(res.statusCode).toBe(400);
    expect(res.body).toEqual(body);
  });

  it('passes a bad action token through', async () => {
    signedIn();
    upstream(401, { detail: 'Unauthorized' });
    const res = await run();
    expect(res.statusCode).toBe(401);
    expect(res.body).toEqual({ detail: 'Unauthorized' });
  });

  it.each([500, 502])('turns a %s into a generic error', async (status) => {
    signedIn();
    upstream(status, { detail: 'Traceback with internals' });
    const res = await run();
    expect(res.statusCode).toBe(status);
    expect(res.body).toEqual({ code: 'error' });
  });

  it('answers 500 when notifications-server is unreachable', async () => {
    signedIn();
    jest.spyOn(console, 'error').mockImplementation(() => {});
    fetchMock.mockRejectedValue(new Error('ECONNREFUSED'));
    const res = await run();
    expect(res.statusCode).toBe(500);
    expect(JSON.stringify(res.body)).not.toContain('link-token');
  });
});
