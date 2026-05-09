import { queryGraphQL } from '@lib/HttpService';
import type { NextApiRequest, NextApiResponse } from 'next';
import { onboardUser } from '@lib/UserService';
import { isOnPremMode } from '@lib/license';

const updateTokenQuery = `mutation UpdateToken($id: String!, $status: String!, $updated_at: String!){
  tenant_onboarding_update_status(id: $id, status: $status, updated_at: $updated_at) {
    id
  }
}`;

const query = `query GetByToken($token: String!){
  tenant_onboarding_get_by_token(token: $token) {
    id
    verification_status
    verification_token_expiration
    username
    tenant_name
    user_displayname
  }
}`;

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    res.status(405).json({
      message: 'Method not allowed',
    });
    return;
  }
  if (isOnPremMode()) {
    res.status(400).json({
      message: 'Not Supported',
    });
    return;
  }

  const token = req.body.token;
  if (typeof token !== 'string' || token?.length === 0 || !token) {
    res.status(400).json({
      message: 'Invalid token',
    });
    return;
  }

  const extraHeaders: Record<string, string> = {};
  if (req.headers['traceparent']) {
    if (Array.isArray(req.headers['traceparent'])) {
      extraHeaders['traceparent'] = req.headers['traceparent'][0];
    } else {
      extraHeaders['traceparent'] = req.headers['traceparent'];
    }
  }
  if (req.headers['x-request-id']) {
    if (Array.isArray(req.headers['x-request-id'])) {
      extraHeaders['x-request-id'] = req.headers['x-request-id'][0];
    } else {
      extraHeaders['x-request-id'] = req.headers['x-request-id'];
    }
  }

  const response = await queryGraphQL(query, 'GetByToken', { token }, extraHeaders);
  if (response.data.errors) {
    res.status(500).json({
      message: 'Internal Error',
    });
    return;
  }

  if (response.data.data.tenant_onboarding_get_by_token.length === 0) {
    res.status(400).json({
      message: 'Invalid token',
    });
    return;
  }

  const tokenDetails = response.data.data.tenant_onboarding_get_by_token[0];
  if (!tokenDetails.verification_token_expiration.endsWith('Z')) {
    tokenDetails.verification_token_expiration = tokenDetails.verification_token_expiration + 'Z';
  }

  if (tokenDetails.verification_status === 'done') {
    res.status(400).json({
      message: 'Already Verified',
    });
    return;
  }

  if (new Date(tokenDetails.verification_token_expiration) < new Date()) {
    const response = await queryGraphQL(
      updateTokenQuery,
      'UpdateToken',
      {
        id: tokenDetails.id,
        status: 'expired',
        updated_at: new Date().toISOString(),
      },
      extraHeaders
    );
    if (response.data.errors) {
      console.log('Error updating token status', JSON.stringify(response.data.errors));
    }

    res.status(400).json({
      message: 'Token expired',
    });
    return;
  }

  try {
    const onboardResponse = await onboardUser({
      username: tokenDetails.username,
      display_name: tokenDetails.user_displayname,
      role: 'tenant_admin',
      status: 'active',
    });

    if (onboardResponse.data.errors) {
      console.log('Error onboarding user', JSON.stringify(onboardResponse));
      res.status(500).json({
        message: 'Internal Error',
      });
      return;
    }

    const response = await queryGraphQL(
      updateTokenQuery,
      'UpdateToken',
      {
        id: tokenDetails.id,
        status: 'done',
        updated_at: new Date().toISOString(),
      },
      extraHeaders
    );
    if (response.data.errors) {
      console.log('Error updating token status', JSON.stringify(response.data.errors));
      res.status(500).json({
        message: 'Internal Error',
      });
      return;
    }
    //send verification email
    res.status(200).json({
      message: 'Success',
    });
  } catch (error: any) {
    console.log('Error onboarding user', JSON.stringify(error));
    res.status(error.status || 500).json({
      message: 'Internal Error',
    });
  }
}
