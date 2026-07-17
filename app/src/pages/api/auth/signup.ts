import type { NextApiRequest, NextApiResponse } from 'next';
import { getUserByUsername } from '@lib/UserService';
import { v4 as uuidv4 } from 'uuid';
import { queryGraphQL } from '@lib/HttpService';
import axios from 'axios';
import { getLicenseDetails } from '@lib/license';

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
  throw new Error(response.data.errors);
}

async function generateAndSendRegistrationEmail(req: NextApiRequest, data: any) {
  const token = `${uuidv4()}-${uuidv4()}-${uuidv4()}`;
  const baseUrl = process.env.BASE_URL ?? '';
  const url = `${baseUrl}/signup_verify?token=${token}`;

  const DELETE_EXISTING_TOKEN = `mutation TenantTokenDelete($username: String!) {
    signup_delete(username: $username) {
      affected_rows
    }
  }`;

  const INSERT_TOKEN = `mutation TenantOnboarding($username: String!, $verification_token: String!, $verification_token_expiration: String!, $tenant_name: String, $user_displayname: String) {
    signup_create(username: $username, verification_token: $verification_token, verification_token_expiration: $verification_token_expiration, tenant_name: $tenant_name, user_displayname: $user_displayname) {
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
  if (gqlDeleteRespomse.data.errors) {
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
    throw new Error(gqlRespomse.data.errors);
  }

  const notificationServiceUrl = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
  const notificationToken = process.env.NOTIFICATION_SERVER_TOKEN;
  await axios.post(
    `${notificationServiceUrl}/api/emails/send`,
    {
      recipients: data.email,
      subject: 'Nudgebee Registration',
      template: 'signup_verification',
      template_params: { verification_url: url },
    },
    notificationToken ? { headers: { 'X-ACTION-TOKEN': notificationToken } } : undefined
  );
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'POST') {
    res.status(405).json({
      message: 'Method not allowed',
    });
    return;
  }

  // Self-signup is only allowed on SaaS-tier deployments. Match the gate
  // used by the signup pages' getServerSideProps so frontend and backend
  // agree even if env vars and the license server disagree.
  let signupAllowed = false;
  try {
    const license = await getLicenseDetails();
    signupAllowed = license.tier === 'saas';
  } catch {
    signupAllowed = false;
  }
  if (!signupAllowed) {
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

    const isEmailExists = await isEmailAlreadyExists(dataJson?.email);
    if (isEmailExists) {
      // Email already belongs to an existing tenant: surface an inline alert on
      // the sign-up page instead of sending an email. A non-200 status makes the
      // sign-up page render `message` as the email-field helper text.
      res.status(409).json({
        message: 'This email is already associated with an existing account. Please sign in instead.',
      });
      return;
    }

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
