import { useSession } from 'next-auth/react';
import { queryGraphQL } from '@lib/HttpService';
import Loader from '@components1/common/Loader';

// AUTHORIZATION MODEL — front-end vs back-end
//
// Everything exported here is ADVISORY: it shapes what UI shows / hides for a
// signed-in user. AUTHORITATIVE access control lives server-side:
//   1. `app/src/lib/actions.yaml` — per-action `permissions:` allow-list
//      gates each RPC route in @lib/rpcGateway before forwarding upstream.
//   2. Upstream Go handlers re-validate via security context (IsSuperAdmin /
//      tenant scoping / etc.) — even if the front-end gate is bypassed by a
//      crafted request, the handler still refuses.
//
// `withAuth` is a SESSION-PRESENCE gate (logged-in? then render), NOT a role
// gate. Role-level differentiation flows through the helpers below
// (`hasReadAccess`, `hasWriteAccess`, `isTenantAdmin`, `hasFeatureAccess`).
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
  if (userData?.roles?.includes('tenant_admin') || userData?.roles?.includes('tenant_admin_readonly')) {
    return null;
  }
  if (userData?.roles?.includes('account_admin') && userData?.accountIds?.includes(accountId)) {
    return null;
  }
  if (userData?.roles?.includes('account_admin_readonly') && userData?.readOnlyAccountIds?.includes(accountId)) {
    return null;
  }
  return userData?.k8sNamespaces?.[accountId] ?? null;
}

export function hasReadAccess(accountId?: string, namespace?: string): boolean {
  if (userData?.roles?.includes('tenant_admin') || userData?.roles?.includes('tenant_admin_readonly')) {
    return true;
  }
  if (userData?.accountIds?.includes(accountId)) {
    return true;
  }
  if (userData?.readOnlyAccountIds?.includes(accountId)) {
    return true;
  }
  if (accountId && namespace) {
    const allowedNamespaces = getAllowedNamespaces(accountId) ?? [];
    if (userData?.namespacedAccountIds?.includes(accountId) && allowedNamespaces != null && allowedNamespaces.includes(namespace)) {
      return true;
    }
    if (userData?.namespacedReadOnlyAccountIds?.includes(accountId) && allowedNamespaces != null && allowedNamespaces.includes(namespace)) {
      return true;
    }
  }

  return false;
}

export function hasWriteAccess(accountId?: string, namespace?: string): boolean {
  if (userData?.roles?.includes('tenant_admin')) {
    return true;
  }
  if (userData?.roles?.includes('tenant_admin_readonly')) {
    return false;
  }
  if (userData?.accountIds?.includes(accountId)) {
    return true;
  }
  if (accountId && namespace) {
    const allowedNamespaces = getAllowedNamespaces(accountId) ?? [];
    if (userData?.namespacedAccountIds?.includes(accountId) && allowedNamespaces != null && allowedNamespaces.includes(namespace)) {
      return true;
    }
  }
  return false;
}

export function hasDeleteAccess(accountId?: string): boolean {
  if (userData?.accountIds?.includes(accountId)) {
    return true;
  }
  return false;
}

export function isTenantAdmin(): boolean {
  if (userData?.roles?.includes('tenant_admin')) {
    return true;
  }
  if (userData?.roles?.includes('tenant_admin_readonly')) {
    return false;
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
