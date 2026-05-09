import NextAuth, { type NextAuthOptions, type Account, type Session } from 'next-auth';
import type { AdapterUser, AdapterSession, VerificationToken, AdapterAccount } from 'next-auth/adapters';
import GoogleProvider from 'next-auth/providers/google';
import EmailProvider from 'next-auth/providers/email';
import OktaProvider from 'next-auth/providers/okta';
import Credentials from 'next-auth/providers/credentials';
import OneLoginProvider from 'next-auth/providers/onelogin';
import AzureADProvider from 'next-auth/providers/azure-ad';
import AzureADB2CProvider from 'next-auth/providers/azure-ad-b2c';
import Auth0Provider from 'next-auth/providers/auth0';
import { Client } from 'ldapts';
import axios from 'axios';

import { encodeHasuraJWT, decodeHasuraJWT } from '@lib/internal';
import {
  updateUserAccountAccessed,
  getUserById,
  getUserByUsername,
  getUserByAccountIdAndAccountProvider,
  getUserByUsernameAndAccountProviderAndCredential,
  createUserAuthAccount,
  listUserTenantRoles,
  deleteUserAuth,
  updateUserStatus,
  getAccountByTenant,
  onboardUser,
  updateUserAccountAccessedByUsername,
  updateTenantUser,
  getTenantAttributes,
  upsertTenantAttributes,
  getUserSuperAdminRole,
  getTenantIdByName,
} from '@lib/UserService';
import { findTenantByDomain } from '@lib/samlUserAdapter';
import { getLicenseDetails, type LicenseDetails as NudgebeeLicenseDetails } from '@lib/license';

import { decodeJwt } from 'jose';
import _ from 'lodash';
import type { NextApiRequest, NextApiResponse } from 'next';

export interface NudgebeeUser extends AdapterUser {
  roles: string[];
  tenant: any;
  userAccountId?: string;
  status: string;
  accountIds?: string[];
  readOnlyAccountIds?: string[];
  namespacedAccountIds?: string[];
  namespacedReadOnlyAccountIds?: string[];
  k8sNamespaces?: any;
  hasMultipleTenantAccess?: boolean;
}

export interface NudgebeeSession extends Session {
  roles?: string[];
  tenant?: { name?: string };
  error?: string;
  onPrem?: boolean;
  accountIds?: string[];
  readOnlyAccountIds?: string[];
  namespacedAccountIds?: string[];
  namespacedReadOnlyAccountIds?: string[];
  k8sNamespaces?: any;
  appVersion?: string;
  pendoEnable: string;
  hasMultipleTenantAccess?: boolean;
  isSuperAdmin?: boolean;
  isSuperAdminReadonly?: boolean;
}

const _userAccessUpdateCache = new Map<string, number>();
const USER_ACCESS_THROTTLE_MS = 5 * 60 * 1000;

function cleanupUserAccessCache() {
  if (_userAccessUpdateCache.size < 1000) return;
  const now = Date.now();
  for (const [key, timestamp] of _userAccessUpdateCache) {
    if (now - timestamp > USER_ACCESS_THROTTLE_MS * 2) {
      _userAccessUpdateCache.delete(key);
    }
  }
}

function adapterUserUpdateDataOnUserRoles(
  user_roles: any[],
  roles: string[],
  accountIds: string[],
  readonlyAccountIds: string[],
  namespacedAccountIds: string[],
  namespacedReadOnlyAccountIds: string[],
  k8sNamespaces: any,
  tenantId?: string
) {
  user_roles?.forEach((r: any) => {
    if (r.entity_type && r.entity_type == 'tenant') {
      if (!tenantId || r.entity_id === tenantId) {
        roles.push(r.role);
      }
    } else if (r.entity_type && r.entity_type == 'account' && r.role == 'account_admin_readonly') {
      roles.push(r.role);
      readonlyAccountIds.push(r.entity_id);
    } else if (r.entity_type && r.entity_type == 'account' && r.role == 'account_admin') {
      roles.push(r.role);
      accountIds.push(r.entity_id);
    } else if (r.entity_type && r.entity_type == 'k8s_namespace' && r.role == 'k8s_namespace_admin') {
      roles.push(r.role);
      const entity = r.entity_id?.split(':');
      if (!k8sNamespaces[entity[0]]) {
        k8sNamespaces[entity[0]] = [entity[1]];
      } else {
        k8sNamespaces[entity[0]].push(entity[1]);
      }
      namespacedAccountIds.push(entity[0]);
    } else if (r.entity_type && r.entity_type == 'k8s_namespace' && r.role == 'k8s_namespace_admin_readonly') {
      roles.push(r.role);
      const entity = r.entity_id?.split(':');
      if (!k8sNamespaces[entity[0]]) {
        k8sNamespaces[entity[0]] = [entity[1]];
      } else {
        k8sNamespaces[entity[0]].push(entity[1]);
      }
      namespacedReadOnlyAccountIds.push(entity[0]);
    }
  });
}

async function adapterUser(user: any): Promise<NudgebeeUser> {
  let tenant: any = {};
  let roles: string[] = [];
  let accountIds: string[] = [];
  let readonlyAccountIds: string[] = [];
  let namespacedAccountIds: string[] = [];
  let namespacedReadOnlyAccountIds: string[] = [];
  const k8sNamespaces: any = {};
  // Select tenant based on user preferences or defaults
  if (user.tenants?.length > 0) {
    const defaultTenant = user.tenants.find((t: any) => t.is_default);
    if (defaultTenant) {
      tenant = defaultTenant;
    } else {
      tenant = user.tenants[0];
    }
  }
  //filter roles based on tenant
  user.user_roles = user.user_roles ?? [];

  adapterUserUpdateDataOnUserRoles(
    user.user_roles,
    roles,
    accountIds,
    readonlyAccountIds,
    namespacedAccountIds,
    namespacedReadOnlyAccountIds,
    k8sNamespaces,
    tenant.id
  );

  const groups = user.groups ?? [];
  for (const group of groups) {
    const groupRoles = group.user_group.group_roles ?? [];
    adapterUserUpdateDataOnUserRoles(
      groupRoles,
      roles,
      accountIds,
      readonlyAccountIds,
      namespacedAccountIds,
      namespacedReadOnlyAccountIds,
      k8sNamespaces,
      tenant.id
    );
  }

  roles = _.uniq(roles);
  accountIds = _.uniq(accountIds);
  readonlyAccountIds = _.uniq(readonlyAccountIds);
  namespacedAccountIds = _.uniq(namespacedAccountIds);
  namespacedReadOnlyAccountIds = _.uniq(namespacedReadOnlyAccountIds);

  if (accountIds.length > 0 || readonlyAccountIds.length > 0) {
    // get accountIds from given tenant
    const resp = await getAccountByTenant(tenant.id);
    if (resp.data) {
      const tenantAccounts = resp.data?.cloud_accounts?.map((a: any) => a.id);
      accountIds = accountIds.filter((a) => tenantAccounts.includes(a));
      readonlyAccountIds = readonlyAccountIds.filter((a) => tenantAccounts.includes(a));
      namespacedAccountIds = namespacedAccountIds.filter((a) => tenantAccounts.includes(a));
      namespacedReadOnlyAccountIds = namespacedReadOnlyAccountIds.filter((a) => tenantAccounts.includes(a));
    } else {
      console.log('unable to get accounts for tenant', tenant.id, resp);
      accountIds = [];
      readonlyAccountIds = [];
      namespacedAccountIds = [];
      namespacedReadOnlyAccountIds = [];
    }
  }

  let authAccountId;
  if (user.user_auths?.length > 0) {
    authAccountId = user.user_auths?.[0].id;
  }
  return {
    id: user.id,
    emailVerified: user.status != 'suspended' ? new Date(Date.parse(user.created_at)) : null,
    email: user.username,
    name: user.display_name,
    image: null,
    roles: roles,
    tenant: tenant,
    userAccountId: authAccountId,
    status: user.status,
    accountIds: accountIds,
    readOnlyAccountIds: readonlyAccountIds,
    namespacedAccountIds: namespacedAccountIds,
    namespacedReadOnlyAccountIds: namespacedReadOnlyAccountIds,
    k8sNamespaces: k8sNamespaces,
    hasMultipleTenantAccess: user?.tenants?.length > 1,
  };
}

