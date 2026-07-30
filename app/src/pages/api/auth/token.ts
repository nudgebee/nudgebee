import type { NextApiRequest, NextApiResponse } from 'next';
import { createHash } from 'crypto';
import { validateHashedPassword, encodeSessionJWT, encrypt } from '@lib/internal';
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

  try {
    const userAccountDetails = await getUserByUsernameAndAccountProviderAndCredential({
      userName: data.email.toString(),
      accountProvider: 'token',
      fetchRoles: true,
      fetchAccounts: true,
    });

    if (userAccountDetails.errors || userAccountDetails.data.user_auths.length == 0) {
      console.error(userAccountDetails.errors);
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

    if (!userAccount || !userAccount.user) {
      res.status(401).json({ message: 'unable to validate user or secret' });
      return;
    }

    if (userAccount.user.status != 'active' || userAccount.status != 'active') {
      console.warn('user account is suspended', userAccount.id);
      res.status(401).json({ message: 'user is not active' });
      return;
    }

    // Lazily backfill token_sha256 for tokens created before the column existed: we hold the
    // plaintext secret here, so compute the same lowercase-hex sha256 the gateway resolves by.
    // Piggybacks on the accessed-update; the handler COALESCEs, so it only writes when NULL.
    const tokenSha256 = createHash('sha256').update(data.secret).digest('hex');

    //update last accessed
    const userAccountAccessUpdated = await updateUserAccountAccessed(userAccount.id, userAccount.tenant_id, tokenSha256);

    if (userAccountAccessUpdated.errors) {
      console.error('unable to update userAccountAccessUpdated', userAccountAccessUpdated.errors);
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

    const jwt = await encodeSessionJWT(
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
    res.status(200).json({ token: encryptedJwt, expiry: expirationDurationTimeSec });
  } catch (error: any) {
    console.error('Token endpoint error:', error);
    res.status(500).json({
      error: 'Internal server error',
    });
  }
}
