import type { NextApiRequest, NextApiResponse } from 'next';
import axios from 'axios';
import * as crypto from 'crypto';
import formurlencoded from 'form-urlencoded';

const SLACK_SIGNING_SECRET = process.env.SLACK_SIGNING_SECRET ?? '';

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.debug(`Incoming request to slack commands api - Method: ${req.method}`);

    let rawBody;
    const requestSignature = req.headers['x-slack-signature'] as string;
    const timestampHeader = req.headers['x-slack-request-timestamp'];
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

    if (!crypto.timingSafeEqual(calculatedSignatureBuffer, requestSignatureBuffer)) {
      console.log('WEBHOOK SIGNATURE MISMATCH');
      return res.status(400).send('Error: Signature mismatch security error');
    }

    const payload = req.body;
    if (payload.type === 'url_verification') {
      res.setHeader('Content-Type', 'text/plain');
      return res.status(200).send(payload.challenge);
    }
    res.status(200).send('OK');

    const endpoint = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
    const response = await axios.post(endpoint + '/webhooks/slack/events', req.body);
    console.log(`Response from notification service - Status Code: ${response.status}, Response Body: ${response.data}`);
    return res.status(response.status);
  } catch (err: any) {
    console.error(err);
    return res.status(500).json({ error: err.toString() });
  }
}
