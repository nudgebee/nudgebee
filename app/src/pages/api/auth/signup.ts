import type { NextApiRequest, NextApiResponse } from 'next';
import { getUserByUsername } from '@lib/UserService';
import { v4 as uuidv4 } from 'uuid';
import { queryGraphQL } from '@lib/HttpService';
import axios from 'axios';
import { isOnPremMode } from '@lib/license';

function verifyEmail(email: string) {
  if (!email) {
    return 'Email is required';
  }
  if (email.includes('+')) {
    return 'Email is invalid';
  }

  // varify using regex
  const emailPattern = /[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+.[a-zA-Z]{2,4}$/;
  if (!emailPattern.test(email)) {
    return 'Email is invalid';
  }

  return '';
}

function verifyDisplayName(displayName: string) {
  if (!displayName) {
    return 'Display Name required';
  }

  // should start with alphabet, can have spaces & length min 3 char & max 30 char
  const displayNamePattern = /^[a-zA-Z][a-zA-Z\s]{1,28}[a-zA-Z]$/;
  if (!displayNamePattern.test(displayName)) {
    return 'Display Name is invalid (should start with alphabet, can have spaces & length min 3 char & max 30 char)';
  }

  return '';
}

function verifyOrgName(orgName: string) {
  if (!orgName) {
    return 'Org Name is required';
  }
  // should start with alphabet, can have spaces & length min 3 char & max 30 char
  const displayNamePattern = /^[a-zA-Z][a-zA-Z0-9\s]{1,28}[a-zA-Z0-9]$/;
  if (!displayNamePattern.test(orgName)) {
    return 'Org Name is invalid (should start with alphabet, can have spaces & length min 3 char & max 30 char)';
  }

  return '';
}

async function isEmailAlreadyExists(email: string) {
  const response = await getUserByUsername({
    username: email,
    fetchAccounts: true,
    fetchGroups: false,
    fetchRoles: false,
    fetchAttrbutes: false,
  });

  if (response.data && response.data.users.length > 0) {
    return true;
  } else if (response.data && response.data.users.length == 0) {
    return false;
  }
  console.log('onboarding error, unable to validate email', JSON.stringify(response));

  throw new Error(response.data.errors);
}

async function generateAndSendRegistrationEmail(req: NextApiRequest, data: any) {
  const token = `${uuidv4()}-${uuidv4()}-${uuidv4()}`;
  const baseUrl = process.env.BASE_URL ?? '';
  const url = `${baseUrl}/signup_verify?token=${token}`;

  const DELETE_EXISTING_TOKEN = `mutation TenantTokenDelete($username: String!) {
    tenant_onboarding_delete_by_username(username: $username) {
      affected_rows
    }
  }`;

  const INSERT_TOKEN = `mutation TenantOnboarding($username: String!, $verification_token: String!, $verification_token_expiration: String!, $tenant_name: String, $user_displayname: String) {
    tenant_onboarding_insert(username: $username, verification_token: $verification_token, verification_token_expiration: $verification_token_expiration, tenant_name: $tenant_name, user_displayname: $user_displayname) {
      id
    }
  }`;

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

  // delete any existin token based on emailId
  const gqlDeleteRespomse = await queryGraphQL(
    DELETE_EXISTING_TOKEN,
    'TenantTokenDelete',
    {
      username: data.email,
    },
    extraHeaders
  );
  console.log('delete response', JSON.stringify(gqlDeleteRespomse.data));
  if (gqlDeleteRespomse.data.errors) {
    console.log('onboarding error, unable to delete existing token', JSON.stringify(gqlDeleteRespomse), data.email);
    throw new Error(gqlDeleteRespomse.data.errors);
  }

  // generate new token
  const gqlRespomse = await queryGraphQL(
    INSERT_TOKEN,
    'TenantOnboarding',
    {
      username: data.email,
      user_displayname: data.fullname,
      tenant_name: data.orgname,
      verification_token: token,
      verification_token_expiration: new Date(new Date().getTime() + 15 * 60000).toISOString(),
    },
    extraHeaders
  );

  if (gqlRespomse.data.errors) {
    console.log('onboarding error, unable to add user', JSON.stringify(gqlRespomse.data), data.email);
    throw new Error(gqlRespomse.data.errors);
  }

  const notificationServiceUrl = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
  await axios.post(`${notificationServiceUrl}/api/send_email`, {
    recipients: data.email,
    subject: 'Nudgebee Registration',
    template: 'signup_verification',
    template_params: { verification_url: url },
  });
}

async function generateAndSendUserExistsEmail(req: NextApiRequest, email: string) {
  const baseUrl = process.env.BASE_URL ?? '';
  const notificationServiceUrl = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
  await axios.post(`${notificationServiceUrl}/api/send_email`, {
    recipients: email,
    subject: 'Nudgebee Registration',
    template: 'signup_user_exists',
    template_params: { recipient_email: email, base_url: baseUrl },
  });
}

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

  const data = req.body;
  let message = '';
  try {
    const dataJson = typeof data === 'string' ? JSON.parse(data) : data;

    message = verifyEmail(dataJson?.email);

    if (!message) {
      message = verifyDisplayName(dataJson?.fullname);
    }

    if (!message) {
      message = verifyOrgName(dataJson?.orgname);
    }

    if (message) {
      res.status(400).json({
        message,
      });
      return;
    }

    console.log('checking if user exists', dataJson);
    const isEmailExists = await isEmailAlreadyExists(dataJson?.email);
    if (isEmailExists) {
      await generateAndSendUserExistsEmail(req, dataJson?.email);
      res.status(200).json({
        message: 'Success',
      });
      return;
    }

    console.log('generating registrationEmail', dataJson);
    await generateAndSendRegistrationEmail(req, dataJson);

    //send verification email
    res.status(200).json({
      message: 'Success',
    });
  } catch (error: any) {
    res.status(error.status || 500).json({
      message: 'Unable to register user, please try again after sometime',
    });
  }
}
