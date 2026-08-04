import type { NextApiRequest } from 'next';
import { clientIpKey, consumeRateLimit, consumeRateLimits, resetRateLimits, setRateLimitRedisForTests } from '@lib/rateLimit';

function makeReq(headers: Record<string, string | string[]> = {}, remoteAddress?: string): NextApiRequest {
  return { headers, socket: { remoteAddress } } as unknown as NextApiRequest;
}

describe('consumeRateLimit', () => {
  beforeEach(() => {
    resetRateLimits();
    setRateLimitRedisForTests(null);
    jest.useFakeTimers();
    jest.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });

  afterEach(() => {
    jest.useRealTimers();
    resetRateLimits();
  });

  it('allows up to the limit then denies', async () => {
    for (let i = 0; i < 3; i++) {
      expect((await consumeRateLimit('t', 'k', 3, 60000)).allowed).toBe(true);
    }
    expect((await consumeRateLimit('t', 'k', 3, 60000)).allowed).toBe(false);
  });

  it('reports seconds remaining in the window', async () => {
    await consumeRateLimit('t', 'k', 1, 60000);
    jest.advanceTimersByTime(20000);
    expect(await consumeRateLimit('t', 'k', 1, 60000)).toEqual({ allowed: false, retryAfterSec: 40 });
  });

  it('never reports a retryAfter of zero', async () => {
    await consumeRateLimit('t', 'k', 1, 60000);
    jest.advanceTimersByTime(59999);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).retryAfterSec).toBe(1);
  });

  it('rolls the window over once it expires', async () => {
    await consumeRateLimit('t', 'k', 1, 60000);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(false);
    jest.advanceTimersByTime(60000);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(true);
  });

  it('keeps separate budgets per key and per bucket name', async () => {
    expect((await consumeRateLimit('t', 'a', 1, 60000)).allowed).toBe(true);
    expect((await consumeRateLimit('t', 'a', 1, 60000)).allowed).toBe(false);
    expect((await consumeRateLimit('t', 'b', 1, 60000)).allowed).toBe(true);
    expect((await consumeRateLimit('other', 'a', 1, 60000)).allowed).toBe(true);
  });

  it('sweeps expired windows instead of failing closed when the bucket fills', async () => {
    // Fill past MAX_ENTRIES_PER_BUCKET with short-lived windows, let them
    // expire, then confirm a fresh key in the same bucket is still admitted.
    for (let i = 0; i < 10001; i++) {
      await consumeRateLimit('flood', `ip-${i}`, 1, 1000);
    }
    jest.advanceTimersByTime(1001);
    expect((await consumeRateLimit('flood', 'fresh', 1, 60000)).allowed).toBe(true);
  });

  it('fails closed rather than growing without bound', async () => {
    for (let i = 0; i < 10001; i++) {
      await consumeRateLimit('flood', `ip-${i}`, 1, 3600000);
    }
    expect((await consumeRateLimit('flood', 'fresh', 1, 60000)).allowed).toBe(false);
  });

  it('keeps a flooded bucket from starving any other bucket', async () => {
    // The signup IP budget is the only one an attacker can grow at will; a
    // flood against it must not fail-close signup_verify or the email budget.
    for (let i = 0; i < 10001; i++) {
      await consumeRateLimit('signup:ip', `ip-${i}`, 1, 3600000);
    }
    expect((await consumeRateLimit('signup:ip', 'fresh', 1, 3600000)).allowed).toBe(false);
    expect((await consumeRateLimit('signup_verify:ip', 'fresh', 1, 3600000)).allowed).toBe(true);
    expect((await consumeRateLimit('signup:email', 'victim@example.com', 1, 3600000)).allowed).toBe(true);
  });

  it('does not rescan a full bucket more than once per second', async () => {
    // Every window below expires at +1ms, but the sweep that would reclaim
    // them is rate limited, so the bucket stays closed until the throttle
    // lapses. Bounded CPU is worth up to a second of extra denial: without
    // this, each request against a full bucket walks the whole map.
    for (let i = 0; i < 10001; i++) {
      await consumeRateLimit('flood', `ip-${i}`, 1, 1);
    }
    jest.advanceTimersByTime(500);
    expect((await consumeRateLimit('flood', 'fresh', 1, 60000)).allowed).toBe(false);
    jest.advanceTimersByTime(501);
    expect((await consumeRateLimit('flood', 'fresh', 1, 60000)).allowed).toBe(true);
  });
});

