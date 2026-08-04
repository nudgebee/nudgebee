// The shared-counter client is built from env, so its options are not visible
// through the normal API — but one of them is load-bearing enough to pin.
const mockConstructed: Record<string, unknown>[] = [];
const mockCommands: string[] = [];

jest.mock('ioredis', () =>
  jest.fn().mockImplementation((options: Record<string, unknown>) => {
    mockConstructed.push(options);
    return {
      on: jest.fn(),
      defineCommand: (name: string) => {
        mockCommands.push(name);
      },
      nbRateLimitConsume: () => Promise.resolve(1),
    };
  })
);

import Redis from 'ioredis';
import { consumeRateLimit, resetRateLimits, setRateLimitRedisForTests } from '@lib/rateLimit';

const mockRedisCtor = Redis as unknown as jest.Mock;

describe('shared counter client', () => {
  const env = process.env;

  beforeEach(() => {
    mockConstructed.length = 0;
    mockCommands.length = 0;
    resetRateLimits();
    // Forget any cached client so the next call builds one from env.
    setRateLimitRedisForTests(undefined);
    process.env = { ...env, CACHE_PROVIDER: 'redis', REDIS_SERVER_HOST: '127.0.0.1', REDIS_SERVER_PORT: '6379' };
    jest.useFakeTimers();
    jest.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });

  afterEach(() => {
    jest.useRealTimers();
    process.env = env;
    resetRateLimits();
    setRateLimitRedisForTests(null);
  });

  it('keeps the offline queue on so a cold start is not mistaken for an outage', async () => {
    // With enableOfflineQueue: false, every request racing the pod's first
    // connect (or a reconnect) is rejected with "Stream isn't writeable" — a
    // healthy Redis reads as down, and the failure cooldown then skips it for
    // seconds. Observed in a real two-instance run before this was fixed.
    await consumeRateLimit('t', 'k', 5, 60000);

    expect(mockConstructed).toHaveLength(1);
    expect(mockConstructed[0].enableOfflineQueue).toBe(true);
    expect(mockConstructed[0].commandTimeout).toBe(200);
    expect(mockCommands).toEqual(['nbRateLimitConsume']);
  });

  it('builds the client once and reuses it', async () => {
    await consumeRateLimit('t', 'k', 5, 60000);
    await consumeRateLimit('t', 'k', 5, 60000);
    expect(mockConstructed).toHaveLength(1);
  });

  it('stays on the in-process path when the cache provider is not redis', async () => {
    process.env.CACHE_PROVIDER = 'memory';
    setRateLimitRedisForTests(undefined);

    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(true);
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(false);
    expect(mockConstructed).toHaveLength(0);
  });
  it('degrades to per-pod counters when the client cannot be constructed', async () => {
    // A limiter that throws is a limiter that is not applied: the exception
    // would escape into the route's catch, become a 500, and leave the
    // endpoint unthrottled. Construction must fail the same way a command
    // does — quietly, onto the local counter.
    jest.spyOn(console, 'warn').mockImplementation(() => {});
    mockRedisCtor.mockImplementationOnce(() => {
      throw new Error('invalid configuration');
    });

    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(true);
    // The local budget is still enforced rather than everything sailing through.
    expect((await consumeRateLimit('t', 'k', 1, 60000)).allowed).toBe(false);
    expect(console.warn).toHaveBeenCalledTimes(1);
  });

  it('retries construction once the cooldown lapses', async () => {
    jest.spyOn(console, 'warn').mockImplementation(() => {});
    mockRedisCtor.mockImplementationOnce(() => {
      throw new Error('invalid configuration');
    });

    await consumeRateLimit('t', 'k', 5, 60000);
    expect(mockConstructed).toHaveLength(0);

    // Within the cooldown the client is not rebuilt...
    await consumeRateLimit('t', 'k', 5, 60000);
    expect(mockConstructed).toHaveLength(0);

    // ...and afterwards a failed construction is not cached as "no redis".
    jest.advanceTimersByTime(5001);
    await consumeRateLimit('t', 'k', 5, 60000);
    expect(mockConstructed).toHaveLength(1);
  });
});