export function GQLAdapter() {
  async function getUser(id: string) {
    const response = await getUserById({ id: id, fetchRoles: true, fetchGroups: true });
    if (response.data && response.data.users && response.data.users.length > 0) {
      const user = response.data.users[0];
      return await adapterUser(user);
    }
    return null;
  }

  async function getUserByEmail(email: string) {
    const response = await getUserByUsername({ username: email, fetchRoles: true, fetchAccounts: true, fetchGroups: true });
    if (response.data && response.data.users.length > 0) {
      const user = response.data.users[0];
      if (user.status == 'suspended') {
        return null;
      }
      return await adapterUser(user);
    }
    if (response.errors) {
      console.log('getUserByEmail Error', JSON.stringify(response));
    }
    return null;
  }

  async function getUserByAccount(providerAccountId: Pick<Account, 'provider' | 'providerAccountId'>) {
    const response = await getUserByAccountIdAndAccountProvider({
      accountId: providerAccountId.providerAccountId,
      accountProvider: providerAccountId.provider?.replaceAll('-', '_'),
      fetchRoles: true,
      fetchAccounts: true,
      fetchGroups: true,
    });
    if (response.data && response.data.user_auths.length > 0) {
      const accountsData = response.data.user_auths[0];
      if (accountsData.user.status === 'suspended') {
        throw new Error('User Account is suspended');
      } else if (accountsData.user.status === 'inactive') {
        //first time login flow
        console.log(`getUserByAccount: user ${accountsData.user.id} is inactive, first time login`);
        return null;
      }
      const transformedUser = await adapterUser(accountsData.user);
      transformedUser.userAccountId = accountsData.user.user_auths[0].id;
      return transformedUser;
    }

    return null;
  }

  async function linkAccount(account: AdapterAccount) {
    const response = await createUserAuthAccount({
      user: account.userId || '',
      provider: account.provider?.replaceAll('-', '_') || '',
      provider_type: account.type || '',
      account_id: account.providerAccountId || '',
      name: account.provider?.replaceAll('-', '_') || '',
      status: 'active',
      accessed_at: new Date().toISOString(),
    });
    if (response.errors) {
      console.log('unable to link account', response.errors);
      throw Error('Unable to Link User');
    }
    account.id = response.data.id;
    if (response.data.userByUser.status === 'inactive') {
      await updateUserStatus(response.data.userByUser.id, 'active');
    }
    return account;
  }

  async function updateUser(user: Partial<AdapterUser>) {
    if (!user.id) {
      throw Error('Unable to find User');
    }
    const adapterUser = await getUser(user.id);
    if (!adapterUser) {
      throw Error('Unable to find User');
    }
    return adapterUser;
  }

  async function createSession(session: { sessionToken: string; userId: string; expires: Date }) {
    return {
      id: '',
      sessionToken: session.sessionToken,
      expires: session.expires,
      userId: session.userId,
    };
  }

  async function getSessionAndUser(sessionToken: string) {
    console.log('getSessionAndUser', sessionToken);
    return null;
  }

  async function updateSession(session: Partial<AdapterSession> & Pick<AdapterSession, 'sessionToken'>) {
    console.log('updateSession', session);
    return null;
  }

  async function deleteSession(sessionToken: string) {
    console.log('deleteSession', sessionToken);
    return null;
  }

  async function createVerificationToken(verificationToken: VerificationToken) {
    console.log('createVerificationToken called for:', verificationToken.identifier);
    const user = await getUserByUsername({ username: verificationToken.identifier, fetchRoles: false, fetchAccounts: true, fetchGroups: true });
    let userAccount = null;
    if (user.data && user.data.users.length > 0) {
      userAccount = user.data.users[0];
      if (userAccount.status === 'suspended') {
        return verificationToken;
      }
    } else {
      return verificationToken;
    }

    console.log(
      'createVerificationToken: user found, id:',
      userAccount.id,
      'email auths:',
      userAccount.user_auths.filter((f: any) => f.provider_type === 'email' && f.provider === 'email').length
    );

    if (userAccount.user_auths.length > 0) {
      const userAuth = userAccount.user_auths.filter((f: any) => f.provider_type === 'email' && f.provider === 'email')[0];
      if (userAuth) {
        //delete existing auth
        console.log('createVerificationToken: deleting old auth entry:', userAuth.id);
        await deleteUserAuth(userAuth.id);
      }
    }

    const response = await createUserAuthAccount({
      user: userAccount.id,
      provider: 'email',
      provider_type: 'email',
      account_id: verificationToken.identifier,
      name: 'email',
      status: 'active',
      accessed_at: new Date().toISOString(),
      expires_at: verificationToken.expires.toISOString(),
      credential: verificationToken.token,
    });

    if (response.errors) {
      console.log('unable to store tokens', response.errors);
      throw Error('Unable to Generate Verification Token');
    }
    console.log('createVerificationToken: auth entry created successfully, id:', response.data?.id);
    return verificationToken;
  }

  async function useVerificationToken(params: { identifier: string; token: string }) {
    console.log('useVerificationToken called for identifier:', params.identifier);
    const credResp = await getUserByUsernameAndAccountProviderAndCredential({
      userName: params.identifier,
      accountProvider: 'email',
      fetchAccounts: true,
    });
    if (!credResp?.data?.user_auths || credResp.data.user_auths.length === 0) {
      console.log('useVerificationToken: no user_auths found for', params.identifier, 'raw response:', JSON.stringify(credResp));
      return null;
    }

    const authEntry = credResp.data.user_auths[0];
    const userAccount = authEntry.user;
    if (!userAccount || userAccount.status === 'suspended') {
      console.log('useVerificationToken: user not found or suspended for', params.identifier, 'status:', userAccount?.status);
      return null;
    }

    if (authEntry.credential === params.token) {
      //delete existing auth (remove one-time token for security)
      await deleteUserAuth(authEntry.id);

      // Re-create a persistent auth record without credential for last-login tracking.
      // This record will be cleaned up when the next magic link is requested (createVerificationToken).
      await createUserAuthAccount({
        user: userAccount.id,
        provider: 'email',
        provider_type: 'email',
        account_id: params.identifier,
        name: 'email',
        status: 'active',
        accessed_at: new Date().toISOString(),
      });

      //mark user as active and update accessed_at with tenant_id
      await updateUserAccountAccessedByUsername(params.identifier, userAccount.tenants[0].id);

      if (userAccount.status === 'inactive') {
        await updateUserStatus(userAccount.id, 'active');
      }

      return {
        identifier: params.identifier,
        token: params.token,
        expires: new Date(authEntry.expires_at),
      };
    }

    console.log(
      'useVerificationToken: credential mismatch for',
      params.identifier,
      'hasCredential:',
      !!authEntry.credential,
      'hasExpiry:',
      !!authEntry.expires_at
    );
    return null;
  }

  return {
    getUser: getUser,
    getUserByEmail: getUserByEmail,
    /** Using the provider id and the id of the user for a specific account, get the user. */
    getUserByAccount: getUserByAccount,
    updateUser: updateUser,
    linkAccount: linkAccount,
    /** Creates a session for the user and returns it. */
    createSession: createSession,
    getSessionAndUser: getSessionAndUser,
    updateSession: updateSession,
    /**
     * Deletes a session from the database.
     * It is preferred that this method also returns the session
     * that is being deleted for logging purposes.
     */
    deleteSession: deleteSession,
    createVerificationToken: createVerificationToken,
    /**
     * Return verification token from the database
     * and delete it so it cannot be used again.
     */
    useVerificationToken: useVerificationToken,
  };
}

