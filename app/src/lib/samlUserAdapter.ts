import { mapSamlGroupsToNudgebeeGroups, type SamlUser } from './saml';
import {
  createUserAuthAccount,
  getUserByAccountIdAndAccountProvider,
  getUserByUsername,
  onboardUser,
  updateUserStatus,
  getTenantAttributes,
  syncUserRoles,
} from '@lib/UserService';
import { extractUserPermissions } from './userPermissionMapper';
import { getLicenseDetails, type LicenseDetails } from '@lib/license';

async function mapAppUser(user: any) {
  const permissions = await extractUserPermissions(user);

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
    roles: permissions.roles,
    tenant: permissions.tenant,
    userAccountId: authAccountId,
    status: user.status,
    accountIds: permissions.accountIds,
    readOnlyAccountIds: permissions.readonlyAccountIds,
    namespacedAccountIds: permissions.namespacedAccountIds,
    namespacedReadOnlyAccountIds: permissions.namespacedReadOnlyAccountIds,
    k8sNamespaces: permissions.k8sNamespaces,
  };
}

/**
 * Sync SAML groups to Nudgebee roles on login
 * Controlled by environment variables:
 * - SAML_SYNC_ROLES_ON_LOGIN: Enable/disable role sync (default: true)
 * - SAML_REMOVE_OLD_ROLES: Remove roles user no longer has in SAML (default: true)
 */
async function syncSamlRolesOnLogin(samlUser: SamlUser, username: string, tenantId: string): Promise<void> {
  const syncEnabled = process.env.SAML_SYNC_ROLES_ON_LOGIN !== 'false';
  if (!syncEnabled) {
    console.info(`[SAML:RoleSync] Role sync disabled (SAML_SYNC_ROLES_ON_LOGIN=false) for user=${username}`);
    return;
  }

  const samlGroups = samlUser.groups || [];
  console.info(`[SAML:RoleSync] Starting role sync for user=${username}, tenant=${tenantId}, samlGroups=${JSON.stringify(samlGroups)}`);

  const nudgebeeRoles = mapSamlGroupsToNudgebeeGroups(samlGroups);

  if (nudgebeeRoles.length === 0) {
    console.info(`[SAML:RoleSync] No roles resolved — defaulting to tenant_admin for user=${username}`);
    nudgebeeRoles.push('tenant_admin');
  }

  const removeOldRoles = process.env.SAML_REMOVE_OLD_ROLES !== 'false';
  console.info(`[SAML:RoleSync] Syncing roles for user=${username}: roles=${JSON.stringify(nudgebeeRoles)}, removeOldRoles=${removeOldRoles}`);

  const result = await syncUserRoles(username, tenantId, nudgebeeRoles, removeOldRoles);

  if (result.errors) {
    console.error(`[SAML:RoleSync] FAILED for user=${username}, tenant=${tenantId}:`, result.errors);
  } else {
    console.info(`[SAML:RoleSync] Success for user=${username}, tenant=${tenantId}`);
  }
}

/**
 * Try to find existing user by SAML provider account
 */
async function tryFindExistingUserByProviderAccount(providerAccountId: string, provider: string, samlUser: SamlUser) {
  console.info(`[SAML:UserLookup] Looking up user by provider account — provider=${provider}, accountId=${providerAccountId}`);

  const accountResp = await getUserByAccountIdAndAccountProvider({
    accountId: providerAccountId,
    accountProvider: provider,
    fetchRoles: true,
    fetchAccounts: true,
    fetchGroups: true,
  });

  if (!accountResp?.data?.user_auths?.length) {
    console.info(`[SAML:UserLookup] No existing user found by provider account — accountId=${providerAccountId}`);
    return null;
  }

  const accountsData = accountResp.data.user_auths[0];
  if (accountsData.user.status === 'suspended') {
    console.warn(`[SAML:UserLookup] User is SUSPENDED — email=${accountsData.user.username}, userId=${accountsData.user.id}`);
    return null;
  }

  const user = accountsData.user;
  console.info(`[SAML:UserLookup] Found existing user by provider account — email=${user.username}, userId=${user.id}, status=${user.status}`);

  const userTenants = user.tenants || [];
  if (userTenants.length > 0) {
    for (const userTenant of userTenants) {
      const tenantId = userTenant.id;
      if (tenantId) {
        await syncSamlRolesOnLogin(samlUser, user.username, tenantId);
      }
    }
  }

  return await mapAppUser(user);
}

/**
 * Link SAML account to existing user
 */
