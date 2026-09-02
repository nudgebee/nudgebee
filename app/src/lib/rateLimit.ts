// Fixed-window rate limiter for unauthenticated API routes.
//
// Two counter backends, selected by configuration, mirroring the Go
// implementation in llm/gateway/ee/ratelimit (same env vars, same
// check-then-increment Lua, same "Redis when configured, in-process
// otherwise" selection):
//
//  * Redis — counters are shared by every replica, so the configured limit is
//    the real cluster-wide limit. This is the deployed path: `app` runs
//    replicaCount: 2 in dev/test/prod, and the `nudgebee` secret the pod
//    already loads via envFrom carries CACHE_PROVIDER + REDIS_SERVER_*, so
//    nothing in the chart has to change to turn this on.
//  * In-process maps — used when Redis is not configured (single-pod on-prem
//    and OSS installs, local dev, tests) and as the degraded fallback when a
//    configured Redis is unreachable.
//
// The fallback is the interesting decision. Failing open would let anyone who
// can knock Redis over flood the mailer, and failing closed would turn a Redis
// blip into a sign-up outage. Degrading to per-pod counters keeps a bound at
// all times: for the duration of the outage the effective limit is
// (limit x pod count), which is exactly what this module enforced before the
// shared backend existed. A short cooldown after a failure keeps an outage
// from costing every request a Redis round trip.
//
// Windows are fixed and wall-clock aligned in both backends. Aligned windows
// are what let the Redis key carry the window (`...:<index>`, expired by TTL
// rather than by bookkeeping), and keeping the in-process store on the same
// scheme means a mid-window fallback cannot report a Retry-After that
// contradicts the one the shared counter just issued. Fixed, not sliding: a
// caller can spend a full budget at the end of one window and another at the
// start of the next. Acceptable for abuse control, not for billing-grade
// quotas.
//
// In-process memory is hard-bounded, and bounded *per bucket name* rather than
// globally. Each `name` gets its own window map capped at
// MAX_ENTRIES_PER_BUCKET, so a flood of distinct keys against one budget
// cannot evict or starve another. That isolation is load-bearing: the per-IP
// budget is the only one an attacker can grow at will (one entry per source
// address), and without separate maps a flood on /api/auth/signup would
// fail-close /api/auth/signup_verify too — locking out users who are holding a
// valid, short-lived verification token.
//
// When a bucket is full the limiter denies rather than growing (fail-closed).
// Evicting instead would hand the attacker a way to reset their own counters.
// Sweeping expired windows is what normally reclaims space, but a full bucket
// under a flood would otherwise sweep on *every* request, so sweeps are
// throttled to at most one per SWEEP_INTERVAL_MS per bucket — an O(n) scan of
// the map must not run on the event loop once per inbound request.

import { createHash } from 'crypto';
import type { NextApiRequest, NextApiResponse } from 'next';
import Redis from 'ioredis';

interface Window {
  count: number;
  resetAt: number;
}

interface Bucket {
  windows: Map<string, Window>;
  lastSweepAt: number;
}

const buckets = new Map<string, Bucket>();

// Per bucket name. Currently seven bucket names are in use (signup ip/global/
// email and signup_verify ip, each split per window length), so the worst case
// is ~70k live windows — single-digit MB, and orders of magnitude above any
// legitimate signup traffic.
const MAX_ENTRIES_PER_BUCKET = 10000;

// Never scan a full bucket more than once a second. While the throttle is in
// effect a full bucket simply denies, which is the same answer the sweep would
// have produced for anything but a just-expired window.
const SWEEP_INTERVAL_MS = 1000;

export interface RateLimitResult {
  allowed: boolean;
  /** Seconds until the caller may retry. Only meaningful when !allowed. */
  retryAfterSec: number;
}

// Frozen: this single instance is returned by reference on every allowed call,
// so a caller mutating it would corrupt every future result.
const ALLOWED: RateLimitResult = Object.freeze({ allowed: true, retryAfterSec: 0 });

/** End of the wall-clock-aligned window containing `now`. */
function windowEnd(now: number, windowMs: number): number {
  return (Math.floor(now / windowMs) + 1) * windowMs;
}

