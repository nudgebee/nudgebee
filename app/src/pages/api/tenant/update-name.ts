import { getToken } from 'next-auth/jwt';
import { getServerSession } from 'next-auth/next';
import type { NextApiRequest, NextApiResponse } from 'next';
import { authOptions } from '@pages/api/auth/[...nextauth]';
import { updateTenantName } from '@lib/UserService';

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

  const { tenantName } = req.body;
  if (!tenantName || typeof tenantName !== 'string') {
    return res.status(400).json({ error: 'missing_tenant_name' });
  }

  try {
    const response = await updateTenantName(tenantId, tenantName);
    return res.status(200).json(response?.data ?? {});
  } catch (error: any) {
    console.error('Failed to update tenant name:', error);
    return res.status(500).json({ error: error.message || 'Internal Server Error' });
  }
}