const providers = [];
if (process.env.GOOGLE_CLIENT_ID) {
  providers.push(
    GoogleProvider({
      allowDangerousEmailAccountLinking: true,
      clientId: process.env.GOOGLE_CLIENT_ID ?? '',
      clientSecret: process.env.GOOGLE_CLIENT_SECRET ?? '',
      authorization: {
        params: {
          prompt: 'consent',
          access_type: 'offline',
          response_type: 'code',
        },
      },
    })
  );
}

if (process.env.OKTA_CLIENT_ID) {
  providers.push(
    OktaProvider({
      allowDangerousEmailAccountLinking: true,
      clientId: process.env.OKTA_CLIENT_ID,
      clientSecret: process.env.OKTA_CLIENT_SECRET ?? '',
      issuer: process.env.OKTA_ISSUER,
    })
  );
}

if (process.env.ONELOGIN_CLIENT_ID) {
  providers.push(
    OneLoginProvider({
      allowDangerousEmailAccountLinking: true,
      clientId: process.env.ONELOGIN_CLIENT_ID,
      clientSecret: process.env.ONELOGIN_CLIENT_SECRET,
      issuer: process.env.ONELOGIN_ISSUER,
    })
  );
}

if (process.env.AZURE_AD_CLIENT_ID) {
  providers.push(
    AzureADProvider({
      allowDangerousEmailAccountLinking: true,
      clientId: process.env.AZURE_AD_CLIENT_ID,
      clientSecret: process.env.AZURE_AD_CLIENT_SECRET ?? '',
      tenantId: process.env.AZURE_AD_TENANT_ID ?? '',
    })
  );
}

if (process.env.AZURE_AD_B2C_CLIENT_ID) {
  providers.push(
    AzureADB2CProvider({
      allowDangerousEmailAccountLinking: true,
      tenantId: process.env.AZURE_AD_B2C_TENANT_NAME,
      clientId: process.env.AZURE_AD_B2C_CLIENT_ID,
      clientSecret: process.env.AZURE_AD_B2C_CLIENT_SECRET ?? '',
      primaryUserFlow: process.env.AZURE_AD_B2C_PRIMARY_USER_FLOW,
      authorization: { params: { scope: 'offline_access openid' } },
    })
  );
}

if (process.env.AUTH0_CLIENT_ID) {
  providers.push(
    Auth0Provider({
      allowDangerousEmailAccountLinking: true,
      clientId: process.env.AUTH0_CLIENT_ID,
      clientSecret: process.env.AUTH0_CLIENT_SECRET ?? '',
      issuer: process.env.AUTH0_ISSUER,
    })
  );
}

if (process.env.EMAIL_SERVER_HOST && (process.env.NEXTAUTH_MAGICLINK_CREDS_ENABLED ?? 'true') == 'true') {
  providers.push(
    EmailProvider({
      maxAge: 10 * 60,
      server: {
        host: process.env.EMAIL_SERVER_HOST,
        port: parseInt(process.env.EMAIL_SERVER_PORT ?? '465'),
        auth: {
          user: process.env.EMAIL_SERVER_USER,
          pass: process.env.EMAIL_SERVER_PASSWORD,
        },
      },
      from: process.env.EMAIL_FROM ?? process.env.EMAIL_SERVER_USER,
      sendVerificationRequest: async ({ identifier: email, url }) => {
        const notificationServiceUrl = process.env.NOTIFICATION_SERVICE_URL ?? 'http://notifications:80';
        const resp = await axios.post(`${notificationServiceUrl}/api/send_email`, {
          recipients: email,
          subject: 'Sign in to your account',
          template: 'magic_link',
          template_params: { magic_link_url: url },
        });
        if (resp.status !== 200) {
          throw new Error(`Failed to send magic link email: ${resp.status}`);
        }
      },
    })
  );
}

/**
 * Validates if a user can log in when no tenant exists in on-prem deployment
 * In on-prem deployments with no tenant, only the email from license should be allowed
 */
async function validateOnPremNoTenantAccess(license: NudgebeeLicenseDetails, userEmail: string): Promise<boolean> {
  if (license.licenseType !== 'on-prem') {
    return true;
  }

  if (!license.tenantId) {
    if (license.email && userEmail.toLowerCase() === license.email.toLowerCase()) {
      console.log(`No tenant exists. Allowing login for license email: ${userEmail}`);
      return true;
    }
    console.log(`No tenant exists. Denying login for non-license email: ${userEmail}`);
    return false;
  }

  return true;
}

/**
 * Ensures the admin's email domain is registered as an allowed_domain for the tenant.
 * This enables other users from the same domain to log in via LDAP/OAuth/SAML.
 */
async function ensureAllowedDomainsSet(email: string, tenantId?: string) {
  if (!tenantId) return;
  const domain = email?.split('@')?.[1];
  if (!domain) return;

  try {
    const { tenantId: existingDomainTenant } = await findTenantByDomain(domain);
    if (!existingDomainTenant) {
      await upsertTenantAttributes([{ name: 'allowed_domains', value: JSON.stringify([domain]) }], { 'x-hasura-user-tenant-id': tenantId });
      await getTenantAttributes(true); // refresh cache
      console.log(`Set allowed_domains for tenant ${tenantId}: [${domain}]`);
    }
  } catch (e) {
    console.log('Failed to set allowed_domains for tenant', e);
  }
}