function retryAfterSec(resetAt: number, now: number): number {
  return Math.max(1, Math.ceil((resetAt - now) / 1000));
}

function getBucket(name: string): Bucket {
  let bucket = buckets.get(name);
  if (!bucket) {
    bucket = { windows: new Map(), lastSweepAt: 0 };
    buckets.set(name, bucket);
  }
  return bucket;
}

function sweep(bucket: Bucket, now: number) {
  if (now - bucket.lastSweepAt < SWEEP_INTERVAL_MS) {
    return;
  }
  bucket.lastSweepAt = now;
  for (const [key, win] of bucket.windows) {
    if (win.resetAt <= now) {
      bucket.windows.delete(key);
    }
  }
}

/** In-process counter. Also the fallback when a configured Redis is down. */
function consumeLocal(name: string, key: string, limit: number, windowMs: number, now: number): RateLimitResult {
  const bucket = getBucket(name);
  // Bound the key so a hostile header can't blow up map memory per entry.
  const mapKey = key.slice(0, 128);

  const existing = bucket.windows.get(mapKey);
  if (existing && existing.resetAt > now) {
    if (existing.count >= limit) {
      return { allowed: false, retryAfterSec: retryAfterSec(existing.resetAt, now) };
    }
    existing.count += 1;
    return ALLOWED;
  }

  if (bucket.windows.size >= MAX_ENTRIES_PER_BUCKET) {
    sweep(bucket, now);
    if (bucket.windows.size >= MAX_ENTRIES_PER_BUCKET) {
      // Fail closed — see the module comment.
      return { allowed: false, retryAfterSec: Math.ceil(windowMs / 1000) };
    }
  }

  bucket.windows.set(mapKey, { count: 1, resetAt: windowEnd(now, windowMs) });
  return ALLOWED;
}

/**
 * Kill switch, global or per scope.
 *
 * Enabled unless explicitly turned off, because the endpoints this guards are
 * unauthenticated (or, for the GraphQL gateway, the whole API surface) and the
 * failure mode of "off" is the incident this module exists to prevent. Only a
 * recognised off value disables it, so a typo leaves the limiter on.
 *
 *   RATE_LIMIT_ENABLED=false            turns everything off
 *   RATE_LIMIT_ENABLED_GRAPHQL=false    turns off one scope, others unaffected
 *
 * The scope is the part of the bucket name before the first colon
 * (`signup:ip` -> SIGNUP, `signup_verify:ip` -> SIGNUP_VERIFY, `graphql:user`
 * -> GRAPHQL), so a new call site cannot forget to honour the switch.
 */
export function rateLimitEnabled(scope?: string): boolean {
  const scoped = scope ? process.env[`RATE_LIMIT_ENABLED_${envSuffix(scope)}`] : undefined;
  const raw = (scoped ?? process.env.RATE_LIMIT_ENABLED ?? '').trim().toLowerCase();
  return !(raw === 'false' || raw === '0' || raw === 'off' || raw === 'no');
}

function envSuffix(name: string): string {
  return name.toUpperCase().replace(/[^A-Z0-9]+/g, '_');
}

const DURATION_MS: Record<string, number> = { s: 1000, m: 60 * 1000, h: 60 * 60 * 1000 };

/**
 * Parse a budget spec: comma-separated `<count>/<window>` pairs, where the
 * window is a number followed by s, m or h — e.g. `5/1m,20/1h`.
 *
 * Returns null for anything malformed, including a zero count: an operator who
 * wants no limit should say so with the kill switch, not with a spec that
 * silently admits everything.
 */
export function parseLimits(spec: string): RateLimitWindow[] | null {
  const windows: RateLimitWindow[] = [];
  for (const part of spec.split(',')) {
    const match = /^\s*(\d+)\s*\/\s*(\d+)\s*([smh])\s*$/.exec(part);
    if (!match) {
      return null;
    }
    const limit = Number(match[1]);
    const windowMs = Number(match[2]) * DURATION_MS[match[3]];
    if (!limit || !windowMs) {
      return null;
    }
    windows.push({ limit, windowMs });
  }
  return windows.length ? windows : null;
}

