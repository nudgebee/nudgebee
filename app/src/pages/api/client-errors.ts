// Sink for browser-side error/network-failure beacons (see @lib/clientErrorReporter).
//
// Every accepted report is emitted as a single-line JSON record on stderr, tagged
// `log_type: "client-error"`. That's the whole delivery mechanism: the cluster's
// log shipper (promtail today, whatever replaces it tomorrow) already tails pod
// stdout/stderr, so the record lands in the log backend with real pod labels
// (namespace/pod/container/node) for free. Query:
//
//   {namespace="nudgebee", container="app"} |= "client-error" | json | kind="api-failure"
//
// The line is pure JSON — no human prefix — because LogQL's `| json` parser
// requires the whole line to parse.
//
// Untrusted input, so: POST-only, small body cap, newline stripping (log-injection
// guard), per-field truncation, fixed field allowlist.

import type { NextApiRequest, NextApiResponse } from 'next';

// Cap the request body so a hostile/looping client can't flood the pipeline.
// 64kb accommodates full request-variables + response bodies (capture-bodies mode).
export const config = { api: { bodyParser: { sizeLimit: '64kb' } } };

// Whether to log the raw request variables + full response body. These can carry
// secrets (integration API keys/passwords) and tenant PII, so it's a per-env
// RUNTIME toggle (server-side, so it works via deploy config — unlike a build-time
// NEXT_PUBLIC_ flag). Off by default → dropped before anything is logged/pushed.
// Enable in dev only: CLIENT_ERROR_CAPTURE_BODIES=true.
const CAPTURE_BODIES = process.env.CLIENT_ERROR_CAPTURE_BODIES === 'true';

// Coerce to a length-bounded string. Raw objects are JSON-stringified (a client
// could POST a raw payload). Newlines are stripped by default as a defensive
// habit, but the whole record is emitted via JSON.stringify (which escapes them
// anyway), so multi-line fields like stacks pass stripNewlines=false to stay
// readable in the log backend.
function clean(v: unknown, max = 4000, stripNewlines = true): string {
  if (v == null) return '';
  const strVal = typeof v === 'object' ? JSON.stringify(v) : String(v);
  const sliced = strVal.slice(0, max);
  return stripNewlines ? sliced.replace(/[\r\n\t]+/g, ' ') : sliced;
}

export default function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    res.setHeader('Allow', 'POST');
    return res.status(405).end();
  }

  const b = (req.body ?? {}) as Record<string, unknown>;
  const record = {
    // Discriminator: doubles as the cheap LogQL line filter (`|= "client-error"`)
    // before the `| json` parse.
    log_type: 'client-error',
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
  console.error(JSON.stringify(record));

  return res.status(204).end();
}
