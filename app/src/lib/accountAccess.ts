import { trace, SpanStatusCode } from '@opentelemetry/api';

// hasAccountAccess delegates per-tenant account-level authz to the api-server's
// /v1/authz/validate_access endpoint. It's the authoritative server-side check
// for Next.js proxy routes that forward user requests to internal services
// (relay, grafana, etc.) — the proxy must verify the calling user has access
// to the account_id being targeted BEFORE forwarding, since the relay/agent
// downstream only enforces routing (not per-user authz).
//
// The pair of env vars is read once at module load; both fall back to local
// dev defaults so this file remains importable in tests without env setup.
const auditEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';
const servicesServerToken = process.env.ACTION_API_SERVER_TOKEN ?? '';

// In-process TTL cache for authz decisions. Grafana's UI loads dozens of assets
// per page-view; without this, every asset request would round-trip to the
// api-server's authz endpoint. api-server already caches access decisions
// internally, but the network hop + serialization cost adds up — caching here
// drops that to ~1 call per (user, account) per TTL window.
//
// Cache positive AND negative results: throttles repeated probing as much as
// it speeds up legitimate flows.
//
// Staleness tradeoff: when an admin revokes access, the user retains it for up
// to CACHE_TTL_MS. 5 min matches typical session-cookie freshness.
const CACHE_TTL_MS = 5 * 60 * 1000;
const CACHE_MAX_ENTRIES = 10000;
type CacheEntry = { value: boolean; expiresAt: number };
const accessCache = new Map<string, CacheEntry>();

function cacheKey(userId: string, tenantId: string, accountId: string): string {
  return `${tenantId}::${userId}::${accountId}`;
}

function cacheGet(key: string): boolean | undefined {
  const entry = accessCache.get(key);
  if (!entry) return undefined;
  if (Date.now() > entry.expiresAt) {
    accessCache.delete(key);
    return undefined;
  }
  return entry.value;
}

function cacheSet(key: string, value: boolean): void {
  // FIFO eviction when over capacity. Map preserves insertion order, so the
  // first key is the oldest. Not strict LRU, but entries expire on the TTL
  // anyway — eviction is a safety net for cache-key explosion, not a fairness
  // mechanism.
  if (accessCache.size >= CACHE_MAX_ENTRIES) {
    const oldest = accessCache.keys().next().value;
    if (oldest !== undefined) accessCache.delete(oldest);
  }
  accessCache.set(key, { value, expiresAt: Date.now() + CACHE_TTL_MS });
}

export async function hasAccountAccess(userId: string, tenantId: string, accountId: string, traceParent: string): Promise<boolean> {
  // Fail-fast guard: empty identifiers can only ever produce a denial from the
  // api-server, so short-circuit the HTTP roundtrip. Also documents the
  // invariant that all three are required for an authz decision.
  if (!userId || !tenantId || !accountId) {
    return false;
  }

  const key = cacheKey(userId, tenantId, accountId);
  const cached = cacheGet(key);
  if (cached !== undefined) {
    return cached;
  }

  const tracer = trace.getTracer('account-access');
  const span = tracer.startSpan('hasAccountAccess');

  try {
    const authResp = await fetch(auditEndpoint + '/v1/authz/validate_access', {
      headers: {
        'Content-Type': 'application/json',
        traceparent: traceParent,
        'X-Request-ID': traceParent,
        'X-ACTION-TOKEN': servicesServerToken,
      },
      body: JSON.stringify({
        user_id: userId,
        access: [
          {
            tenant_id: tenantId,
            permission: 'read',
            category: 'ACCOUNTS',
            args: { account_id: accountId },
          },
        ],
      }),
      method: 'post',
    });

    if (authResp.ok) {
      const responseJson = await authResp.json();
      const allowed = responseJson?.access && responseJson.access?.length > 0 && responseJson.access[0]?.allowed;
      if (allowed) {
        span.setStatus({ code: SpanStatusCode.OK });
      } else {
        span.setStatus({ code: SpanStatusCode.ERROR, message: 'Access denied' });
      }
      cacheSet(key, allowed);
      return allowed;
    }

    span.setStatus({
      code: SpanStatusCode.ERROR,
      message: `Access API returned ${authResp.status}`,
    });
  } catch (e: any) {
    span.recordException(e);
    span.setStatus({ code: SpanStatusCode.ERROR, message: e.message });
  } finally {
    span.end();
  }
  // Don't cache transient failures — caller will retry next request and the
  // api-server will get a chance to answer authoritatively.
  return false;
}
