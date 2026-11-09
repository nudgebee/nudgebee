/**
 * A generic upstream message ("database connection failed") is only debuggable
 * if the client can find the log line that holds the cause. The gateway hands
 * back the trace id the upstream stamped on that log line.
 */
jest.mock('next-auth/jwt', () => ({ getToken: jest.fn() }));
jest.mock('@lib/internal', () => ({ decodeSessionJWT: jest.fn(), decrypt: jest.fn() }));
jest.mock('@lib/sessionRevocation', () => ({ isSessionRevoked: jest.fn() }));

import type { JWT } from 'next-auth/jwt';
import { traceIdFromTraceparent, tryBypassGraphQL } from '@lib/rpcGateway';
import { getUpstreamStatus } from '@lib/errorMessages';
import { UPDATE_DASHBOARD } from '@api1/dashboards';

const jwt = { roles: ['tenant_admin'], tenant: { id: 't1' }, id: 'u1' } as unknown as JWT;
const traceparent = '00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01';

describe('gateway error trace id', () => {
  const originalFetch = global.fetch;
  beforeEach(() => {
    process.env.SERVICE_API_SERVER_URL = 'http://api-server';
  });
  afterEach(() => {
    global.fetch = originalFetch;
  });

  it('puts the request trace id on an upstream error and keeps the message and status', async () => {
    global.fetch = jest.fn(async () => ({
      ok: false,
      status: 500,
      headers: new Headers({ 'content-type': 'application/json; charset=utf-8' }),
      text: async () => JSON.stringify({ message: 'database connection failed' }),
    })) as unknown as typeof fetch;

    const result = await tryBypassGraphQL({
      query: UPDATE_DASHBOARD,
      variables: { request: { id: 'dash-1', title: 'Fleet', definition: { panels: [] }, expected_version: 1 } },
      jwt,
      traceparent,
      requestId: 'r1',
    });

    expect(result.handled).toBe(true);
    if (!result.handled) return;
    const err = result.body.errors?.[0];
    expect(err?.message).toBe('database connection failed');
    expect(err?.extensions?.trace_id).toBe('0af7651916cd43dd8448eb211c80319c');
    expect(getUpstreamStatus(result.body)).toBe(500);
  });

  it('reads the trace id field of a traceparent and rejects malformed ones', () => {
    expect(traceIdFromTraceparent(traceparent)).toBe('0af7651916cd43dd8448eb211c80319c');
    expect(traceIdFromTraceparent('r1')).toBe('');
    expect(traceIdFromTraceparent(undefined)).toBe('');
  });
});
