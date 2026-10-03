import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import { verifySlackRequest } from '@lib/slackWebhook';

// Slack's HMAC is over the raw body — let verifySlackRequest read the stream.
export const config = { api: { bodyParser: false } };

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug('Incoming request to slack commands api', { method: req.method });

    const verified = await verifySlackRequest(req);
    if (!verified.ok) {
      return res.status(verified.status).send(verified.message);
    }
    const payload = verified.body;

    if (payload.type === 'url_verification') {
      res.setHeader('Content-Type', 'text/plain');
      return res.status(200).send(payload.challenge);
    }
    res.status(200).send('OK');

    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
    const response = await axios.post(endpoint + '/webhooks/slack/events', payload, {
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
