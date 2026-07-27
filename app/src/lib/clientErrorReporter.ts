// Fire-and-forget client-error beacon → POST /api/client-errors → Loki.
//
// Why this is deliberately NOT built on HttpService/axios: the reporter must
// never route through the instrumented API client. A failing API call reports
// an error; if that report were itself an instrumented API call and it failed,
// it would report again — an amplifying loop. So we use raw navigator.sendBeacon
// (with a keepalive fetch fallback) straight to /api/client-errors, which is the
// one route excluded from reporting.
//
// Captures: uncaught JS errors, unhandled promise rejections, React error
// boundaries, explicitly-handled errors, and API-call failures. The server
// route logs each to stdout and best-effort pushes to Loki.

export type ClientErrorKind = 'js-error' | 'unhandled-rejection' | 'react-boundary' | 'handled' | 'api-failure';

export interface ClientErrorPayload {
  kind: ClientErrorKind;
  message: string;
  stack?: string;
  componentStack?: string;
  /** Context label: boundary name, GraphQL operationName, or error source. */
  source?: string;
  /** Upstream action / GraphQL root-field name (api-failure). */
  action?: string;
  /** HTTP status for api-failure. */
  status?: number;
  /** GraphQL errors array from the response (api-failure). */
  errors?: unknown;
  /** Request variables (api-failure) — only set when body capture is enabled. May contain secrets/PII. */
  variables?: unknown;
  /** Full response body (api-failure) — only set when body capture is enabled. May contain secrets/PII. */
  responseBody?: unknown;
  /** W3C trace id, when known. */
  traceId?: string;
  /** Small extra context. Stringified + truncated server-side is avoided; keep it small. */
  meta?: Record<string, unknown>;
}

const MAX_STR = 4000;
const MAX_BODY = 8000; // larger cap for request variables / response bodies
const WINDOW_MS = 10_000; // dedupe + rate window
const MAX_PER_WINDOW = 20; // hard cap on reports per window (storm guard)
const MAX_RECENT_KEYS = 500;

// Capture-all by default (errors are rare; the rate cap handles storms). Lower
// via NEXT_PUBLIC_CLIENT_ERROR_SAMPLE=0.1 etc. Kill entirely with *_DISABLED=true.
const SAMPLE = Number(process.env.NEXT_PUBLIC_CLIENT_ERROR_SAMPLE ?? '1') || 1;
const DISABLED = process.env.NEXT_PUBLIC_CLIENT_ERROR_DISABLED === 'true';

let sending = false; // synchronous recursion guard
let windowStart = 0;
let countInWindow = 0;
const recent = new Map<string, number>(); // dedupe key -> last-sent ts

/** Serialize an arbitrary value (object/array/primitive) to a length-bounded string. */
function serializeBody(v: unknown): string | undefined {
  if (v == null) return undefined;
  let str: string;
  try {
    str = typeof v === 'string' ? v : JSON.stringify(v);
  } catch {
    str = String(v); // circular / non-serializable
  }
  return str.length > MAX_BODY ? `${str.slice(0, MAX_BODY)}…[truncated]` : str;
}

function truncate(s?: string): string | undefined {
  if (s == null) return undefined;
  const str = String(s);
  return str.length > MAX_STR ? `${str.slice(0, MAX_STR)}…[truncated]` : str;
}

function pruneRecent(now: number) {
  if (recent.size <= MAX_RECENT_KEYS) return;
  for (const [k, ts] of recent) {
    if (now - ts > WINDOW_MS) recent.delete(k);
  }
}

export function reportClientError(payload: ClientErrorPayload): void {
  try {
    if (DISABLED || typeof window === 'undefined') return; // client-only
    if (sending) return; // never report while reporting
    if (SAMPLE < 1 && Math.random() > SAMPLE) return;

    const now = Date.now();
    if (now - windowStart > WINDOW_MS) {
      windowStart = now;
      countInWindow = 0;
    }
    // Drop repeat bursts of the same error, and cap total volume per window.
    const key = `${payload.kind}|${payload.source ?? ''}|${payload.message}`.slice(0, 200);
    const last = recent.get(key);
    if (last && now - last < WINDOW_MS) return;
    if (countInWindow >= MAX_PER_WINDOW) return;
    recent.set(key, now);
    countInWindow++;
    pruneRecent(now);

    const body = JSON.stringify({
      kind: payload.kind,
      message: truncate(payload.message) ?? 'unknown',
      stack: truncate(payload.stack),
      componentStack: truncate(payload.componentStack),
      source: truncate(payload.source),
      action: truncate(payload.action),
      status: payload.status,
      errors: serializeBody(payload.errors),
      variables: serializeBody(payload.variables),
      responseBody: serializeBody(payload.responseBody),
      traceId: payload.traceId,
      meta: payload.meta ? truncate(JSON.stringify(payload.meta)) : undefined,
      url: truncate(window.location?.href),
      userAgent: navigator.userAgent,
      ts: new Date(now).toISOString(),
    });

    sending = true;
    // sendBeacon survives page unloads (page-break navigations); keepalive fetch
    // is the fallback for browsers/paths where sendBeacon isn't available.
    const beaconOk =
      typeof navigator.sendBeacon === 'function' && navigator.sendBeacon('/api/client-errors', new Blob([body], { type: 'application/json' }));
    if (!beaconOk) {
      void fetch('/api/client-errors', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body,
        keepalive: true,
      }).catch(() => {
        /* swallow — the reporter must never surface its own failure */
      });
    }
  } catch {
    /* the reporter must never throw */
  } finally {
    sending = false;
  }
}
