import type { NextApiRequest } from 'next';
import * as crypto from 'crypto';

// Verify Slack's signature over the RAW request body, then parse. Hashing a
// re-serialization of an already-parsed body (the previous approach) rounds bare
// integers > 2^53 — e.g. the 19-digit event_id in Datadog alert payloads — so the
// hash stops matching what Slack signed (nb #37285). Routes MUST set
// `export const config = { api: { bodyParser: false } }`.

const SLACK_SIGNING_SECRET = process.env.SLACK_SIGNING_SECRET ?? '';
const SLACK_REPLAY_WINDOW_SECONDS = 60 * 5;
// Backstop for the now-unbounded read (Next's 1mb bodyParser limit no longer applies).
const MAX_BODY_BYTES = 1024 * 1024;

export type SlackVerifyResult = { ok: true; body: any } | { ok: false; status: number; message: string };

async function readRawBody(req: NextApiRequest): Promise<string | null> {
  const chunks: Buffer[] = [];
  let total = 0;
  for await (const chunk of req) {
    const buf = typeof chunk === 'string' ? Buffer.from(chunk) : chunk;
    total += buf.length;
    if (total > MAX_BODY_BYTES) {
      req.destroy(); // abort the upload rather than leave the stream half-read
      return null;
    }
    chunks.push(buf);
  }
  return Buffer.concat(chunks).toString('utf8');
}

export async function verifySlackRequest(req: NextApiRequest): Promise<SlackVerifyResult> {
  if (!SLACK_SIGNING_SECRET) {
    console.error('SLACK_SIGNING_SECRET not configured — refusing to process Slack webhook');
    return { ok: false, status: 503, message: 'Error: Slack integration not configured' };
  }

  const requestSignature = req.headers['x-slack-signature'] as string;
  const timestampHeader = req.headers['x-slack-request-timestamp'] as string;
  if (!requestSignature || !timestampHeader) {
    return { ok: false, status: 401, message: 'Error: Missing Slack signature headers' };
  }

  const timestamp = Number(timestampHeader);
  if (!Number.isFinite(timestamp) || Math.abs(Date.now() / 1000 - timestamp) > SLACK_REPLAY_WINDOW_SECONDS) {
    return { ok: false, status: 401, message: 'Error: Stale request timestamp' };
  }

  const rawBody = await readRawBody(req);
  if (rawBody === null) {
    return { ok: false, status: 413, message: 'Error: Request body too large' };
  }

  const basestring = ['v0', timestampHeader, rawBody].join(':');
  const calculatedSignature = 'v0=' + crypto.createHmac('sha256', SLACK_SIGNING_SECRET).update(basestring).digest('hex');
  const calculatedSignatureBuffer = Buffer.from(calculatedSignature, 'utf8');
  const requestSignatureBuffer = Buffer.from(requestSignature, 'utf8');

  // Length check first — timingSafeEqual throws on unequal-length buffers.
  if (
    calculatedSignatureBuffer.length !== requestSignatureBuffer.length ||
    !crypto.timingSafeEqual(calculatedSignatureBuffer, requestSignatureBuffer)
  ) {
    console.error('WEBHOOK SIGNATURE MISMATCH');
    return { ok: false, status: 401, message: 'Error: Signature mismatch security error' };
  }

  const contentType = req.headers['content-type']?.toLowerCase() ?? '';
  try {
    const body = contentType.includes('application/x-www-form-urlencoded')
      ? Object.fromEntries(new URLSearchParams(rawBody))
      : rawBody
      ? JSON.parse(rawBody)
      : {};
    return { ok: true, body };
  } catch {
    return { ok: false, status: 400, message: 'Error: Malformed request body' };
  }
}
