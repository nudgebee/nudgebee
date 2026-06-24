import { useSession } from 'next-auth/react';
import { useEffect, useRef, useState } from 'react';
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

// A signed-in user can briefly get an HTML 404 from /api/auth/session instead of
// JSON when the NextAuth API route (or the backend it calls) is still coming up
// — most often in local dev when the frontend and API server start together.
// NextAuth's client reads that parse failure (CLIENT_FETCH_ERROR) as
// "unauthenticated" and bounces to /api/auth/signin, which (when it is also
// still compiling) 404s too — producing the redirect loop in issue #394.
// To ride out that window we re-check the session a few times before concluding
// the user is actually logged out. AUTH_RETRY_LIMIT * AUTH_RETRY_DELAY_MS bounds
// the wait so a genuinely-down backend still falls through to sign-in.
const AUTH_RETRY_LIMIT = 5;
const AUTH_RETRY_DELAY_MS = 1000;

type AuthProbe = 'authenticated' | 'unauthenticated' | 'unavailable';

// useSession()'s status can't tell "logged out" from "endpoint down" — both
// collapse to "unauthenticated". Inspect /api/auth/session directly: a JSON body
// means the endpoint is healthy (empty object => logged out, populated =>
// signed in), while a non-OK / non-JSON / network failure means it's transiently
// unavailable and worth retrying.
async function probeSession(): Promise<AuthProbe> {
  try {
    const res = await fetch('/api/auth/session', { headers: { Accept: 'application/json' }, cache: 'no-store' });
    if (!res.ok) return 'unavailable';
    if (!(res.headers.get('content-type') || '').includes('application/json')) return 'unavailable';
    const session = await res.json();
    return session && Object.keys(session).length > 0 ? 'authenticated' : 'unauthenticated';
  } catch {
    return 'unavailable';
  }
}

function redirectToSignIn() {
  if (window.location.pathname === '/signin') return;
  // Use the current path only (never the full href, which may already carry a
  // callbackUrl) so repeated bounces can never nest callbackUrls into each other.
  const callbackUrl = window.location.pathname + window.location.search;
  window.location.href = `/signin?${new URLSearchParams({ callbackUrl })}`;
}

export function withAuth(Component: React.ComponentType<any | string>) {
  const WithAuthComponent = (props: any) => {
    const { data, status } = useSession();
    const retriesRef = useRef(0);
    const [retryTick, setRetryTick] = useState(0);

    useEffect(() => {
      if (status !== 'unauthenticated') {
        retriesRef.current = 0;
        return;
      }
      let cancelled = false;
      let timer: ReturnType<typeof setTimeout> | undefined;
      void (async () => {
        const probe = await probeSession();
        if (cancelled) return;
        if (probe === 'authenticated') {
          // The endpoint is healthy and a session exists, but NextAuth's client
          // state is stuck "unauthenticated" from the earlier failed fetch. Its
          // broadcast channel uses storage events that don't fire in the same
          // tab, and update() no-ops without an existing session, so neither can
          // refresh us here — reload to let NextAuth re-read the session cleanly.
          window.location.reload();
          return;
        }
        if (probe === 'unauthenticated') {
          redirectToSignIn();
          return;
        }
        // 'unavailable' => transient. Retry a bounded number of times before
        // giving up and sending the user to sign-in.
        if (retriesRef.current < AUTH_RETRY_LIMIT) {
          retriesRef.current += 1;
          timer = setTimeout(() => {
            if (!cancelled) setRetryTick((n) => n + 1);
          }, AUTH_RETRY_DELAY_MS);
        } else {
          redirectToSignIn();
        }
      })();
      return () => {
        cancelled = true;
        if (timer) clearTimeout(timer);
      };
    }, [status, retryTick]);

    if (status !== 'authenticated') {
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
  featureflags_list(where: { account_id: { _is_null: true } }){
    rows {
      status
      feature_id
      feature_module_id
    }
  }
}`;

const LIST_ACCOUNT_FEATURE_FLAGS = `
  query GetAccountFeatureFlags($accountId: String) {
    featureflags_list(where: { account_id: { _eq: $accountId } }) {
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
      featureData[tenantKey] = response?.data?.data?.featureflags_list?.rows || [];
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
    featureData[tenantKey] = response?.data?.data?.featureflags_list?.rows || [];
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
    featureData[`${tenantKey}::${accountId}`] = response?.data?.data?.featureflags_list?.rows || [];
  } catch (error) {
    console.log('Failed to fetch feature flags -', error);
    featureData[`${tenantKey}::${accountId}`] = [];
  }

  return featureData[`${tenantKey}::${accountId}`];
}
