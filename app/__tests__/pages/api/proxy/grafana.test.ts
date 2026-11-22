// The /api/proxy/grafana/[...grafana] route forwards to the relay with the relay
// secret attached, and only the first segment (the account) is access-checked.
// Next.js decodes each segment once, so an encoded `/` or `..` inside a segment
// must not let the forwarded path leave the relay's /grafana/ routes (#43931).
jest.mock('next-auth/next', () => ({ getServerSession: jest.fn().mockResolvedValue({ user: { id: 'u1' } }) }));
jest.mock('@pages/api/auth/[...nextauth]', () => ({ authOptions: {} }));
jest.mock('@lib/sessionCookie', () => ({
  readSessionToken: jest.fn().mockResolvedValue({ sub: 'u1', tenant: { id: 't1' }, idToken: 'id-token' }),
}));
jest.mock('@lib/accountAccess', () => ({ hasAccountAccess: jest.fn().mockResolvedValue(true) }));

import handler from '@pages/api/proxy/grafana/[...grafana]';
import type { NextApiRequest, NextApiResponse } from 'next';

const RELAY = 'http://relay.test:52832';

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
  res.send = jest.fn((body: unknown) => {
    res.body = body;
    return res;
  });
  res.setHeader = jest.fn().mockReturnValue(res);
  return res as unknown as NextApiResponse & { statusCode: number; body: any };
}

// segments are what Next.js hands the route: already decoded once.
async function call(segments: string[], method = 'GET') {
  const res = mockRes();
  await handler({ method, query: { grafana: segments }, headers: {}, body: { account_id: 'victim' } } as unknown as NextApiRequest, res);
  return res;
}

describe('/api/proxy/grafana path handling', () => {
  const fetchMock = jest.fn();
  const savedEnv = { ...process.env };

  beforeEach(() => {
    process.env.RELAY_SERVER_ENDPOINT = RELAY;
    process.env.RELAY_SERVER_SECRET_KEY = 'relay-secret';
    fetchMock.mockReset();
    fetchMock.mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ 'content-type': 'application/json' }),
      json: async () => ({ ok: true }),
    });
    global.fetch = fetchMock as unknown as typeof fetch;
  });

  afterAll(() => {
    process.env = savedEnv;
  });

  it('forwards an ordinary Grafana path under /grafana/', async () => {
    const res = await call(['gr-acct1', 'api', 'dashboards', 'uid', 'abc.def']);
    expect(res.statusCode).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const url = new URL(fetchMock.mock.calls[0][0]);
    expect(url.pathname.startsWith('/grafana/')).toBe(true);
  });

  it.each([
    ['decoded slashes and dot-segments', ['gr-acct1', '../../../proxy/config/push']],
    ['a plain dot-dot segment', ['gr-acct1', '..', '..', '..', 'proxy', 'config', 'push']],
    ['percent-encoded dots', ['gr-acct1', '%2e%2e', '%2E%2E', '%2e%2e', 'proxy', 'config', 'test']],
    ['a single dot segment', ['gr-acct1', '.', 'api']],
    ['a backslash', ['gr-acct1', '..\\..\\debug']],
    ['a slash in the account segment', ['gr-acct1/../..', 'proxy', 'resync-inventory']],
  ])('refuses %s without calling the relay', async (_name, segments) => {
    const res = await call(segments, 'POST');
    expect(res.statusCode).toBe(400);
    expect(res.body.error).toBe('invalid_path');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
