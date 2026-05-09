import type { NextApiRequest, NextApiResponse } from 'next';

const APP_IDS: Record<string, string> = {
  rackspace: 'f340b7f9-1b3f-42ab-80a7-b52055a902fc',
};

const DEFAULT_APP_ID = 'bc08fe9e-cab5-4078-9eca-f74741d67188';

export default function handler(_req: NextApiRequest, res: NextApiResponse) {
  const title = (process.env.DEFAULT_TITLE || '').toLowerCase();
  const applicationId = APP_IDS[title] || DEFAULT_APP_ID;

  res.setHeader('Content-Type', 'application/json');
  res.status(200).json({
    associatedApplications: [{ applicationId }],
  });
}