async function getOrCreateNewOnPremAdminUser(license: NudgebeeLicenseDetails, email: string) {
  const canAccess = await validateOnPremNoTenantAccess(license, email);
  if (!canAccess) {
    throw Error('NO_TENANT_ACCESS');
  }

  const nbUser = await getUserByUsername({ username: email, fetchRoles: true, fetchAccounts: true, fetchGroups: true });
  if (nbUser.data && nbUser.data.users.length > 0) {
    // Ensure allowed_domains is set for existing admin users (backfill for tenants created before this fix)
    await ensureAllowedDomainsSet(email, license.tenantId);
    return adapterUser(nbUser.data.users[0]);
  }
  console.log('user not found, checking licenseDetails for first time onboarding', email);
  if (license.licenseType === 'on-prem' && license.tenantId) {
    const response = await onboardUser({
      username: email,
      display_name: 'admin',
      status: 'active',
      tenant_id: license.tenantId,
      role: 'tenant_admin',
    });
    if (response.errors) {
      console.log('unable to create user', response.errors);
      throw Error('Unable to Create User');
    }

    // Set allowed_domains for the tenant so other users from the same domain can log in
    await ensureAllowedDomainsSet(email, license.tenantId);

    const nbUser = await getUserByUsername({ username: email, fetchRoles: true, fetchAccounts: true, fetchGroups: true });
    if (nbUser.data && nbUser.data.users.length > 0) {
      return await adapterUser(nbUser.data.users[0]);
    }
  }

  throw Error('Invalid Username');
}

async function getOrCreateNewOnPremUser(
  license: NudgebeeLicenseDetails,
  email: string,
  provider: string,
  providerId: string,
  name: string,
  groups: string[]
) {
  const canAccess = await validateOnPremNoTenantAccess(license, email);
  if (!canAccess) {
    throw Error('NO_TENANT_ACCESS');
  }

  const nbUser = await getUserByUsername({ username: email, fetchRoles: true, fetchAccounts: true, fetchGroups: true });
  if (nbUser.errors) {
    console.log('unable to get user', JSON.stringify(nbUser.errors));
    throw Error('Unable to Get Users');
  }
  let existingUserId = null;
  if (nbUser?.data?.users?.length > 0) {
    existingUserId = nbUser.data.users[0].id;
    const user = nbUser.data.users[0];

    // Check if user has any tenants
    if (!user.tenants || user.tenants.length === 0) {
      console.log('user found but has no tenant assigned', user.id);
      throw Error('NO_TENANT_ACCESS');
    }

    for (const t of user.tenants || []) {
      if (t.id === license.tenantId) {
        console.log('user found and part of license tenant', user.id, t.id);
        await ensureAllowedDomainsSet(email, license.tenantId);
        return await adapterUser(nbUser.data.users[0]);
      }
    }
    console.log('user found but not part of license tenant');
  }
  console.log('checking licenseDetails for first time onboarding', email);
  if (license.licenseType === 'on-prem' && license.tenantId) {
    if (license.email && email.toLowerCase() === license.email.toLowerCase()) {
      await ensureAllowedDomainsSet(email, license.tenantId);
    }

    // Validate email domain matches tenant's allowed_domains
    const domainOfUser = email?.split('@')?.[1] || '';
    if (!domainOfUser) {
      console.log('Cannot extract domain from email', email);
      throw Error('Invalid Email Format');
    }

    const { tenantId: domainTenantId } = await findTenantByDomain(domainOfUser);
    if (!domainTenantId || domainTenantId !== license.tenantId) {
      console.log(`Domain ${domainOfUser} is not authorized for tenant ${license.tenantId}. Domain tenant: ${domainTenantId}`);
      throw Error('NO_TENANT_ACCESS');
    }

    const userResponse = await onboardUser({
      username: email,
      display_name: name ?? email,
      status: 'active',
      tenant_id: license.tenantId,
      groups: groups,
      existingUserId: existingUserId,
    });
    if (userResponse.errors) {
      console.log('unable to create user', userResponse.errors);
      throw Error('Unable to Create User');
    }

    //link account
    const authResponse = await createUserAuthAccount({
      user: userResponse?.data?.id,
      provider: provider,
      provider_type: 'credentials',
      account_id: providerId ?? email,
      name: provider,
      status: 'active',
      accessed_at: new Date().toISOString(),
      expires_at: new Date().toISOString(),
      credential: '',
    });
    console.log('linking users account to response', authResponse, provider, providerId);

    const nbUser = await getUserByUsername({ username: email, fetchRoles: true, fetchAccounts: true, fetchGroups: true });
    if (nbUser?.data?.users?.length > 0) {
      return await adapterUser(nbUser.data.users[0]);
    }
  }

  throw Error('Invalid Username');
}

if (process.env.NEXTAUTH_DUMMY_CREDS_ENABLED == 'true') {
  providers.push(
    Credentials({
      credentials: {
        username: { label: 'Username', type: 'text', placeholder: 'Username' },
        password: { label: 'Password', type: 'password' },
      },
      async authorize(credentials, _req) {
        const emailPattern = new RegExp('[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+.[a-zA-Z]{2,4}$');
        if (!credentials?.username) {
          throw Error('Invalid Username');
        }
        if (!emailPattern.test(credentials?.username as string)) {
          throw Error('Invalid Email format');
        }
        if (!credentials?.username) {
          throw Error('Invalid Username');
        }
        const normalizedUsername = credentials.username.toLowerCase();
        const license = await getLicenseDetails();
        if (license.licenseType === 'on-prem' && license.email?.toLowerCase() !== normalizedUsername) {
          throw Error('Invalid Email');
        }
        if (credentials?.password !== process.env.NEXTAUTH_DUMMY_CREDS_PASSWORD) {
          throw Error('Invalid Passsword');
        }
        return await getOrCreateNewOnPremAdminUser(license, normalizedUsername);
      },
    })
  );
}

