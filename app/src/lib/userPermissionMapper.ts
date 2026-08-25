import { getAccountByTenant } from '@lib/UserService';
/**
 * Shared utility for extracting and processing user roles/permissions
 * Used by both SAML authentication and NextAuth adapters
 */

/**
 * Is this role row granted inside the tenant the session opens in?
 *
 * Role rows are global to the user — `user_roles` holds every grant across every
 * tenant the user belongs to — so each row must be matched against the selected
 * tenant before it lands in the session. `tenant` rows carry the tenant in
 * `entity_id`; every other row carries it in `tenant_id` (populated by every
 * insert path in api-server since V325). `group_roles` rows carry neither — the
 * owning group's tenant is checked by the caller instead.
 *
 * A row with no tenant information is KEPT: this session scope is advisory (the
 * backend re-authorizes every request), so a missing field must not lock a user
 * out of access the backend still grants.
 */
export function roleBelongsToTenant(r: any, tenantId?: string) {
  if (!tenantId) return true;
  if (r.entity_type === 'tenant') return r.entity_id === tenantId;
  return !r.tenant_id || r.tenant_id === tenantId;
}

/**
 * Group membership is global; a group's roles only count inside the tenant that
 * owns the group. Older api-server builds do not return the group's tenant — fall
 * back to keeping the group for the same fail-open reason as above.
 */
export function groupBelongsToTenant(group: any, tenantId?: string) {
  const groupTenant = group?.user_group?.tenant;
  if (!tenantId || !groupTenant) return true;
  return groupTenant === tenantId;
}

export function adapterUserUpdateDataOnUserRoles(
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
    if (!roleBelongsToTenant(r, tenantId)) {
      return;
    }
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
    k8sNamespaces,
    tenant.id
  );

  const groups = user.groups ?? [];
  for (const group of groups) {
    if (!groupBelongsToTenant(group, tenant.id)) {
      continue;
    }
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

  roles = [...new Set(roles)];
  accountIds = [...new Set(accountIds)];
  readonlyAccountIds = [...new Set(readonlyAccountIds)];
  namespacedAccountIds = [...new Set(namespacedAccountIds)];
  namespacedReadOnlyAccountIds = [...new Set(namespacedReadOnlyAccountIds)];

  if (accountIds.length > 0 || readonlyAccountIds.length > 0 || namespacedAccountIds.length > 0 || namespacedReadOnlyAccountIds.length > 0) {
    // Narrow role-granted account ids to those that belong to the selected tenant.
    const resp = await getAccountByTenant(tenant.id);
    const tenantAccounts: string[] = resp.data?.cloud_accounts?.map((a: any) => a.id) ?? [];
    if (tenantAccounts.length > 0) {
      accountIds = accountIds.filter((a) => tenantAccounts.includes(a));
      readonlyAccountIds = readonlyAccountIds.filter((a) => tenantAccounts.includes(a));
      namespacedAccountIds = namespacedAccountIds.filter((a) => tenantAccounts.includes(a));
      namespacedReadOnlyAccountIds = namespacedReadOnlyAccountIds.filter((a) => tenantAccounts.includes(a));
      // k8sNamespaces is keyed by account id — prune it alongside the id lists, or a
      // namespace grant on another tenant's account stays visible in the session.
      for (const accountId of Object.keys(k8sNamespaces)) {
        if (!tenantAccounts.includes(accountId)) {
          delete k8sNamespaces[accountId];
        }
      }
    } else {
      // No tenant accounts resolved. This session scope is ADVISORY — the backend
      // re-authorizes every request — so we deliberately keep the user's explicit role
      // grants rather than fail closed: silently stripping them locked account-scoped
      // admins out of features the backend still authorizes (#32887), and throwing here
      // would escalate a transient lookup blip into a full login outage for every
      // account-scoped user. Log loudly, distinguishing a failed lookup from an empty one.
      console.warn(
        resp.errored
          ? 'tenant-account lookup failed; preserving role-granted account access for tenant'
          : 'no tenant accounts resolved; preserving role-granted account access for tenant',
        tenant.id
      );
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