const limitsCache = new Map<string, RateLimitWindow[]>();

/**
 * Budgets for a bucket, overridable per deployment.
 *
 * `signup:ip` reads RATE_LIMIT_SIGNUP_IP, `graphql:user` reads
 * RATE_LIMIT_GRAPHQL_USER, and so on. A malformed override falls back to the
 * built-in default and says so once — a bad value must not be a way to
 * accidentally remove a limit. Parsed once per process; changing an override
 * takes a restart, like every other env var here.
 */
export function limitsFor(name: string, fallback: string): RateLimitWindow[] {
  const cached = limitsCache.get(name);
  if (cached) {
    return cached;
  }
  const raw = process.env[`RATE_LIMIT_${envSuffix(name)}`]?.trim();
  let windows = raw ? parseLimits(raw) : null;
  if (raw && !windows) {
    console.warn(`rateLimit: ignoring malformed RATE_LIMIT_${envSuffix(name)}="${raw}", using ${fallback}`);
  }
  if (!windows) {
    windows = parseLimits(fallback);
  }
  if (!windows) {
    throw new Error(`rateLimit: built-in default "${fallback}" for ${name} is not a valid spec`);
  }
  limitsCache.set(name, windows);
  return windows;
}

// Namespace for shared-store keys. Separate Redis instances per environment
// make collisions impossible today, but an installation pointed at a Redis it
// shares with another Nudgebee install would otherwise mix counters — set
// RATE_LIMIT_KEY_PREFIX to something installation-specific there.
function keyPrefix(): string {
  return process.env.RATE_LIMIT_KEY_PREFIX?.trim() || 'nb:rl';
}

/**
 * Stable, non-reversible key for a value that identifies a person.
 *
 * Email addresses are the budget key for the anti-mail-cannon cap, and a
 * counter key is not a place to keep PII: keys are visible to anyone with
 * Redis access, to `MONITOR`, and to whatever backs the instance up. The
 * truncated digest keeps the budget exactly as strict while making the key
 * useless to an onlooker.
 */
export function identityKey(value: string): string {
  return createHash('sha256').update(value).digest('hex').slice(0, 32);
}

// Check first, INCR only when the call is admitted, and set the TTL on the
// write that created the key — so a crash between INCR and EXPIRE cannot
// strand a counter forever. Same shape as checkAndIncrLua in
// llm/gateway/ee/ratelimit/redis.go.
//
// KEYS[1]=window key  ARGV[1]=limit  ARGV[2]=ms until the window ends.
const CONSUME_LUA = `
local cur = tonumber(redis.call('GET', KEYS[1]) or '0')
if cur + 1 > tonumber(ARGV[1]) then return 0 end
local new = redis.call('INCR', KEYS[1])
if new == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
return 1
`;

/** The one command this module needs, registered via ioredis defineCommand. */
export interface RateLimitRedis {
  nbRateLimitConsume(key: string, limit: string, ttlMs: string): Promise<number>;
}

// A command must not add latency to the path we are trying to keep cheap, and
// an outage must not cost every request that latency either: one failure
// parks Redis for REDIS_COOLDOWN_MS and everything runs locally until then.
const REDIS_COMMAND_TIMEOUT_MS = 200;
const REDIS_COOLDOWN_MS = 5000;

// Cached on globalThis so Next's dev-mode module reloading doesn't leak a new
// connection per edit.
const globalForRedis = globalThis as typeof globalThis & {
  __nbRateLimitRedis?: RateLimitRedis | null;
};

let redisDownUntil = 0;

function redisConfigured(): boolean {
  return (process.env.CACHE_PROVIDER ?? '').toLowerCase() === 'redis' && !!process.env.REDIS_SERVER_HOST;
}

/**
 * The shared counter client, or null to use the in-process path.
 *
 * Total by construction: a limiter that throws is a limiter that isn't
 * applied — the route's catch would turn it into a 500 and the endpoint would
 * serve unthrottled. Anything that goes wrong here degrades to the local
 * counter instead, exactly like a command failure.
 */
