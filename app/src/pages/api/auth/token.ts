import type { NextApiRequest, NextApiResponse } from 'next';
import { validateHashedPassword, encodeHasuraJWT, encrypt } from '@lib/internal';
import { updateUserAccountAccessed, getUserByUsernameAndAccountProviderAndCredential } from '@lib/UserService';

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const data = req.body;

  if (req.method !== 'POST') {
    res.status(405).json({
      message: 'Method not allowed',
    });
    return;
  }

  if (!data.email || !data.secret) {
    res.status(400).json({ message: 'email or secret missing' });
    return;
  }

  // check types
  if (typeof data.email !== 'string' || typeof data.secret !== 'string') {
    res.status(400).json({ message: 'email or secret missing' });
    return;
  }

  const userAccountDetails = await getUserByUsernameAndAccountProviderAndCredential({
    userName: data.email.toString(),
    accountProvider: 'token',
    fetchRoles: true,
    fetchAccounts: true,
  });

  if (userAccountDetails.errors || userAccountDetails.data.user_auths.length == 0) {
    console.log(userAccountDetails.errors);
    res.status(401).json({ message: 'unable to find user or secret' });
    return;
  }

  let userAccount: any;

  for (const ua of userAccountDetails.data.user_auths) {
    const validatedPassword = await validateHashedPassword(data.secret, ua.credential);
    if (validatedPassword) {
      userAccount = ua;
      break;
    }
  }

  if (!userAccount) {
    res.status(401).json({ message: 'unable to validate user or secret' });
    return;
  }

  if (userAccount.user.status != 'active' || userAccount.status != 'active') {
    console.log('user account is suspended', userAccount);
    res.status(401).json({ message: 'user is not active' });
    return;
  }

  //update last accessed
  const userAccountAccessUpdated = await updateUserAccountAccessed(userAccount.id, userAccount.tenant_id);

  if (userAccountAccessUpdated.errors) {
    console.log('unable to update userAccountAccessUpdated', userAccountAccessUpdated.errors);
  }

  const claims = {
    name: userAccount.user?.display_name,
    email: userAccount.user?.username,
    sub: userAccount.user.id,
    given_name: userAccount.user?.display_name,
  };
  const expirationDurationTimeSec = 60 * 60;
  const currentTimeSec = Math.floor(new Date().getTime() / 1000);

  const accountIds: string[] = [];
  const readonlyAccountIds: string[] = [];
  const namespacedAccountIds: string[] = [];
  const namespacedReadOnlyAccountIds: string[] = [];
  const roles: string[] = [];

  for (const ur of userAccount.user.user_roles) {
    if (ur.entity_type && ur.entity_type == 'tenant' && ur.entity_id == userAccount.tenant_id) {
      roles.push(ur.role);
    } else if (ur.entity_type && ur.entity_type == 'account') {
      if (ur.role == 'account_admin_readonly') {
        readonlyAccountIds.push(ur.entity_id);
      } else if (ur.role == 'account_admin') {
        accountIds.push(ur.entity_id);
      }
    }
  }

  const jwt = await encodeHasuraJWT(
    {
      id: userAccount.user?.id,
      roles: roles,
      tenant: { id: userAccount.tenant_id },
      accountIds: accountIds,
      readOnlyAccountIds: readonlyAccountIds,
      namespacedAccountIds: namespacedAccountIds,
      namespacedReadOnlyAccountIds: namespacedReadOnlyAccountIds,
    },
    claims,
    currentTimeSec + expirationDurationTimeSec,
    currentTimeSec
  );
  const encryptedJwt = await encrypt(jwt);
  try {
    res.status(200).json({ token: encryptedJwt, expiry: expirationDurationTimeSec });
  } catch (error: any) {
    res.status(error.status || 500).json({
      code: error.code,
      error: error.message,
    });
  }
}