async function linkSamlAccountToUser(userId: string, provider: string, providerAccountId: string) {
  console.info(`[SAML:LinkAccount] Linking SAML account — userId=${userId}, provider=${provider}, accountId=${providerAccountId}`);

  const linkResponse = await createUserAuthAccount({
    user: userId,
    provider,
    provider_type: 'saml',
    account_id: providerAccountId,
    name: provider,
    status: 'active',
    accessed_at: new Date().toISOString(),
  });

  if (linkResponse.errors) {
    console.error(`[SAML:LinkAccount] FAILED to link account — userId=${userId}:`, linkResponse.errors);
    throw new Error('Unable to link SAML account');
  }
  console.info(`[SAML:LinkAccount] Successfully linked — userId=${userId}`);
}

/**
 * Fetch user after onboarding with all required data
 */
async function fetchUserAfterOnboarding(email: string) {
  const freshUserResp = await getUserByUsername({
    username: email,
    fetchRoles: true,
    fetchAccounts: true,
    fetchGroups: true,
  });

  if (freshUserResp?.data?.users?.length) {
    return await mapAppUser(freshUserResp.data.users[0]);
  }

  return null;
}

/**
 * Try to link existing user found by email
 */
async function tryLinkExistingUserByEmail(samlUser: SamlUser, provider: string, providerAccountId: string) {
  console.info(`[SAML:UserLookup] Looking up user by email — email=${samlUser.email}`);

  const userResp = await getUserByUsername({
    username: samlUser.email,
    fetchRoles: true,
    fetchAccounts: true,
    fetchGroups: true,
  });

  if (!userResp?.data?.users?.length) {
    console.info(`[SAML:UserLookup] No existing user found by email — email=${samlUser.email}`);
    return null;
  }

  const existing = userResp.data.users[0];

  if (existing.status === 'suspended') {
    console.warn(`[SAML:UserLookup] User found by email but SUSPENDED — email=${samlUser.email}, userId=${existing.id}`);
    return null;
  }

  console.info(
    `[SAML:UserLookup] Found existing user by email — email=${samlUser.email}, userId=${existing.id}, status=${existing.status}. Linking SAML account...`
  );
  await linkSamlAccountToUser(existing.id, provider, providerAccountId);

  if (existing.status === 'inactive') {
    console.info(`[SAML:UserLookup] Activating inactive user — userId=${existing.id}`);
    await updateUserStatus(existing.id, 'active');
  }

  const userTenants = existing.tenants || [];
  if (userTenants.length > 0) {
    for (const userTenant of userTenants) {
      const tenantId = userTenant.id;
      if (tenantId) {
        await syncSamlRolesOnLogin(samlUser, existing.username, tenantId);
      }
    }
  }

  return await mapAppUser(existing);
}

/**
 * Find tenant by domain matching
 */
export async function findTenantByDomain(domain: string): Promise<{ tenantId: string; tenantAttrs: any[] }> {
  if (!domain) {
    return { tenantId: '', tenantAttrs: [] };
  }

  const tenantAttrs = await getTenantAttributes();
  if (!tenantAttrs?.length) {
    return { tenantId: '', tenantAttrs: [] };
  }

  const allowedDomainsArr = tenantAttrs.filter((f: any) => f.name === 'allowed_domains');

  for (const allowedDomains of allowedDomainsArr) {
    if (!allowedDomains.value) {
      continue;
    }

    try {
      const allowedDomainsList = JSON.parse(allowedDomains.value);
      if (Array.isArray(allowedDomainsList) && allowedDomainsList.includes(domain)) {
        return { tenantId: allowedDomains.tenant_id, tenantAttrs };
      }
    } catch (e) {
      console.log('Failed to parse allowedDomain -', e, allowedDomains);
    }
  }

  return { tenantId: '', tenantAttrs };
}

/**
 * Create on-prem SAML user
 */
async function createOnPremSamlUser(samlUser: SamlUser, license: LicenseDetails, provider: string, providerAccountId: string) {
  console.info(`[SAML:Onboard] Creating on-prem SAML user — email=${samlUser.email}, tenant=${license.tenantId}`);

  const roleName = process.env.AUTH_DEFAULT_ROLE;
  const newUser = await onboardUser({
    username: samlUser.email,
    display_name: samlUser.name || samlUser.email.split('@')[0],
    status: 'active',
    ...(roleName && { role: roleName }),
    tenant_id: license.tenantId,
  });

  if (newUser.errors) {
    console.error(`[SAML:Onboard] FAILED to onboard on-prem user — email=${samlUser.email}:`, newUser.errors);
    throw new Error('Unable to onboard SAML user');
  }

  if (!newUser.data?.id) {
    console.error(`[SAML:Onboard] Onboard returned no user ID — email=${samlUser.email}`);
    throw new Error('Failed to create on-prem user');
  }

  console.info(`[SAML:Onboard] On-prem user created — userId=${newUser.data.id}, email=${samlUser.email}`);
  await linkSamlAccountToUser(newUser.data.id, provider, providerAccountId);

  if (license.tenantId) {
    await syncSamlRolesOnLogin(samlUser, samlUser.email, license.tenantId);
  }

  return await fetchUserAfterOnboarding(samlUser.email);
}

