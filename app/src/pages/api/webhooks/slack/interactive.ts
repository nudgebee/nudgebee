import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import * as crypto from 'crypto';
import formurlencoded from 'form-urlencoded';

const SLACK_SIGNING_SECRET = process.env.SLACK_SIGNING_SECRET ?? '';
const SLACK_REPLAY_WINDOW_SECONDS = 60 * 5;

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug(`Incoming request to interactive slack api - Method: ${req.method}`);

    // Reject when the signing secret is unset (don't HMAC over an empty key).
    if (!SLACK_SIGNING_SECRET) {
      console.error('SLACK_SIGNING_SECRET not configured — refusing to process Slack webhook');
      return res.status(503).send('Error: Slack integration not configured');
    }

    const requestSignature = req.headers['x-slack-signature'] as string;
    const timestampHeader = req.headers['x-slack-request-timestamp'] as string;
    if (!requestSignature || !timestampHeader) {
      return res.status(401).send('Error: Missing Slack signature headers');
    }

    // Reject stale/replayed requests (Slack's recommended 5-minute window).
    const timestamp = Number(timestampHeader);
    if (!Number.isFinite(timestamp) || Math.abs(Date.now() / 1000 - timestamp) > SLACK_REPLAY_WINDOW_SECONDS) {
      return res.status(401).send('Error: Stale request timestamp');
    }

    let rawBody;
    if (req.headers['content-type']?.toLocaleLowerCase() === 'application/x-www-form-urlencoded') {
      rawBody = formurlencoded(req.body);
    } else {
      rawBody = JSON.stringify(req.body)
        .replace(/\//g, '\\/')
        .replace(/[\u007f-\uffff]/g, (c) => '\\u' + ('0000' + c.charCodeAt(0).toString(16)).slice(-4));
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
      console.log('WEBHOOK SIGNATURE MISMATCH');
      return res.status(401).send('Error: Signature mismatch security error');
    }
    res.status(200).send('OK');
    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';

    const response = await axios.post(endpoint + '/webhooks/slack/interactive', JSON.parse(req.body.payload), {
      headers: { 'X-ACTION-TOKEN': process.env.ACTION_API_SERVER_TOKEN ?? '' },
      timeout: 5000,
    });
    console.log(`Response from notification service - Status Code: ${response.status}, Response Body: ${response.data}`);
    return;
  } catch (err: any) {
    console.error(err);
    if (!res.headersSent) {
      return res.status(500).json({ error: err.toString() });
    }
  }
}
