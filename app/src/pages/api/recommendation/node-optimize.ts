import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';
import type { NextApiRequest, NextApiResponse } from 'next';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { queryGraphQL } from '@lib/HttpService';
import { NODE_RECOMMENDATION } from '@api1/recommendation';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    return res.status(405).json({ error: 'Method not allowed' });
  }

  const session = await getServerSession(req, res, authOptions);
  if (!session?.user) {
    return res.status(401).json({ error: 'not_authenticated' });
  }

  const token = await getToken({ req });
  const tenantId = (token?.tenant as any)?.id;
  if (!tenantId) {
    return res.status(400).json({ error: 'missing_tenant' });
  }

  const { accountId, graviton, instance_groups, number_of_recommendations } = req.body;
  if (!accountId) {
    return res.status(400).json({ error: 'missing_account_id' });
  }

  try {
    const response = await queryGraphQL(NODE_RECOMMENDATION, 'NodeRecommendation', {
      account: accountId,
      graviton: graviton ?? false,
      instance_groups: instance_groups ?? [],
      tenant_id: tenantId,
      number_of_recommendations: number_of_recommendations ?? 1,
    });
    return res.status(200).json(response?.data?.data ?? {});
  } catch (error: any) {
    console.error('Failed to get node recommendation:', error);
    return res.status(500).json({ error: error.message || 'Internal Server Error' });
  }
}