describe('consumeRateLimit with a shared counter', () => {
  interface Call {
    key: string;
    limit: string;
    ttlMs: string;
  }

  function fakeRedis() {
    const counts = new Map<string, number>();
    const calls: Call[] = [];
    return {
      counts,
      calls,
      nbRateLimitConsume(key: string, limit: string, ttlMs: string) {
        calls.push({ key, limit, ttlMs });
        const cur = counts.get(key) ?? 0;
        if (cur + 1 > Number(limit)) {
          return Promise.resolve(0);
        }
        counts.set(key, cur + 1);
        return Promise.resolve(1);
      },
    };
  }

  beforeEach(() => {
    resetRateLimits();
    jest.useFakeTimers();
    jest.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });

  afterEach(() => {
    jest.useRealTimers();
    resetRateLimits();
    setRateLimitRedisForTests(null);
    jest.restoreAllMocks();
  });

  it('counts in the shared store so every replica sees the same budget', async () => {
    const redis = fakeRedis();
    setRateLimitRedisForTests(redis);

    for (let i = 0; i < 3; i++) {
      expect((await consumeRateLimit('t', 'k', 3, 60000)).allowed).toBe(true);
    }
    const denied = await consumeRateLimit('t', 'k', 3, 60000);
    expect(denied).toEqual({ allowed: false, retryAfterSec: 60 });
    // All four calls went to the shared store; nothing was decided locally.
    expect(redis.calls).toHaveLength(4);
    expect([...redis.counts.values()]).toEqual([3]);
  });

  it('puts the window in the key and expires it with the window', async () => {
    const redis = fakeRedis();
    setRateLimitRedisForTests(redis);

    await consumeRateLimit('signup:ip', '203.0.113.7', 5, 60000);
    // The window index is the aligned minute since the epoch, so the key
    // itself changes at the boundary and Redis expires the old one.
    const minute = Math.floor(Date.parse('2026-01-01T00:00:00Z') / 60000);
    expect(redis.calls[0].key).toBe(`nb:rl:signup:ip:203.0.113.7:${minute}`);
    expect(redis.calls[0].ttlMs).toBe('60000');

    // Half a minute in, the TTL is only the remainder of the window.
    jest.advanceTimersByTime(30000);
    await consumeRateLimit('signup:ip', '203.0.113.7', 5, 60000);
    expect(redis.calls[1].key).toBe(redis.calls[0].key);
    expect(redis.calls[1].ttlMs).toBe('30000');
  });

  it('rolls over to a new shared window', async () => {
    const redis = fakeRedis();
    setRateLimitRedisForTests(redis);

    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(true);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(false);
    jest.advanceTimersByTime(60000);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(true);
    expect(redis.counts.size).toBe(2);
  });

  it('degrades to per-pod counters when the shared store errors', async () => {
    // Neither fail-open (a mail cannon for anyone who can knock Redis over)
    // nor fail-closed (a Redis blip becomes a sign-up outage): the process
    // falls back to the bound it enforced before the shared store existed.
    jest.spyOn(console, 'warn').mockImplementation(() => {});
    setRateLimitRedisForTests({
      nbRateLimitConsume: () => Promise.reject(new Error('connection refused')),
    });

    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(true);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(false);
    expect(console.warn).toHaveBeenCalledTimes(1);
  });

  it('stops paying for the shared store while it is down, then retries', async () => {
    jest.spyOn(console, 'warn').mockImplementation(() => {});
    let attempts = 0;
    setRateLimitRedisForTests({
      nbRateLimitConsume: () => {
        attempts += 1;
        return Promise.reject(new Error('connection refused'));
      },
    });

    await consumeRateLimit('t', 'k', 10, 60000);
    await consumeRateLimit('t', 'k', 10, 60000);
    // The second call skipped Redis entirely rather than paying the round
    // trip (and the timeout) again.
    expect(attempts).toBe(1);

    jest.advanceTimersByTime(5001);
    await consumeRateLimit('t', 'k', 10, 60000);
    expect(attempts).toBe(2);
  });

  it('gives up on a slow shared store instead of holding the request', async () => {
    jest.spyOn(console, 'warn').mockImplementation(() => {});
    setRateLimitRedisForTests({
      nbRateLimitConsume: () => new Promise<number>(() => {}),
    });

    const pending = consumeRateLimit('t', 'k', 1, 60000);
    await jest.advanceTimersByTimeAsync(201);
    expect((await pending).allowed).toBe(true);
    expect(console.warn).toHaveBeenCalledTimes(1);
  });
});