if (process.env.NEXTAUTH_LDAP_URI) {
  providers.push(
    Credentials({
      id: 'ldap',
      name: 'LDAP',
      credentials: {
        username: { label: 'Username', type: 'text', placeholder: 'LDAP Username' },
        password: { label: 'Password', type: 'password' },
      },
      async authorize(credentials, _req) {
        const license = await getLicenseDetails();
        if (license.licenseType !== 'on-prem') {
          throw Error('LDAP Auth is not enabled for on-prem license type');
        }

        const ldapUri = process.env.NEXTAUTH_LDAP_URI ?? 'ldap://localhost:389';

        let client = new Client({
          url: ldapUri,
        });
        if (!credentials?.username) {
          throw Error('Invalid Username');
        }
        if (!credentials?.password) {
          throw Error('Invalid Password');
        }
        try {
          const searchFilter = process.env.NEXTAUTH_LDAP_LOGIN_FILTER ?? '(uid=%s)';
          const searchDn = searchFilter.replace('%s', credentials.username);
          console.log('searchDn', searchDn);
          await client.bind(searchDn, credentials.password);
        } catch (e) {
          console.log('LDAP Auth Error', e);
          throw Error('Invalid Credentials');
        } finally {
          await client.unbind();
        }

        client = new Client({
          url: ldapUri,
        });
        const ldapAttributeEmail = process.env.NEXTAUTH_LDAP_ATTRIBUTE_EMAIL ?? 'mail';
        const ldapAttributeGroup = process.env.NEXTAUTH_LDAP_ATTRIBUTE_GROUP ?? 'memberOf';
        const ldapAttributeName = process.env.NEXTAUTH_LDAP_ATTRIBUTE_NAME ?? 'name';
        const ldapAttributeFirstName = process.env.NEXTAUTH_LDAP_ATTRIBUTE_FIRSTNAME ?? 'gn';
        const ldapAttributeLastName = process.env.NEXTAUTH_LDAP_ATTRIBUTE_LASTNAME ?? 'sn';

        try {
          if (process.env.NEXTAUTH_LDAP_BIND_DN && process.env.NEXTAUTH_LDAP_BIND_PASSWORD) {
            await client.bind(process.env.NEXTAUTH_LDAP_BIND_DN, process.env.NEXTAUTH_LDAP_BIND_PASSWORD);
          } else {
            throw Error('Unable to bind to search user');
          }
          let searchFilter = process.env.NEXTAUTH_LDAP_SEARCH_FILTER ?? '(uid=%s)';
          searchFilter = searchFilter.replace('%s', credentials.username);
          const searchOptions = {
            filter: searchFilter,
            attributes: [ldapAttributeEmail, ldapAttributeGroup, ldapAttributeName, ldapAttributeFirstName, ldapAttributeLastName],
          };

          console.log('searchOptions', searchOptions, process.env.NEXTAUTH_LDAP_SEARCH_DN);

          const { searchEntries } = await client.search(process.env.NEXTAUTH_LDAP_SEARCH_DN ?? '', searchOptions);
          if (searchEntries.length > 0) {
            // get email
            let email = searchEntries[0][ldapAttributeEmail];
            if (!email) {
              throw Error('Unable to find email');
            }
            if (Array.isArray(email)) {
              email = email[0];
            }

            // get name
            let name: any = searchEntries[0][ldapAttributeName];
            if (name && Array.isArray(name)) {
              if (name.length > 0) {
                name = name[0]?.toString();
              } else {
                name = null;
              }
            }

            if (!name) {
              let firstName = searchEntries[0][ldapAttributeFirstName];
              if (firstName && Array.isArray(firstName) && firstName.length > 0) {
                firstName = firstName[0]?.toString();
              }

              let lastName = searchEntries[0][ldapAttributeLastName];
              if (lastName && Array.isArray(lastName) && lastName.length > 0) {
                lastName = lastName[0]?.toString();
              }
              if (firstName && lastName) {
                name = `${firstName} ${lastName}`;
              }
            }

            if (!name) {
              name = credentials.username;
            }

            const nameStr = name?.toString() || credentials.username;

            // get user groups
            const groupMappingstr = process.env.NEXTAUTH_LDAP_GROUP_MAPPING;
            let groupMapping: Record<string, string> = {};
            if (groupMappingstr) {
              groupMapping = JSON.parse(groupMappingstr);
            }

            let groups = searchEntries[0][ldapAttributeGroup];
            if (!groups) {
              groups = [];
            }
            if (!Array.isArray(groups)) {
              groups = [groups.toString()];
            } else {
              groups = groups.map((r) => r.toString());
            }
            console.log('userLdapDetails Groups', groups);
            const groupUpdated: string[] = groups
              .filter((r) => r != '' && groupMapping[r])
              .map((r) => {
                return groupMapping[r];
              });

            console.log('userLdapDetails', email, credentials.username, nameStr, groupUpdated, groupMapping);

            return await getOrCreateNewOnPremUser(license, email.toString().toLowerCase(), 'ldap', credentials.username, nameStr, groupUpdated ?? []);
          }
        } catch (e: any) {
          console.log('Unable to search attributes', e);
          // Re-throw NO_TENANT_ACCESS error specifically
          if (e?.message === 'NO_TENANT_ACCESS') {
            throw e;
          }
          throw Error('Unable to search user attributes');
        } finally {
          await client.unbind();
        }

        throw Error('Unable to search user attributes');
      },
    })
  );
}

if (process.env.TELEPORT_ENABLED == 'true') {
  providers.push(
    Credentials({
      id: 'teleport',
      name: 'Teleport',
      credentials: {
        username: { label: 'Username', type: 'text', placeholder: 'Teleport Username' },
        password: { label: 'Password', type: 'password' },
      },
      async authorize(_credentials, _req) {
        const teleportJwt = _req?.headers?.['teleport-jwt-assertion'];
        if (!teleportJwt) {
          throw Error('Unable to find teleport jwt assertion');
        }
        const jwtPayload = decodeJwt(teleportJwt);
        if (!jwtPayload) {
          throw Error('Unable to decode teleport jwt assertion');
        }

        console.log('teleport-header', teleportJwt);

        const traits = (jwtPayload['traits'] ?? {}) as Record<string, string[]>;

        const license = await getLicenseDetails();
        if (license.licenseType !== 'on-prem') {
          throw Error('Teleport Auth is not enabled for on-prem license type');
        }

        const userNameKey = process.env.TELEPORT_ATTRIBUTE_USERNAME ?? 'sub';

        const username = jwtPayload[userNameKey] ?? traits[userNameKey]?.[0];
        if (!username) {
          throw Error('Unable to search user attribute' + userNameKey);
        }

        const displayNameKey = process.env.TELEPORT_ATTRIBUTE_NAME ?? 'sub';

        let name = (jwtPayload[displayNameKey] ?? traits[displayNameKey]?.[0]) as string;
        if (!name) {
          name = username as string;
        }

        const groupsKey = process.env.TELEPORT_ATTRIBUTE_GROUPS ?? 'groups';

        let groups = (jwtPayload[groupsKey] ?? traits[groupsKey]) as string[];
        if (!groups) {
          groups = [];
        }
        return await getOrCreateNewOnPremUser(license, (username as string).toLowerCase(), 'teleport', username as string, name, groups);
      },
    })
  );
}

async function jwtUpdateTokenForCredentialToken(token: any, user: any, oauthAccount: any, _trigger: any, _session: any) {
  token.email = oauthAccount.providerAccountId?.toLowerCase();
  if (user.email) {
    token.email = user.email.toLowerCase();
  }
  token.email_verified = true;
  const nbUser = await getUserByUsername({ username: token.email ?? '', fetchRoles: true, fetchAccounts: true, fetchGroups: true });
  if (nbUser.data && nbUser.data.users.length > 0) {
    const finalUser = await adapterUser(nbUser.data.users[0]);
    token.id = finalUser.id;
    token.sub = finalUser.id;
    token.roles = finalUser.roles;
    token.tenant = finalUser.tenant;
    oauthAccount.expires_at = 60 * 60;
  }
}

async function jwtUpdateTokenForOAuth(token: any, user: any, oauthAccount: any, _trigger: any, _session: any) {
  token.accessToken = oauthAccount.access_token;
  token.idToken = oauthAccount.id_token;
  token.refreshToken = oauthAccount.refresh_token;
  token.expiresAt = oauthAccount.expires_at;
  token.email_verified = true;
  const nudgebeeUser = user as NudgebeeUser;
  token.roles = nudgebeeUser?.roles;
  token.tenant = token?.tenant ?? nudgebeeUser?.tenant;
}

