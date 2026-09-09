// The /api/proxy/relay/[relay] route's endpoint allowlist. The segment is
// interpolated into the upstream fetch URL, so unknown segments must be rejected —
// but every segment the app actually posts to has to survive the gate. Dropping
// `ws` from the allowlist is what broke the pod shell (#36589), and nothing failed
// until a user opened a terminal.
//
// next-auth and the auth route pull in browser ESM that jest can't parse, and none
// of it matters here: every case below is decided before or at the auth step, so
// the modules are stubbed and auth is left failing on purpose. A segment that
// reaches the 401 has passed the allowlist, which is the whole assertion.
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn().mockResolvedValue(null) }));
jest.mock('next-auth/next', () => ({ getServerSession: jest.fn().mockResolvedValue(null) }));
jest.mock('@pages/api/auth/[...nextauth]', () => ({ authOptions: {} }));
jest.mock('@lib/internal', () => ({ decrypt: jest.fn(), decodeSessionJWT: jest.fn() }));
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
jest.mock('@lib/accountAccess', () => ({ hasAccountAccess: jest.fn().mockResolvedValue(true) }));

import handler from '@pages/api/proxy/relay/[relay]';
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
  // The handler chains off setHeader on the success path
  // (`res.status(200).setHeader(...).setHeader(...).json(...)`), so the mock has to
  // return the response rather than undefined.
  res.setHeader = jest.fn().mockReturnValue(res);
  return res as unknown as NextApiResponse & { statusCode: number; body: any };
}

async function call(relay: unknown) {
  const res = mockRes();
  await handler({ method: 'POST', query: { relay }, headers: {}, body: {} } as unknown as NextApiRequest, res);
  return res;
}

describe('/api/proxy/relay/[relay] allowlist', () => {
  // The segments every relay caller in the app posts to: `hitRelayServer`
  // (HttpService.ts) sends `/request` and `/grafana`, XtermTerminal.jsx sends `/ws`
  // for all four of start/exec/read/close.
  it.each(['request', 'grafana', 'ws'])('lets through the %s segment the app posts to', async (relay) => {
    const res = await call(relay);
    expect(res.body).not.toEqual(expect.objectContaining({ error: 'invalid_relay_endpoint' }));
    expect(res.statusCode).toBe(401);
  });

  it.each([['register'], ['../secret'], [''], [undefined]])('rejects the %s segment', async (relay) => {
    const res = await call(relay);
    expect(res.statusCode).toBe(400);
    expect(res.body.error).toBe('invalid_relay_endpoint');
  });
});
