// Rate-limiting extension point for the GraphQL gateway.
//
// The limiter itself is an EE feature: the implementation, its budgets and
// its shared-counter backend live in `@ee/ratelimit` and are removed from OSS
// snapshots along with the rest of `app/src/ee/`. This file is the seam that
// lets `graphqlGatewayHandler` call it without importing it, which is what
// keeps the OSS/EE boundary check (scripts/check-oss-boundary.sh) satisfied.
//
// **With nothing registered every request is admitted.** An OSS build has no
// throttle on /api/graphql; the deployment is expected to bound request rates
// at its ingress. That is a deliberate product decision, not an oversight --
// state it plainly here so a reader of the OSS tree is not left believing a
// limiter is present.
//
// The registry lives on globalThis for the same reason `@lib/authHooks` does:
// Next.js keeps a dual module graph, and the EE registration runs from
// `instrumentation.ts` -> `@ee/init-server` while the read happens in a page
// route context. Module-scoped state would let the registration mutate one
// instance of this file while the gateway reads another, and the EE behavior
// would silently no-op.

import type { NextApiRequest } from 'next';

export interface RateLimitOutcome {
  allowed: boolean;
  /** Seconds until the caller may retry. Only meaningful when !allowed. */
  retryAfterSec: number;
  /** Which budget refused, for the trace span. Only set when !allowed. */
  deniedBy?: string;
}

/** Budgets that apply before the request is authenticated. */
export type PreAuthRateLimitCheck = (req: NextApiRequest) => Promise<RateLimitOutcome>;

/**
 * The per-caller budget, applied once the request is authenticated.
 *
 * `subject` is the authenticated principal, or an empty string when the
 * request produced none — the implementation decides what to fall back to, so
 * that deriving an identity from the request stays on the EE side.
 */
export type IdentityRateLimitCheck = (req: NextApiRequest, subject: string) => Promise<RateLimitOutcome>;

interface GatewayRateLimiter {
  preAuth: PreAuthRateLimitCheck;
  identity: IdentityRateLimitCheck;
}

const ADMITTED: RateLimitOutcome = Object.freeze({ allowed: true, retryAfterSec: 0 });

const _g = globalThis as unknown as { __nbGatewayRateLimiter?: GatewayRateLimiter };
if (!_g.__nbGatewayRateLimiter) {
  _g.__nbGatewayRateLimiter = {
    preAuth: async () => ADMITTED,
    identity: async () => ADMITTED,
  };
}

const _registry = _g.__nbGatewayRateLimiter!;

/** Install the limiter. Called once, from the EE server-side init. */
export function registerGatewayRateLimiter(limiter: Partial<GatewayRateLimiter>): void {
  if (limiter.preAuth) {
    _registry.preAuth = limiter.preAuth;
  }
  if (limiter.identity) {
    _registry.identity = limiter.identity;
  }
}

export function checkPreAuthRateLimit(req: NextApiRequest): Promise<RateLimitOutcome> {
  return _registry.preAuth(req);
}

export function checkIdentityRateLimit(req: NextApiRequest, subject: string): Promise<RateLimitOutcome> {
  return _registry.identity(req, subject);
}

/** Test-only: drop any registered limiter and go back to admitting everything. */
export function resetGatewayRateLimiter(): void {
  _registry.preAuth = async () => ADMITTED;
  _registry.identity = async () => ADMITTED;
}