async function jwtGenerateHasuraTokenAndUpdate(token: any) {
  const expirationDurationTimeSec = 60 * 60;
  const currentTimeSec = Math.floor(new Date().getTime() / 1000);
  const refreshThreshold = 5 * 60;

  if (token.hasuraIdToken) {
    try {
      const decodedToken = await decodeHasuraJWT(token.hasuraIdToken as string);
      if (decodedToken?.payload?.exp && decodedToken.payload.exp > currentTimeSec + refreshThreshold) {
        const hasuraClaims: any = decodedToken.payload['https://hasura.io/jwt/claims'];
        if (hasuraClaims) {
          const hasuraTenant = hasuraClaims['x-hasura-user-tenant-id'];
          if (hasuraTenant && hasuraTenant === token.tenant.id) {
            return token;
          }
        }
      }
    } catch (e) {
      console.log('unable to decode hasura/varify token, will regenerate new token ', e);
    }
  }

  const claims = {
    name: token?.name,
    email: token?.email,
    email_verified: token?.email_verified,
    sub: token?.sub,
  };

  token.hasuraIdToken = await encodeHasuraJWT(
    {
      id: token.id,
      roles: token.roles,
      tenant: token.tenant,
      accountIds: token.accountIds,
      readOnlyAccountIds: token.readOnlyAccountIds,
      namespacedAccountIds: token.namespacedAccountIds,
      namespacedReadOnlyAccountIds: token.namespacedReadOnlyAccountIds,
    },
    claims,
    currentTimeSec + expirationDurationTimeSec,
    currentTimeSec
  );

  return token;
}

async function jwtUpdateTokenOnUpdateTrigger(token: any, session: any, trigger: string | undefined) {
  console.log(
    'jwtUpdateTokenOnUpdateTrigger:',
    JSON.stringify({
      trigger,
      tenantName: session?.tenantName,
      tenantId: session?.tenantId,
      userEmail: session?.user?.email,
      tokenTenant: token?.tenant,
    })
  );
  if (trigger === 'update' && (session.tenantId || session.tenantName)) {
    const userEmail = session.user?.email ?? '';
    let tenantId = session.tenantId;
    if (!tenantId && session.tenantName) {
      const isSuperAdmin = !!(token.isSuperAdmin || token.isSuperAdminReadonly);
      tenantId = await getTenantIdByName(session.tenantName, userEmail, isSuperAdmin);
      console.log('getTenantIdByName result:', JSON.stringify({ tenantName: session.tenantName, userEmail, isSuperAdmin, tenantId }));
    }
    if (!tenantId) {
      console.log('jwtUpdateTokenOnUpdateTrigger: tenantId is null, aborting switch');
      return;
    }
    const currentTenantId = typeof token.tenant === 'object' ? token.tenant?.id : token.tenant;
    const response = await listUserTenantRoles(userEmail, tenantId);
    const tenantName = response.tenantName;
    console.log(
      'jwtUpdateTokenOnUpdateTrigger: tenant comparison:',
      JSON.stringify({ newTenantId: tenantId, currentTenantId, newTenantName: tenantName, rolesCount: response?.data?.length })
    );

    if (tenantId && tenantId !== currentTenantId) {
      await updateTenantUser(tenantId, session.user.email);
      if (response?.data?.length > 0) {
        token.tenant = { id: tenantId, name: tenantName };
        const roles = [];
        const accountIds = [];
        const readOnlyAccountIds = [];
        const namespacedAccountIds = [];
        const namespacedReadOnlyAccountIds = [];
        const k8sNamespaces: any = {};
        for (const role of response.data) {
          if (role.entity_type === 'tenant') {
            roles.push(role.role);
          } else if (role.entity_type === 'account' && role.role === 'account_admin') {
            accountIds.push(role.entity_id);
            roles.push(role.role);
          } else if (role.entity_type === 'account' && role.role === 'account_admin_readonly') {
            readOnlyAccountIds.push(role.entity_id);
            roles.push(role.role);
          } else if (role.entity_type === 'k8s_namespace' && role.role === 'k8s_namespace_admin') {
            const entity = role.entity_id?.split(':');
            if (!k8sNamespaces[entity[0]]) {
              k8sNamespaces[entity[0]] = [entity[1]];
            } else {
              k8sNamespaces[entity[0]].push(entity[1]);
            }
            namespacedAccountIds.push(entity[0]);
            roles.push(role.role);
          } else if (role.entity_type === 'k8s_namespace' && role.role === 'k8s_namespace_admin_readonly') {
            const entity = role.entity_id?.split(':');
            if (!k8sNamespaces[entity[0]]) {
              k8sNamespaces[entity[0]] = [entity[1]];
            } else {
              k8sNamespaces[entity[0]].push(entity[1]);
            }
            namespacedReadOnlyAccountIds.push(entity[0]);
            roles.push(role.role);
          }
        }
        token.roles = _.uniq(roles);
        token.accountIds = _.uniq(accountIds);
        token.readOnlyAccountIds = _.uniq(readOnlyAccountIds);
        token.namespacedAccountIds = _.uniq(namespacedAccountIds);
        token.namespacedReadOnlyAccountIds = _.uniq(namespacedReadOnlyAccountIds);
        token.k8sNamespaces = k8sNamespaces;
      } else if (token.isSuperAdmin || token.isSuperAdminReadonly) {
        // Super admin with no direct access to this tenant → readonly
        token.tenant = { id: tenantId, name: tenantName };
        token.roles = ['tenant_admin_readonly'];
        token.accountIds = [];
        token.readOnlyAccountIds = [];
        token.namespacedAccountIds = [];
        token.namespacedReadOnlyAccountIds = [];
        token.k8sNamespaces = {};
      }
    }
  }
}

function getSessionExpirationSeconds() {
  let expiration = 1 * 24 * 60 * 60;
  if (process.env.NEXTAUTH_SESSION_EXPIRATION_DAYS) {
    expiration = parseInt(process.env.NEXTAUTH_SESSION_EXPIRATION_DAYS) * 24 * 60 * 60;
  } else if (process.env.NEXTAUTH_SESSION_EXPIRATION_HOURS) {
    expiration = parseInt(process.env.NEXTAUTH_SESSION_EXPIRATION_HOURS) * 60 * 60;
  }
  return expiration;
}

function getSessionUpdateSeconds() {
  let expiration = 1 * 60 * 60;
  if (process.env.NEXTAUTH_SESSION_UPDATE_DAYS) {
    expiration = parseInt(process.env.NEXTAUTH_SESSION_UPDATE_DAYS) * 24 * 60 * 60;
  } else if (process.env.NEXTAUTH_SESSION_UPDATE_HOURS) {
    expiration = parseInt(process.env.NEXTAUTH_SESSION_UPDATE_HOURS) * 60 * 60;
  }
  return expiration;
}

