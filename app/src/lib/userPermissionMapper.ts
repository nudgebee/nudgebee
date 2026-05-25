import { getAccountByTenant } from '@lib/UserService';
/**
 * Shared utility for extracting and processing user roles/permissions
 * Used by both SAML authentication and NextAuth adapters
 */

export function adapterUserUpdateDataOnUserRoles(
  user_roles: any[],
  roles: string[],
  accountIds: string[],
  readonlyAccountIds: string[],
  namespacedAccountIds: string[],
  namespacedReadOnlyAccountIds: string[],
  k8sNamespaces: any
) {
  user_roles?.forEach((r: any) => {
    if (r.entity_type && r.entity_type == 'tenant') {
      roles.push(r.role);
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

export async function extractUserPermissions(user: any) {
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

  // Filter roles based on tenant
  user.user_roles = user.user_roles ?? [];

  adapterUserUpdateDataOnUserRoles(
    user.user_roles,
    roles,
    accountIds,
    readonlyAccountIds,
    namespacedAccountIds,
    namespacedReadOnlyAccountIds,
    k8sNamespaces
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
      k8sNamespaces
    );
  }

  roles = [...new Set(roles)];
  accountIds = [...new Set(accountIds)];
  readonlyAccountIds = [...new Set(readonlyAccountIds)];
  namespacedAccountIds = [...new Set(namespacedAccountIds)];
  namespacedReadOnlyAccountIds = [...new Set(namespacedReadOnlyAccountIds)];

  if (accountIds.length > 0 || readonlyAccountIds.length > 0) {
    // Get accountIds from given tenant
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

  return {
    tenant,
    roles,
    accountIds,
    readonlyAccountIds,
    namespacedAccountIds,
    namespacedReadOnlyAccountIds,
    k8sNamespaces,
  };
}

export function getSessionExpirationSeconds() {
  let expiration = 1 * 24 * 60 * 60;
  if (process.env.NEXTAUTH_SESSION_EXPIRATION_DAYS) {
    expiration = parseInt(process.env.NEXTAUTH_SESSION_EXPIRATION_DAYS) * 24 * 60 * 60;
  } else if (process.env.NEXTAUTH_SESSION_EXPIRATION_HOURS) {
    expiration = parseInt(process.env.NEXTAUTH_SESSION_EXPIRATION_HOURS) * 60 * 60;
  }
  return expiration;
}
