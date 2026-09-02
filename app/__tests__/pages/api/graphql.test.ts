import type { NextApiRequest, NextApiResponse } from 'next';
import handler from '@pages/api/graphql';
import { authenticateRequest, tryBypassGraphQL } from '@lib/rpcGateway';
import { resetRateLimits, setRateLimitRedisForTests } from '@lib/rateLimit';

jest.mock('@lib/rpcGateway', () => ({
  authenticateRequest: jest.fn(),
  tryBypassGraphQL: jest.fn(),
}));

const mockAuth = authenticateRequest as jest.Mock;
const mockGateway = tryBypassGraphQL as jest.Mock;

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

function call(ip: string, sub = 'user-1') {
  mockAuth.mockResolvedValue({ jwt: { sub }, token: null });
  const req = {
    method: 'POST',
    headers: { 'x-forwarded-for': `${ip}, 10.0.0.1` },
    socket: {},
    body: { query: '{ me { id } }', operationName: 'Me' },
  } as unknown as NextApiRequest;
  const res = makeRes();
  return handler(req, res).then(() => res);
}

describe('POST /api/graphql rate limiting', () => {
  beforeEach(() => {
    resetRateLimits();
    setRateLimitRedisForTests(null);
    jest.clearAllMocks();
    jest.useFakeTimers();
    jest.setSystemTime(new Date('2026-01-01T00:00:00Z'));
    mockGateway.mockResolvedValue({ handled: true, status: 200, body: { data: {} } });
  });

  afterEach(() => {
    jest.useRealTimers();
    resetRateLimits();
    process.env.RATE_LIMIT_ENABLED_GRAPHQL = '';
    delete process.env.RATE_LIMIT_ENABLED_GRAPHQL;
  });

  it('passes normal traffic straight through', async () => {
    const res = await call('203.0.113.10');
    expect(res.statusCode).toBe(200);
    expect(mockGateway).toHaveBeenCalledTimes(1);
  });

  it('throttles one user without touching the upstream gateway', async () => {
    // The default per-user budget is 600/min; spend it, then assert the 601st
    // is refused before it reaches the RPC gateway.
    for (let i = 0; i < 600; i++) {
      await call('203.0.113.11');
    }
    mockGateway.mockClear();

    const res = await call('203.0.113.11');

    expect(res.statusCode).toBe(429);
    expect(res.body.errors[0].message).toMatch(/too many requests/i);
    expect(Number(res.headers['Retry-After'])).toBeGreaterThan(0);
    expect(mockGateway).not.toHaveBeenCalled();
  });

  it('keeps budgets separate per user', async () => {
    for (let i = 0; i < 600; i++) {
      await call('203.0.113.12', 'noisy-user');
    }
    expect((await call('203.0.113.12', 'noisy-user')).statusCode).toBe(429);
    // A different account sharing the same ingress address is unaffected.
    expect((await call('203.0.113.12', 'quiet-user')).statusCode).toBe(200);
  });

  it('throttles an unauthenticated flood before authenticating it', async () => {
    // The address-keyed budget runs first, so a caller that never presents a
    // session still cannot make the handler verify one 1200 times a minute.
    mockAuth.mockResolvedValue(null);
    for (let i = 0; i < 1200; i++) {
      await call('203.0.113.13');
    }
    mockAuth.mockClear();

    const res = await call('203.0.113.13');

    expect(res.statusCode).toBe(429);
    expect(mockAuth).not.toHaveBeenCalled();
  });

  it('is disabled by its own scope switch, leaving other scopes alone', async () => {
    process.env.RATE_LIMIT_ENABLED_GRAPHQL = 'false';
    for (let i = 0; i < 700; i++) {
      expect((await call('203.0.113.14')).statusCode).toBe(200);
    }
  });
});