function getRedis(now: number): RateLimitRedis | null {
  if (globalForRedis.__nbRateLimitRedis !== undefined) {
    return globalForRedis.__nbRateLimitRedis;
  }
  if (!redisConfigured()) {
    globalForRedis.__nbRateLimitRedis = null;
    return null;
  }

  try {
    const client = new Redis({
      host: process.env.REDIS_SERVER_HOST,
      port: Number(process.env.REDIS_SERVER_PORT ?? 6379),
      username: process.env.REDIS_USER_NAME || undefined,
      password: process.env.REDIS_USER_PASSWORD || undefined,
      connectTimeout: 2000,
      commandTimeout: REDIS_COMMAND_TIMEOUT_MS,
      // Keep the offline queue. Turning it off rejects anything issued before
      // the socket is ready — which is every request racing a pod's cold start
      // or a reconnect, so a perfectly healthy Redis reads as down and the
      // cooldown below then skips it for 5s. Queued commands are still bounded
      // by the timeout race in consumeRateLimit(), so a real outage still falls
      // back within REDIS_COMMAND_TIMEOUT_MS.
      enableOfflineQueue: true,
      maxRetriesPerRequest: 1,
    });
    // ioredis emits 'error' on every reconnect attempt; an unhandled one is a
    // process-level crash. Failures are reported by the command path instead.
    client.on('error', () => {});
    client.defineCommand('nbRateLimitConsume', { numberOfKeys: 1, lua: CONSUME_LUA });

    globalForRedis.__nbRateLimitRedis = client as unknown as RateLimitRedis;
    return globalForRedis.__nbRateLimitRedis;
  } catch (err) {
    // Bad configuration, or a module that didn't load the way we expect. Not
    // cached, so the next call after the cooldown tries again.
    noteRedisFailure(err, now);
    return null;
  }
}

function withTimeout<T>(p: Promise<T>, ms: number): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('rateLimit: redis timed out')), ms);
    p.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (err) => {
        clearTimeout(timer);
        reject(err);
      }
    );
  });
}

function noteRedisFailure(err: unknown, now: number) {
  // One line per cooldown, not one per request: an outage must not also be a
  // log flood.
  if (redisDownUntil <= now) {
    console.warn('rateLimit: redis unavailable, falling back to per-pod counters', err);
  }
  redisDownUntil = now + REDIS_COOLDOWN_MS;
}

/**
 * Consume one unit from the `name`:`key` window. Returns whether the call is
 * allowed and, when it is not, how long until the window rolls over.
 *
 * Callers should treat this as consuming the budget: a request that is later
 * rejected for another reason still counts, which is what we want for an
 * endpoint whose expensive work happens after validation.
 */
export async function consumeRateLimit(name: string, key: string, limit: number, windowMs: number): Promise<RateLimitResult> {
  if (!rateLimitEnabled(name.split(':')[0])) {
    return ALLOWED;
  }

  const now = Date.now();
  // Charge the in-process counter on every call, even when the shared store is
  // the one deciding. Without this each pod enters a Redis outage with a fresh
  // budget, so the degraded bound is (pods + 1) x limit — the shared counter's
  // full allowance, then a whole new allowance per pod. Keeping local state
  // warm means a pod that has already served its share has already spent it.
  const local = consumeLocal(name, key, limit, windowMs, now);
  const client = now >= redisDownUntil ? getRedis(now) : null;

  if (client) {
    const resetAt = windowEnd(now, windowMs);
    // The window index is part of the key, so expiry is Redis's TTL rather
    // than anything this process has to remember.
    const redisKey = `${keyPrefix()}:${name}:${key.slice(0, 128)}:${Math.floor(now / windowMs)}`;
    try {
      const allowed = await withTimeout(
        client.nbRateLimitConsume(redisKey, String(limit), String(Math.max(1, resetAt - now))),
        REDIS_COMMAND_TIMEOUT_MS
      );
      return allowed === 1 ? ALLOWED : { allowed: false, retryAfterSec: retryAfterSec(resetAt, now) };
    } catch (err) {
      noteRedisFailure(err, now);
    }
  }

  return local;
}

export interface RateLimitWindow {
  limit: number;
  windowMs: number;
}

