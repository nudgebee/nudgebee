import type { NextApiRequest, NextApiResponse } from 'next';

export const config = {
  api: {
    bodyParser: false,
  },
};

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.log('newrelic - url', req.url);
    console.log('newrelic - headers', req.headers);

    if (req.method === 'POST') {
      const chunks: Buffer[] = [];
      for await (const chunk of req) {
        chunks.push(typeof chunk === 'string' ? Buffer.from(chunk) : chunk);
      }
      const buffer = Buffer.concat(chunks);
      console.log('newrelic - body:', buffer.toString());
      return res.status(200).json({ status: 'ok' });
    }

    // Webhook validation and processing will be implemented in future iterations
    return res.status(200).json({ status: 'ok' });
  } catch (err: any) {
    console.error(err);
    return res.status(500).json({ error: err.toString() });
  }
}