/**
 * Create SaaS SAML user
 */
async function createSaasSamlUser(samlUser: SamlUser, tenantId: string, tenantAttrs: any[], provider: string, providerAccountId: string) {
  console.info(`[SAML:Onboard] Creating SaaS SAML user — email=${samlUser.email}, tenant=${tenantId}`);

  const defaultRole = tenantAttrs.find((f: any) => f.name === 'auth_default_role' && f.tenant_id === tenantId);
  const newUser = await onboardUser({
    username: samlUser.email,
    display_name: samlUser.name || samlUser.email.split('@')[0],
    status: 'active',
    ...(defaultRole?.value && { role: defaultRole.value }),
    tenant_id: tenantId,
  });

  if (newUser.errors) {
    console.error(`[SAML:Onboard] FAILED to onboard SaaS user — email=${samlUser.email}:`, newUser.errors);
    throw new Error('Unable to onboard SAML user');
  }

  if (!newUser.data?.id) {
    console.error(`[SAML:Onboard] Onboard returned no user ID — email=${samlUser.email}`);
    throw new Error('Failed to create SaaS user');
  }

  console.info(`[SAML:Onboard] SaaS user created — userId=${newUser.data.id}, email=${samlUser.email}`);
  await linkSamlAccountToUser(newUser.data.id, provider, providerAccountId);

  await syncSamlRolesOnLogin(samlUser, samlUser.email, tenantId);

  return await fetchUserAfterOnboarding(samlUser.email);
}

/**
 * Create new SAML user based on license and domain
 */
async function createNewSamlUser(samlUser: SamlUser, provider: string, providerAccountId: string) {
  console.info(`[SAML:Onboard] No existing user found — attempting to create new user for email=${samlUser.email}`);
  const license = await getLicenseDetails();
  console.info(`[SAML:Onboard] License: type=${license.licenseType}, tenantId=${license.tenantId || 'N/A'}`);

  // On-prem mode takes precedence
  if (license.licenseType === 'on-prem' && license.tenantId) {
    return await createOnPremSamlUser(samlUser, license, provider, providerAccountId);
  }

  // SaaS mode with domain matching
  const domain = samlUser.email.split('@')?.[1] || '';
  console.info(`[SAML:Onboard] SaaS mode — looking up tenant for domain=${domain}`);
  const { tenantId, tenantAttrs } = await findTenantByDomain(domain);

  if (tenantId) {
    console.info(`[SAML:Onboard] Found tenant=${tenantId} for domain=${domain}`);
    return await createSaasSamlUser(samlUser, tenantId, tenantAttrs, provider, providerAccountId);
  }

  console.warn(`[SAML:Onboard] DENIED — email=${samlUser.email}, domain=${domain}. No matching tenant or on-prem license. Self-onboarding disabled.`);
  return null;
}

export async function findOrCreateSamlUser(samlUser: SamlUser) {
  const provider = 'saml';
  const providerAccountId = samlUser.nameID || samlUser.id;

  console.info(
    `[SAML:Auth] findOrCreateSamlUser — email=${samlUser.email}, nameID=${samlUser.nameID}, groups=${JSON.stringify(samlUser.groups || [])}`
  );

  // Try to find existing user by provider account
  const existingByAccount = await tryFindExistingUserByProviderAccount(providerAccountId, provider, samlUser);
  if (existingByAccount) {
    console.info(`[SAML:Auth] Returning existing user (matched by provider account) — email=${samlUser.email}, userId=${existingByAccount.id}`);
    return existingByAccount;
  }

  // Try to link existing user by email
  const existingByEmail = await tryLinkExistingUserByEmail(samlUser, provider, providerAccountId);
  if (existingByEmail) {
    console.info(`[SAML:Auth] Returning existing user (matched by email, linked SAML) — email=${samlUser.email}, userId=${existingByEmail.id}`);
    return existingByEmail;
  }

  // Create new user
  const newUser = await createNewSamlUser(samlUser, provider, providerAccountId);
  if (newUser) {
    console.info(`[SAML:Auth] New user created and returned — email=${samlUser.email}, userId=${newUser.id}`);
  } else {
    console.warn(`[SAML:Auth] User creation returned null — email=${samlUser.email}. User will be denied access.`);
  }
  return newUser;
}