/**
 * Consume one unit from every window in `limits`. Denies on the first window
 * that is exhausted; the shorter windows it already charged stay charged,
 * which is fine for abuse control and keeps the call site a single check.
 */
export async function consumeRateLimits(name: string, key: string, limits: RateLimitWindow[]): Promise<RateLimitResult> {
  for (const { limit, windowMs } of limits) {
    const result = await consumeRateLimit(`${name}:${windowMs}`, key, limit, windowMs);
    if (!result.allowed) {
      return result;
    }
  }
  return ALLOWED;
}

/** 429 with a `Retry-After` so a throttled but legitimate caller can recover. */
export function sendTooManyRequests(res: NextApiResponse, retryAfterSec: number, message: string) {
  res.setHeader('Retry-After', String(retryAfterSec));
  res.status(429).json({ message });
}

/**
 * Collapse an IPv6 address to its /64 prefix.
 *
 * A single host is routinely delegated a whole /64, so keying on the full
 * address would give one attacker 2^64 free identities — an unlimited per-IP
 * budget, and a cheap way to fill a bucket to its cap. /64 is the smallest
 * unit that is actually allocated to a subscriber, so it is the right
 * granularity; it does mean everyone behind one residential /64 shares a
 * budget, which is the same property IPv4 NAT already has.
 */
function ipv6Prefix64(addr: string): string {
  const [head, tail] = addr.split('::');
  const headParts = head ? head.split(':') : [];
  const tailParts = tail ? tail.split(':') : [];
  const missing = addr.includes('::') ? Math.max(0, 8 - headParts.length - tailParts.length) : 0;
  const groups = [...headParts, ...Array(missing).fill('0'), ...tailParts];
  const prefix = groups
    .slice(0, 4)
    .map((group) => (group || '0').replace(/^0+(?=.)/, '').toLowerCase())
    .join(':');
  return `${prefix}::/64`;
}

/**
 * Rate-limit identity for the caller, derived from the client address.
 *
 * The leftmost `X-Forwarded-For` entry is the client address in our topology:
 * ingress-nginx replaces the header with the connecting peer unless
 * `use-forwarded-headers` is turned on, and the app's nginx sidecar then
 * appends its own peer (`$proxy_add_x_forwarded_for`), so the header arriving
 * here is `<client>, <ingress-pod-ip>`.
 *
 * Note that this rests on the ingress controller's *default*, which is not
 * pinned anywhere in the chart or in nudgebee-infra, and dev is mid-migration
 * from ingress-nginx to Traefik. Treat the value as untrusted: a deployment
 * that fronts the ingress with a CDN, turns `use-forwarded-headers` on, or
 * lands on a controller with different defaults makes the leftmost entry
 * client-controlled. That is why the signup routes also carry a process-wide
 * cap that does not depend on this value at all.
 */
export function clientIpKey(req: NextApiRequest): string {
  const xff = req.headers['x-forwarded-for'];
  const raw = Array.isArray(xff) ? xff[0] : xff;
  const first = raw?.split(',')[0]?.trim();
  const addr = first || req.socket?.remoteAddress || '';
  if (!addr) {
    return 'unknown';
  }

  // Strip any zone index (`fe80::1%eth0`).
  const bare = addr.split('%')[0];
  // A dual-stack socket reports IPv4 peers as `::ffff:1.2.3.4`; key those as
  // the IPv4 address so one client cannot hold two identities.
  const mapped = /^::ffff:(\d{1,3}(?:\.\d{1,3}){3})$/i.exec(bare);
  if (mapped) {
    return mapped[1];
  }
  return bare.includes(':') ? ipv6Prefix64(bare) : bare;
}

/** Test-only: drop all windows so cases don't leak state into each other. */
export function resetRateLimits() {
  buckets.clear();
  limitsCache.clear();
  redisDownUntil = 0;
}

/**
 * Test-only: inject a shared counter client, force the local path with null,
 * or forget the cached one with undefined so the next call rebuilds from env.
 */
export function setRateLimitRedisForTests(client?: RateLimitRedis | null) {
  if (client === undefined) {
    delete globalForRedis.__nbRateLimitRedis;
    return;
  }
  globalForRedis.__nbRateLimitRedis = client;
}
