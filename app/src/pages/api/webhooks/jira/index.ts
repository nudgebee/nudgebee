import type { NextApiRequest, NextApiResponse } from 'next';

export const config = {
  api: {
    bodyParser: false,
  },
};

export default async function trigger(req: NextApiRequest, res: NextApiResponse) {
  try {
    console.log('jira - url', req.url);
    console.log('jira - headers', req.headers);

    if (req.method === 'POST') {
      const readable = req.read();
      const buffer = Buffer.from(readable);
      console.log('Raw request body:', buffer.toString());
      return res.status(200).json({ status: 'ok' });
    }

    // Webhook validation and processing will be implemented in future iterations
    return res.status(200).json({ status: 'ok' });
  } catch (err: any) {
    console.error(err);
    return res.status(500).json({ error: err.toString() });
  }
}
