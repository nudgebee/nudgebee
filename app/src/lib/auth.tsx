import { useSession } from 'next-auth/react';
import { queryGraphQL } from '@lib/HttpService';
import Loader from '@components1/common/Loader';
let userData: any = {};

export function withAuth(Component: React.ComponentType<any | string>) {
  const WithAuthComponent = (props: any) => {
    const { data, status } = useSession({ required: true });
    if (status === 'loading') {
      return <Loader />;
    }

    userData = data;

    return <Component {...props} />;
  };
  return WithAuthComponent;
}

export function getUserSession() {
  return userData;
}

// returns null if user has access to all namespaces
export function getAllowedNamespaces(accountId: string): string[] | null {
  if (userData?.roles?.indexOf('tenant_admin') >= 0 || userData?.roles?.indexOf('tenant_admin_readonly') >= 0) {
    return null;
  }
  if (userData?.roles?.indexOf('account_admin') >= 0 && userData?.accountIds?.indexOf(accountId) >= 0) {
    return null;
  }
  if (userData?.roles?.indexOf('account_admin_readonly') >= 0 && userData?.readOnlyAccountIds?.indexOf(accountId) >= 0) {
    return null;
  }
  return userData?.k8sNamespaces?.[accountId] ?? null;
}

export function hasReadAccess(accountId?: string, namespace?: string): boolean {
  if (userData?.roles?.indexOf('tenant_admin') >= 0 || userData?.roles?.indexOf('tenant_admin_readonly') >= 0) {
    return true;
  }
  if (userData?.accountIds?.indexOf(accountId) >= 0) {
    return true;
  }
  if (userData?.readOnlyAccountIds?.indexOf(accountId) >= 0) {
    return true;
  }
  if (accountId && namespace) {
    const allowedNamespaces = getAllowedNamespaces(accountId) ?? [];
    if (userData?.namespacedAccountIds?.indexOf(accountId) >= 0 && allowedNamespaces != null && allowedNamespaces.indexOf(namespace) >= 0) {
      return true;
    }
    if (userData?.namespacedReadOnlyAccountIds?.indexOf(accountId) >= 0 && allowedNamespaces != null && allowedNamespaces.indexOf(namespace) >= 0) {
      return true;
    }
  }

  return false;
}

export function hasWriteAccess(accountId?: string, namespace?: string): boolean {
  if (userData?.roles?.indexOf('tenant_admin_readonly') >= 0) {
    return false;
  }
  if (userData?.roles?.indexOf('tenant_admin') >= 0) {
    return true;
  }
  if (userData?.accountIds?.indexOf(accountId) >= 0) {
    return true;
  }
  if (accountId && namespace) {
    const allowedNamespaces = getAllowedNamespaces(accountId) ?? [];
    if (userData?.namespacedAccountIds?.indexOf(accountId) >= 0 && allowedNamespaces != null && allowedNamespaces.indexOf(namespace) >= 0) {
      return true;
    }
  }
  return false;
}

export function hasDeleteAccess(accountId?: string): boolean {
  if (userData?.accountIds?.indexOf(accountId) >= 0) {
    return true;
  }
  return false;
}

export function isTenantAdmin(): boolean {
  if (userData?.roles?.indexOf('tenant_admin_readonly') >= 0) {
    return false;
  }
  if (userData?.roles?.indexOf('tenant_admin') >= 0) {
    return true;
  }
  return false;
}

const featureData: Record<string, any> = Object.create(null);

const LIST_TENANT_FEATURE_FLAGS = `
query GetTenantFeatureFlags {
  feature_flag_v2(where: { account_id: { _is_null: true } }){
    rows {
      status
      feature_id
      feature_module_id
    }
  }
}`;

const LIST_ACCOUNT_FEATURE_FLAGS = `
  query GetAccountFeatureFlags($accountId: String) {
    feature_flag_v2(where: { account_id: { _eq: $accountId } }) {
      rows {
        status
        feature_id
        feature_module_id
      }
    }
  }`;

export async function hasFeatureAccess(featureName: string): Promise<boolean> {
  const tenantKey = getTenantKey();
  if (!Object.hasOwn(featureData, tenantKey)) {
    try {
      const response = await queryGraphQL(LIST_TENANT_FEATURE_FLAGS, 'GetTenantFeatureFlags', {});
      featureData[tenantKey] = response?.data?.data?.feature_flag_v2?.rows || [];
    } catch (error) {
      console.log('failed to fetch feature flags-', error);
    }
  }
  const tenantFeatures: any[] = featureData[tenantKey];
  for (const f of tenantFeatures) {
    if (f['feature_id'] === featureName && f['status'] === 'enabled') {
      return true;
    }
  }

  return false;
}

const getTenantKey = () => userData?.tenant?.name?.replace(/[^a-zA-Z0-9_-]/g, '_') || '';

export async function fetchFeatureFlagsForTenant(refresh = false): Promise<any[]> {
  const tenantKey = getTenantKey();
  if (!tenantKey) {
    return [];
  }

  // Use cache only if refresh is false
  if (!refresh && Object.hasOwn(featureData, tenantKey)) {
    return featureData[tenantKey];
  }

  try {
    const response = await queryGraphQL(LIST_TENANT_FEATURE_FLAGS, 'GetTenantFeatureFlags', {});
    featureData[tenantKey] = response?.data?.data?.feature_flag_v2?.rows || [];
  } catch (error) {
    console.log('Failed to fetch feature flags -', error);
    featureData[tenantKey] = [];
  }

  return featureData[tenantKey];
}

export async function fetchFeatureFlagsForAccount(accountId: string, refresh: boolean = false): Promise<any[]> {
  const tenantKey = getTenantKey();
  if (!tenantKey || !accountId) {
    return [];
  }
  if (!refresh && Object.hasOwn(featureData, `${tenantKey}::${accountId}`)) {
    return featureData[`${tenantKey}::${accountId}`];
  }
  try {
    const response = await queryGraphQL(LIST_ACCOUNT_FEATURE_FLAGS, 'GetAccountFeatureFlags', { accountId });
    featureData[`${tenantKey}::${accountId}`] = response?.data?.data?.feature_flag_v2?.rows || [];
  } catch (error) {
    console.log('Failed to fetch feature flags -', error);
    featureData[`${tenantKey}::${accountId}`] = [];
  }

  return featureData[`${tenantKey}::${accountId}`];
}
