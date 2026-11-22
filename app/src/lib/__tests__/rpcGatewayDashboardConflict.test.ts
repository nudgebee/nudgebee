/**
 * The dashboard editor recognises "someone else saved first" by the upstream
 * status alone (#40228). This pins both halves of that contract through the
 * gateway: the version the editor read reaches the api-server, and the 409 it
 * answers with comes back where the editor looks for it.
 */
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import type { JWT } from 'next-auth/jwt';
import { tryBypassGraphQL } from '@lib/rpcGateway';
import { getUpstreamStatus } from '@lib/errorMessages';
import { UPDATE_DASHBOARD } from '@api1/dashboards';

const jwt = { roles: ['tenant_admin'], tenant: { id: 't1' }, id: 'u1' } as unknown as JWT;

describe('saving a dashboard through the gateway', () => {
  const originalFetch = global.fetch;
  beforeEach(() => {
    process.env.SERVICE_API_SERVER_URL = 'http://api-server';
  });
  afterEach(() => {
    global.fetch = originalFetch;
  });

  it('forwards expected_version and hands the 409 back as the upstream status', async () => {
    const message = 'this dashboard was saved by someone else after you opened it; reload to see their changes';
    // What gin sends for c.JSON(409, …), in the shape the gateway reads; jsdom's Response cannot be read here.
    const fetchMock = jest.fn(async () => ({
      ok: false,
      status: 409,
      headers: new Headers({ 'content-type': 'application/json; charset=utf-8' }),
      text: async () => JSON.stringify({ message }),
    }));
    global.fetch = fetchMock as unknown as typeof fetch;

    const result = await tryBypassGraphQL({
      query: UPDATE_DASHBOARD,
      variables: { request: { id: 'dash-1', title: 'Fleet', definition: { panels: [] }, expected_version: 4 } },
      jwt,
      traceparent: '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01',
      requestId: 'r1',
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = (fetchMock.mock.calls[0] as unknown as [string, RequestInit]) ?? [];
    expect(url).toBe('http://api-server/rpc/dashboard');
    expect(JSON.stringify(JSON.parse(String(init.body)))).toContain('"expected_version":4');

    expect(result.handled).toBe(true);
    if (!result.handled) return;
    expect(result.body.data).toEqual({ dashboards_update: null });
    expect(result.body.errors?.[0]?.message).toBe(message);
    expect(getUpstreamStatus(result.body)).toBe(409);
  });
});
