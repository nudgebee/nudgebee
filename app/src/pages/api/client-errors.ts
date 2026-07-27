// Sink for browser-side error/network-failure beacons (see @lib/clientErrorReporter).
//
// Every accepted report is written as a single structured `[client-error]` line
// to stdout (grep-able wherever the app's logs land) AND best-effort pushed to
// Loki when CLIENT_ERROR_LOKI_URL is set (the reliable path — the monorepo has
// no pod-stdout log shipper). Untrusted, so: POST-only, small body cap, newline
// stripping (log-injection guard), per-field truncation, fixed field allowlist.

import type { NextApiRequest, NextApiResponse } from 'next';

// Cap the request body so a hostile/looping client can't flood the pipeline.
// 64kb accommodates full request-variables + response bodies (capture-bodies mode).
export const config = { api: { bodyParser: { sizeLimit: '64kb' } } };

// Loki HTTP push endpoint, e.g. http://loki-gateway.loki.svc.cluster.local/loki/api/v1/push
const LOKI_URL = process.env.CLIENT_ERROR_LOKI_URL;
const ENV = process.env.APP_ENV ?? process.env.NEXT_PUBLIC_ENV ?? 'unknown';
const LOKI_TIMEOUT_MS = 2000;

// Whether to log the raw request variables + full response body. These can carry
// secrets (integration API keys/passwords) and tenant PII, so it's a per-env
// RUNTIME toggle (server-side, so it works via deploy config — unlike a build-time
// NEXT_PUBLIC_ flag). Off by default → dropped before anything is logged/pushed.
// Enable in dev only: CLIENT_ERROR_CAPTURE_BODIES=true.
const CAPTURE_BODIES = process.env.CLIENT_ERROR_CAPTURE_BODIES === 'true';

let warnedNoLoki = false;

// Coerce to a length-bounded string. Raw objects are JSON-stringified (a client
// could POST a raw payload). Newlines are stripped by default as a defensive
// habit, but the whole record is emitted via JSON.stringify (which escapes them
// anyway), so multi-line fields like stacks pass stripNewlines=false to stay
// readable in Loki.
function clean(v: unknown, max = 4000, stripNewlines = true): string {
  if (v == null) return '';
  const strVal = typeof v === 'object' ? JSON.stringify(v) : String(v);
  const sliced = strVal.slice(0, max);
  return stripNewlines ? sliced.replace(/[\r\n\t]+/g, ' ') : sliced;
}

async function pushToLoki(line: string, labels: Record<string, string>): Promise<void> {
  if (!LOKI_URL) {
    if (!warnedNoLoki) {
      console.warn('[client-error] CLIENT_ERROR_LOKI_URL unset — client errors go to stdout only');
      warnedNoLoki = true;
    }
    return;
  }
  const tsNs = String(Date.now() * 1_000_000); // Loki wants ns-epoch as a string
  const payload = { streams: [{ stream: labels, values: [[tsNs, line]] }] };
  try {
    const res = await fetch(LOKI_URL, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(payload),
      signal: AbortSignal.timeout(LOKI_TIMEOUT_MS),
    });
    if (!res.ok) console.error('[client-error] loki push rejected', res.status);
  } catch (e) {
    console.error('[client-error] loki push failed', (e as Error)?.message);
  }
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    res.setHeader('Allow', 'POST');
    return res.status(405).end();
  }

  const b = (req.body ?? {}) as Record<string, unknown>;
  const record = {
    kind: clean(b.kind, 40) || 'unknown',
    message: clean(b.message),
    source: clean(b.source, 200),
    action: clean(b.action, 200),
    status: typeof b.status === 'number' ? b.status : undefined,
    errors: clean(b.errors, 8000),
    // Bodies may contain secrets/PII — dropped unless explicitly enabled for this env.
    variables: CAPTURE_BODIES ? clean(b.variables, 8000) : '',
    responseBody: CAPTURE_BODIES ? clean(b.responseBody, 8000) : '',
    traceId: clean(b.traceId, 64),
    url: clean(b.url, 1000),
    userAgent: clean(b.userAgent, 300),
    stack: clean(b.stack, 4000, false),
    componentStack: clean(b.componentStack, 4000, false),
    meta: clean(b.meta, 2000),
    ts: clean(b.ts, 40),
    ip: clean(req.headers['x-forwarded-for'] ?? req.socket?.remoteAddress, 64),
  };
  const line = JSON.stringify(record);

  console.error('[client-error]', line);
  await pushToLoki(line, { app: 'nextjs-app', source: 'client-errors', env: ENV, kind: record.kind });

  return res.status(204).end();
}
