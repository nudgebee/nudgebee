import type { NextApiRequest } from 'next';
import { checkIdentityRateLimit, checkPreAuthRateLimit, registerGatewayRateLimiter, resetGatewayRateLimiter } from '@lib/rateLimitHooks';

const req = { headers: {}, socket: {} } as unknown as NextApiRequest;

describe('gateway rate-limit hooks', () => {
  afterEach(() => {
    resetGatewayRateLimiter();
  });

  // The OSS build ships no limiter. This is the behaviour that defines it:
  // if this test ever fails closed, an OSS deployment starts rejecting
  // traffic with no way to configure its way out.
  it('admits every request when nothing is registered', async () => {
    await expect(checkPreAuthRateLimit(req)).resolves.toEqual({ allowed: true, retryAfterSec: 0 });
    await expect(checkIdentityRateLimit(req, 'user-1')).resolves.toEqual({ allowed: true, retryAfterSec: 0 });
  });

  it('delegates to a registered limiter and passes the subject through', async () => {
    const seen: string[] = [];
    registerGatewayRateLimiter({
      preAuth: async () => ({ allowed: false, retryAfterSec: 7, deniedBy: 'ip' }),
      identity: async (_req, subject) => {
        seen.push(subject);
        return { allowed: false, retryAfterSec: 9, deniedBy: 'user' };
      },
    });

    await expect(checkPreAuthRateLimit(req)).resolves.toEqual({ allowed: false, retryAfterSec: 7, deniedBy: 'ip' });
    await expect(checkIdentityRateLimit(req, 'user-1')).resolves.toEqual({ allowed: false, retryAfterSec: 9, deniedBy: 'user' });
    expect(seen).toEqual(['user-1']);
  });

  // Registration is partial by design, so an EE bundle can replace one budget
  // without silently dropping the other back to allow-all.
  it('leaves an unregistered half at its default', async () => {
    registerGatewayRateLimiter({ preAuth: async () => ({ allowed: false, retryAfterSec: 3, deniedBy: 'entry' }) });

    await expect(checkPreAuthRateLimit(req)).resolves.toMatchObject({ allowed: false });
    await expect(checkIdentityRateLimit(req, '')).resolves.toEqual({ allowed: true, retryAfterSec: 0 });
  });
});
