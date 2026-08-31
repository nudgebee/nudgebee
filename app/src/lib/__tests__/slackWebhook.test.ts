// verifySlackRequest's contract. The big-integer test is the nb #37285
// regression guard: hashing a re-serialized body rounds ints > 2^53.

import { Readable } from 'node:stream';
import * as crypto from 'node:crypto';

const SECRET = 'test-signing-secret';

let verifySlackRequest: typeof import('@lib/slackWebhook').verifySlackRequest;

function loadWithSecret(secret: string | undefined) {
  if (secret === undefined) {
    delete process.env.SLACK_SIGNING_SECRET;
  } else {
    process.env.SLACK_SIGNING_SECRET = secret;
  }
  jest.isolateModules(() => {
    verifySlackRequest = require('@lib/slackWebhook').verifySlackRequest;
  });
}

beforeEach(() => loadWithSecret(SECRET));

interface MakeReqOpts {
  contentType?: string;
  timestamp?: string;
  signWith?: string | null; // null = no signature header at all
  signBody?: string; // sign this instead of rawBody (tamper)
}

function makeReq(rawBody: string, opts: MakeReqOpts = {}) {
  const ts = opts.timestamp ?? String(Math.floor(Date.now() / 1000));
  const headers: Record<string, string> = {
    'content-type': opts.contentType ?? 'application/json',
    'x-slack-request-timestamp': ts,
  };
  if (opts.signWith !== null) {
    const secret = opts.signWith ?? SECRET;
    const signed = opts.signBody ?? rawBody;
    headers['x-slack-signature'] = 'v0=' + crypto.createHmac('sha256', secret).update(`v0:${ts}:${signed}`).digest('hex');
  }
  const req = Readable.from([Buffer.from(rawBody, 'utf8')]) as any;
  req.headers = headers;
  return req;
}

describe('verifySlackRequest', () => {
  it('accepts a JSON body and returns it parsed', async () => {
    const body = JSON.stringify({ type: 'url_verification', challenge: 'abc123' });
    const result = await verifySlackRequest(makeReq(body));
    expect(result).toEqual({ ok: true, body: { type: 'url_verification', challenge: 'abc123' } });
  });

  it('accepts a form-urlencoded body and exposes the raw payload field', async () => {
    const inner = JSON.stringify({ type: 'block_actions', actions: [{ action_id: 'ack' }] });
    const raw = `payload=${encodeURIComponent(inner)}`;
    const result = await verifySlackRequest(makeReq(raw, { contentType: 'application/x-www-form-urlencoded' }));
    expect(result.ok).toBe(true);
    if (result.ok) expect(JSON.parse(result.body.payload)).toEqual(JSON.parse(inner));
  });

  // nb #37285: 8790099056701638497 (a Datadog event_id) can't round-trip through V8.
  it('verifies a body with an integer above 2^53 (does not round-trip through JSON)', async () => {
    const raw = '{"event":{"metadata":{"event_payload":{"event_id":8790099056701638497,"monitor_id":16207777}}}}';
    expect(JSON.stringify(JSON.parse(raw))).not.toBe(raw);
    const result = await verifySlackRequest(makeReq(raw));
    expect(result.ok).toBe(true);
  });

  it('verifies against the raw bytes, not a reserialization (form)', async () => {
    const inner = JSON.stringify({ url: 'https://x.test/a/b', team: 'café', nested: { z: 1, a: 2 } });
    const raw = `payload=${encodeURIComponent(inner)}&extra=a+b%2Fc`;
    const result = await verifySlackRequest(makeReq(raw, { contentType: 'application/x-www-form-urlencoded' }));
    expect(result.ok).toBe(true);
  });

  it('verifies against the raw bytes, not a reserialization (json key order + slashes)', async () => {
    // Keys deliberately not alphabetical; forward slashes unescaped.
    const raw = '{"z":"a/b/c","a":1,"m":"http://x.test"}';
    const result = await verifySlackRequest(makeReq(raw));
    expect(result.ok).toBe(true);
    if (result.ok) expect(result.body.z).toBe('a/b/c');
  });

  it('rejects a tampered signature with 401', async () => {
    const body = JSON.stringify({ hello: 'world' });
    const result = await verifySlackRequest(makeReq(body, { signBody: JSON.stringify({ hello: 'evil' }) }));
    expect(result).toEqual({ ok: false, status: 401, message: 'Error: Signature mismatch security error' });
  });

  it('rejects a body signed with the wrong secret', async () => {
    const body = JSON.stringify({ hello: 'world' });
    const result = await verifySlackRequest(makeReq(body, { signWith: 'not-the-secret' }));
    expect(result.ok).toBe(false);
    if (!result.ok) expect(result.status).toBe(401);
  });

  it('rejects when signature headers are missing', async () => {
    const result = await verifySlackRequest(makeReq('{}', { signWith: null }));
    expect(result).toEqual({ ok: false, status: 401, message: 'Error: Missing Slack signature headers' });
  });

  it('rejects a stale timestamp outside the 5-minute window', async () => {
    const old = String(Math.floor(Date.now() / 1000) - 6 * 60);
    const result = await verifySlackRequest(makeReq('{}', { timestamp: old }));
    expect(result).toEqual({ ok: false, status: 401, message: 'Error: Stale request timestamp' });
  });

  it('rejects a malformed JSON body with 400 (after the signature passes)', async () => {
    const result = await verifySlackRequest(makeReq('{not json'));
    expect(result).toEqual({ ok: false, status: 400, message: 'Error: Malformed request body' });
  });

  it('rejects a body over the 1MB cap with 413 (bodyParser is off on these routes)', async () => {
    const result = await verifySlackRequest(makeReq('x'.repeat(1024 * 1024 + 1)));
    expect(result).toEqual({ ok: false, status: 413, message: 'Error: Request body too large' });
  });

  it('refuses when the signing secret is unset', async () => {
    loadWithSecret(undefined);
    const result = await verifySlackRequest(makeReq('{}', { signWith: null }));
    expect(result).toEqual({ ok: false, status: 503, message: 'Error: Slack integration not configured' });
  });
});
