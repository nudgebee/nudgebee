import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import * as crypto from 'crypto';

const MS_TEAMS_CLIENT_SECRET = process.env.MS_TEAMS_CLIENT_SECRET ?? '';

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug(`Incoming request to ms teams events api - Method: ${req.method}`);

    const requestSignature = req.headers['x-ms-signature'] as string;

    if (requestSignature && MS_TEAMS_CLIENT_SECRET) {
      const rawBody = JSON.stringify(req.body);
      const calculatedSignature = crypto.createHmac('sha256', MS_TEAMS_CLIENT_SECRET).update(rawBody).digest('base64');

      const actualSignatureBuf = Buffer.from(requestSignature, 'base64');
      const expectedSignatureBuf = Buffer.from(calculatedSignature, 'base64');

      if (actualSignatureBuf.length !== expectedSignatureBuf.length || !crypto.timingSafeEqual(actualSignatureBuf, expectedSignatureBuf)) {
        console.warn('WEBHOOK SIGNATURE MISMATCH');
        return res.status(401).send('Error: Signature mismatch security error');
      }
    }

    const payload = req.body;

    if (payload.type === 'message' && payload.text === 'verify') {
      return res.status(200).send('OK');
    }

    res.status(200).send('OK');

    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
    const response = await axios.post(endpoint + '/webhooks/msteams/events', req.body);
    console.log(`Response from notification service - Status Code: ${response.status}, Response Body: ${response.data}`);

    return;
  } catch (err: any) {
    console.error(err);
    return res.status(500).json({ error: err.toString() });
  }
}
