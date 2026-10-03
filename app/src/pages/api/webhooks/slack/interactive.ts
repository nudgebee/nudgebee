import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import { verifySlackRequest } from '@lib/slackWebhook';

// Slack's HMAC is over the raw body — let verifySlackRequest read the stream.
export const config = { api: { bodyParser: false } };

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug('Incoming request to interactive slack api', { method: req.method });

    const verified = await verifySlackRequest(req);
    if (!verified.ok) {
      return res.status(verified.status).send(verified.message);
    }

    // Empty ack, not a literal "OK" body: for legacy interactive_message
    // actions, Slack renders whatever non-empty text this synchronous
    // response contains as the message's new content. Sending 'OK' here
    // was committing that text as the message itself, not just showing a
    // generic loading placeholder -- the async chat.update below was always
    // racing to overwrite content Slack had already applied, not a fallback.
    res.status(200).end();
    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';

    const response = await axios.post(endpoint + '/webhooks/slack/interactive', JSON.parse(verified.body.payload), {
      headers: { 'X-ACTION-TOKEN': process.env.ACTION_API_SERVER_TOKEN ?? '' },
      timeout: 5000,
    });
    console.log('Response from notification service', { status: response.status, body: response.data });
    return;
  } catch (err: any) {
    console.error(err);
    if (!res.headersSent) {
      return res.status(500).json({ error: 'Internal server error' });
    }
  }
}