export const authOptions: NextAuthOptions = {
  adapter: GQLAdapter(),
  session: {
    strategy: 'jwt',
    maxAge: getSessionExpirationSeconds(),
    updateAge: getSessionUpdateSeconds(),
  },
  providers: providers,
  callbacks: {
    async jwt({ token, user, account: oauthAccount, trigger, session }) {
      try {
        //firsttime login flow for email
        if (oauthAccount?.provider === 'email' || oauthAccount?.provider === 'credentials' || oauthAccount?.provider === 'ldap') {
          await jwtUpdateTokenForCredentialToken(token, user, oauthAccount, trigger, session);
        }
        if (oauthAccount) {
          await jwtUpdateTokenForOAuth(token, user, oauthAccount, trigger, session);
        }

        if (user) {
          token.id = user.id;
          token.accountIds = token.accountIds ?? (user as NudgebeeUser).accountIds;
          token.readOnlyAccountIds = token.readOnlyAccountIds ?? (user as NudgebeeUser).readOnlyAccountIds;
          token.namespacedAccountIds = token.namespacedAccountIds ?? (user as NudgebeeUser).namespacedAccountIds;
          token.namespacedReadOnlyAccountIds = token.namespacedReadOnlyAccountIds ?? (user as NudgebeeUser).namespacedReadOnlyAccountIds;
          token.k8sNamespaces = token.k8sNamespaces ?? (user as NudgebeeUser).k8sNamespaces;
          token.hasMultipleTenantAccess = (user as NudgebeeUser).hasMultipleTenantAccess;

          // Check for super admin role (cross-tenant, tenant_id IS NULL)
          const userId = (user?.id || token?.id || token?.sub) as string;
          if (userId) {
            const superAdminRole = await getUserSuperAdminRole(userId);
            if (superAdminRole) {
              token.isSuperAdmin = superAdminRole === 'super_admin';
              token.isSuperAdminReadonly = superAdminRole === 'super_admin_readonly';
              token.hasMultipleTenantAccess = true;
            }
          }
        }
        await jwtUpdateTokenOnUpdateTrigger(token, session, trigger);
        try {
          token = await jwtGenerateHasuraTokenAndUpdate(token);
        } catch (error) {
          console.log('jwt, unable to decode token ', error);
          return {};
        }
        return token;
      } catch (error) {
        console.log('jwt, unable to handle jwt token ', error);
      }
      return {};
    },
    async session({ session, token }) {
      // this will expose tokens on session api causing possible security issue, for now disablig it
      try {
        const nudgeBeeSession = session as NudgebeeSession;
        if (token) {
          nudgeBeeSession.roles = (token.roles as string[]) ?? [];
          nudgeBeeSession.tenant = { name: (token.tenant as any)?.name };
          nudgeBeeSession.accountIds = [];
          if (token.accountIds) {
            nudgeBeeSession.accountIds = token.accountIds as string[];
          }
          nudgeBeeSession.readOnlyAccountIds = [];
          if (token.readOnlyAccountIds) {
            nudgeBeeSession.readOnlyAccountIds = token.readOnlyAccountIds as string[];
          }
          nudgeBeeSession.namespacedAccountIds = [];
          if (token.namespacedAccountIds) {
            nudgeBeeSession.namespacedAccountIds = token.namespacedAccountIds as string[];
          }
          nudgeBeeSession.namespacedReadOnlyAccountIds = [];
          if (token.namespacedReadOnlyAccountIds) {
            nudgeBeeSession.namespacedReadOnlyAccountIds = token.namespacedReadOnlyAccountIds as string[];
          }
          nudgeBeeSession.k8sNamespaces = {};
          if (token.k8sNamespaces) {
            nudgeBeeSession.k8sNamespaces = token.k8sNamespaces;
          }
          nudgeBeeSession.hasMultipleTenantAccess = !!token.hasMultipleTenantAccess;
        }
        nudgeBeeSession.onPrem = false;
        const licenseDetails = await getLicenseDetails();
        if (licenseDetails.licenseType === 'on-prem' && licenseDetails.tenantId && licenseDetails.email === session?.user?.email) {
          nudgeBeeSession.onPrem = true;
        }

        nudgeBeeSession.error = token.error as string;
        nudgeBeeSession.appVersion = process.env.NEXT_PUBLIC_APP_VERSION;
        nudgeBeeSession.pendoEnable = process.env.PENDO_ENABLE ?? 'false';
        nudgeBeeSession.isSuperAdmin = !!token.isSuperAdmin;
        nudgeBeeSession.isSuperAdminReadonly = !!token.isSuperAdminReadonly;

        // update user access time (skip for super admin - no trace, throttle to once per 5 min per user)
        if (!token.isSuperAdmin && !token.isSuperAdminReadonly) {
          cleanupUserAccessCache();
          const userKey = `${nudgeBeeSession.user?.email}:${(token.tenant as any)?.id}`;
          const now = Date.now();
          const lastUpdate = _userAccessUpdateCache.get(userKey) ?? 0;
          if (now - lastUpdate > USER_ACCESS_THROTTLE_MS) {
            _userAccessUpdateCache.set(userKey, now);
            try {
              await updateUserAccountAccessedByUsername(nudgeBeeSession.user?.email ?? '', (token.tenant as any)?.id);
            } catch (err) {
              // Roll back throttle entry so the write is retried next session
              if (lastUpdate > 0) {
                _userAccessUpdateCache.set(userKey, lastUpdate);
              } else {
                _userAccessUpdateCache.delete(userKey);
              }
              console.log('unable to update user access time ', err);
            }
          }
        }
        return nudgeBeeSession;
      } catch (error) {
        console.log('session, unable to get session ', error);
      }
      return { expires: new Date(1970, 1, 1).toISOString() };
    },
    async redirect({ url, baseUrl }) {
      if (url.startsWith('/')) {
        // Allows relative callback URLs
        return `${baseUrl}${url}`;
      } else if (url == baseUrl + '/') {
        return baseUrl;
      }
      try {
        const parsedUrl = new URL(url);
        if (parsedUrl.searchParams.has('callbackUrl')) {
          let callbackUrl = parsedUrl.searchParams.get('callbackUrl') as string;
          callbackUrl = decodeURIComponent(callbackUrl);
          if (callbackUrl.startsWith('/')) {
            // Allows relative callback URLs
            return `${baseUrl}${callbackUrl}`;
          }
          return callbackUrl;
        }
        if (parsedUrl.origin === baseUrl && !url.endsWith('/signin')) {
          return url;
        }
      } catch {
        console.log('redirect called, invalid url', url);
        return baseUrl;
      }
      return baseUrl;
    },
    async signIn({ user, account, email }) {
      console.log('signIn called', user, account, email);
      if (user?.email) {
        user.email = user.email.toLowerCase();
      }
      if (
        (account?.provider === 'email' || account?.provider === 'credentials' || account?.provider === 'ldap' || account?.provider === 'teleport') &&
        user?.email
      ) {
        const userList = await getUserByUsername({ username: user?.email, fetchRoles: false, fetchAccounts: true, fetchGroups: true });
        let userAccount = null;
        if (userList.data && userList.data.users.length > 0) {
          userAccount = userList.data.users[0];
          if (userAccount.status !== 'suspended') {
            return true;
          }
        }
        return false;
      } else if (account?.type === 'oauth' && user?.email) {
        const userList = await getUserByUsername({ username: user?.email, fetchRoles: false, fetchAccounts: false, fetchGroups: true });
        const domainOfUser = user?.email?.split('@')?.[1] || '';
        let tenantId = '';
        let tenantAttrs = [];
        if (domainOfUser) {
          tenantAttrs = await getTenantAttributes();
          if (tenantAttrs && tenantAttrs?.length > 0) {
            const allowedDomainsArr = tenantAttrs.filter((f: any) => f.name === 'allowed_domains');
            for (const allowedDomains of allowedDomainsArr) {
              if (allowedDomains.value) {
                try {
                  const allowedDomainsList = JSON.parse(allowedDomains.value);
                  if (Array.isArray(allowedDomainsList) && allowedDomainsList.includes(domainOfUser)) {
                    tenantId = allowedDomains.tenant_id;
                    break; // stop at the first match
                  }
                } catch (e) {
                  console.log('Failed to parse allowedDomain -', e, allowedDomains);
                }
              }
            }
          }
        }
        if (userList?.data?.users?.length > 0) {
          const userAccount = userList.data.users[0];
          if (userAccount.status !== 'suspended') {
            // Ensure allowed_domains is set for on-prem existing OAuth users (backfill)
            const license = await getLicenseDetails();
            if (license.licenseType === 'on-prem' && license.tenantId) {
              await ensureAllowedDomainsSet(user.email, license.tenantId);
            }
            return true;
          }
          // If user is suspended, sign in should fail.
          return false;
        }
        const license = await getLicenseDetails();
        if (license.licenseType === 'on-prem') {
          console.log(`User with email ${user.email} not found. On-prem license detected. Attempting to onboard.`);

          const canAccess = await validateOnPremNoTenantAccess(license, user.email);
          if (!canAccess) {
            throw Error('NO_TENANT_ACCESS');
          }

          const isLicenseEmail = license.email && user.email.toLowerCase() === license.email.toLowerCase();
          if (!isLicenseEmail) {
            const domainOfUser = user?.email?.split('@')?.[1] || '';
            if (!domainOfUser) {
              console.error(`Cannot extract domain from email: ${user.email}`);
              return false;
            }

            const { tenantId: domainTenantId } = await findTenantByDomain(domainOfUser);
            if (!domainTenantId || domainTenantId !== license.tenantId) {
              console.error(`Domain ${domainOfUser} is not authorized for tenant ${license.tenantId}. Domain tenant: ${domainTenantId}`);
              throw Error('NO_TENANT_ACCESS');
            }
          }

          const roleName = process.env.AUTH_DEFAULT_ROLE;
          try {
            const newUser = await onboardUser({
              username: user.email,
              display_name: user.name || user.email.split('@')[0],
              status: 'active',
              ...(roleName && { role: roleName }),
              tenant_id: license.tenantId,
            });

            if (newUser.errors) {
              console.error('Error onboarding new user for on-prem:', newUser.errors);
              return false; // Onboarding failed
            }

            if (newUser.data?.id && account) {
              const linkResponse = await createUserAuthAccount({
                user: newUser.data.id,
                provider: account.provider?.replaceAll('-', '_') || '',
                provider_type: account.type || '',
                account_id: account.providerAccountId || '',
                name: account.provider?.replaceAll('-', '_') || '',
                status: 'active',
                accessed_at: new Date().toISOString(),
              });

              if (linkResponse.errors) {
                console.error('Error linking OAuth account to new on-prem user:', linkResponse.errors);
                return false;
              }
              user.id = newUser.data.id;
              await ensureAllowedDomainsSet(user.email, license.tenantId);
              console.log(`Successfully onboarded and linked on-prem account for ${user.email}`);
              return true; // Successfully onboarded and linked
            }
            console.error('Failed to get new user ID or account details after on-prem onboarding.');
            return false;
          } catch (e) {
            console.error('Exception during new on-prem user onboarding or account linking:', e);
            return false;
          }
        } else if (tenantId) {
          try {
            const defaultRole = tenantAttrs.find((f: any) => f.name == 'auth_default_role' && f.tenant_id == tenantId);
            const newUser = await onboardUser({
              username: user.email,
              display_name: user.name || user.email.split('@')[0],
              status: 'active',
              ...(defaultRole?.value && { role: defaultRole.value }),
              tenant_id: tenantId,
            });

            if (newUser.errors) {
              console.error('Error onboarding new user for on-prem:', newUser.errors);
              return false; // Onboarding failed
            }

            if (newUser.data?.id && account) {
              const linkResponse = await createUserAuthAccount({
                user: newUser.data.id,
                provider: account.provider?.replaceAll('-', '_') || '',
                provider_type: account.type || '',
                account_id: account.providerAccountId || '',
                name: account.provider?.replaceAll('-', '_') || '',
                status: 'active',
                accessed_at: new Date().toISOString(),
              });

              if (linkResponse.errors) {
                console.error('Error linking OAuth account to new saas user:', linkResponse.errors);
                return false;
              }
              user.id = newUser.data.id;
              console.log(`Successfully onboarded and linked saas account for ${user.email}`);
              return true; // Successfully onboarded and linked
            }
            console.error('Failed to get new user ID or account details after saas onboarding.');
            return false;
          } catch (e) {
            console.error('Exception during new user onboarding or account linking:', e);
            return false;
          }
        } else {
          console.log(`User with email ${user.email} not found. Non-on-prem license. Self-onboarding via OAuth is disabled.`);
          return false; // Not an on-prem license, so don't onboard.
        }
      }
      return false;
    },
  },
  events: {
    signIn: async (message) => {
      if (message.user) {
        const nudgeBeeUser = message.user as NudgebeeUser;
        // Skip login tracking for super admin (no trace)
        const superAdminRole = nudgeBeeUser.id ? await getUserSuperAdminRole(nudgeBeeUser.id) : null;
        if (!superAdminRole && nudgeBeeUser.userAccountId) {
          await updateUserAccountAccessed(nudgeBeeUser.userAccountId, nudgeBeeUser.tenant?.id);
        }
      }
    },
    signOut: async (message) => {
      console.log('signOut called', message);
    },
  },
  pages: {
    signIn: '/signin',
    verifyRequest: '/auth/verify-request',
  },
};