describe('consumeRateLimits', () => {
  const LIMITS = [
    { limit: 2, windowMs: 60000 },
    { limit: 3, windowMs: 3600000 },
  ];

  beforeEach(() => {
    resetRateLimits();
    setRateLimitRedisForTests(null);
    jest.useFakeTimers();
    jest.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });

  afterEach(() => {
    jest.useRealTimers();
    resetRateLimits();
  });

  it('enforces the short window first', async () => {
    expect((await consumeRateLimits('t', 'k', LIMITS)).allowed).toBe(true);
    expect((await consumeRateLimits('t', 'k', LIMITS)).allowed).toBe(true);
    expect(await consumeRateLimits('t', 'k', LIMITS)).toEqual({ allowed: false, retryAfterSec: 60 });
  });

  it('still enforces the long window after the short one rolls over', async () => {
    await consumeRateLimits('t', 'k', LIMITS);
    await consumeRateLimits('t', 'k', LIMITS);
    jest.advanceTimersByTime(60000);
    // 3rd call of the hour: allowed.
    expect((await consumeRateLimits('t', 'k', LIMITS)).allowed).toBe(true);
    // 4th exceeds the hourly budget even though the minute budget has room.
    expect(await consumeRateLimits('t', 'k', LIMITS)).toEqual({ allowed: false, retryAfterSec: 3540 });
  });
});

describe('clientIpKey', () => {
  it('takes the leftmost x-forwarded-for entry', () => {
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '203.0.113.7, 10.0.0.1' }))).toBe('203.0.113.7');
  });

  it('handles a repeated header arriving as an array', () => {
    expect(clientIpKey(makeReq({ 'x-forwarded-for': ['203.0.113.7', '10.0.0.1'] }))).toBe('203.0.113.7');
  });

  it('falls back to the socket address when the header is absent or empty', () => {
    expect(clientIpKey(makeReq({}, '198.51.100.4'))).toBe('198.51.100.4');
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '' }, '198.51.100.4'))).toBe('198.51.100.4');
  });

  it('returns a stable placeholder when nothing identifies the caller', () => {
    expect(clientIpKey(makeReq({}))).toBe('unknown');
  });

  it('collapses IPv6 callers to their /64 so one host cannot mint identities', () => {
    // A subscriber is routinely delegated a whole /64. Keying on the full
    // address would make the per-IP budget unenforceable.
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '2001:db8:1:2:3:4:5:6' }))).toBe('2001:db8:1:2::/64');
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '2001:db8:1:2:aaaa:bbbb:cccc:dddd' }))).toBe('2001:db8:1:2::/64');
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '2001:0db8:0001:0002::9' }))).toBe('2001:db8:1:2::/64');
  });

  it('expands a compressed IPv6 address before taking the prefix', () => {
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '2001:db8::1' }))).toBe('2001:db8:0:0::/64');
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '::1' }))).toBe('0:0:0:0::/64');
  });

  it('strips a zone index', () => {
    expect(clientIpKey(makeReq({}, 'fe80::1%eth0'))).toBe('fe80:0:0:0::/64');
  });

  it('keys an IPv4-mapped peer as the IPv4 address', () => {
    // Dual-stack sockets report IPv4 peers as ::ffff:a.b.c.d; one client must
    // not get two identities depending on how the socket was opened.
    expect(clientIpKey(makeReq({}, '::ffff:198.51.100.4'))).toBe('198.51.100.4');
  });

  it('leaves IPv4 addresses untouched', () => {
    expect(clientIpKey(makeReq({ 'x-forwarded-for': '203.0.113.7, 10.0.0.1' }))).toBe('203.0.113.7');
  });
});