export default (req: NextApiRequest, res: NextApiResponse) => {
  // Protect magic link tokens from email security scanners (SafeLinks, Proofpoint, etc.)
  // Scanners first send HEAD, then follow up with GET using a spoofed browser user-agent.
  // The HEAD guard alone is insufficient — scanners consume the one-time token via GET
  // before the real user clicks. Solution: serve an intermediate HTML page that requires
  // JavaScript execution (which scanners don't do) to proceed to the actual verification.
  if (req.url?.includes('/api/auth/callback/email')) {
    if (req.method === 'HEAD') {
      res.status(200).end();
      return;
    }
    if (req.method === 'GET') {
      const reqUrl = new URL(req.url, `https://${req.headers.host || 'localhost'}`);
      if (!reqUrl.searchParams.has('confirm')) {
        reqUrl.searchParams.set('confirm', '1');
        const confirmUrl = reqUrl.pathname + reqUrl.search;
        res
          .status(200)
          .setHeader('Content-Type', 'text/html')
          .setHeader('Cache-Control', 'no-store, no-cache, must-revalidate, private')
          .setHeader('Pragma', 'no-cache').send(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Verifying...</title></head>
<body><p>Verifying your login...</p>
<script>window.location.replace("${confirmUrl}");</script>
<noscript><p><a href="${confirmUrl}">Click here to continue login</a></p></noscript>
</body></html>`);
        return;
      }
    }
  }

  // special handling for teleport self login
  if (req.url && req.headers?.['teleport-jwt-assertion'] && process.env.TELEPORT_ENABLED == 'true' && process.env.TELEPORT_SSO_ENABLED == 'true') {
    let pathname = req.url;
    if (pathname.startsWith('http://') || pathname.startsWith('https://')) {
      const url = new URL(pathname);
      pathname = url.pathname;
    } else {
      pathname = pathname.split('?')[0];
    }
    if (pathname === '/api/auth/signin') {
      res.redirect('/api/auth/signin/teleport');
      return;
    }
  }
  return NextAuth(req, res, authOptions);
};
