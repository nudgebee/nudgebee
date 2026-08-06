import { useSession } from 'next-auth/react';
import { useEffect, useRef, useState } from 'react';
import { queryGraphQL } from '@lib/HttpService';
import Loader from '@shared/Loader';

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

export function getCurrentTenant(): { id?: string; name?: string } {
  return userData?.tenant ?? {};
}

// True for users whose access spans the whole tenant (tenant admins, super
// admins). For these users the per-account session lists below are NOT
// authoritative — `accountIds`/`readOnlyAccountIds` are empty even though they
// can reach every account in the tenant. Callers that need to know whether an
// account belongs to the current tenant must consult the live accounts list
// (see useAccountGuard), not the session, for these roles.
export function isTenantWideRole(): boolean {
  return !!(
    userData?.roles?.includes('tenant_admin') ||
    userData?.roles?.includes('tenant_admin_readonly') ||
    userData?.isSuperAdmin ||
    userData?.isSuperAdminReadonly
  );
}

// ---------------------------------------------------------------------------
// Dynamic RBAC (custom roles) — the front-end half of the on/off switch.
//
// isCustomRolesEnabled() mirrors the backend security.CustomRolesEnabled():
// it is the tenant's CUSTOM_ROLES feature state, resolved once at session build
// (see resolveUserCustomPermissions). Every UI gate this feature introduced hangs
// off it, so with the feature off the app renders exactly as it did before
// dynamic RBAC existed — no disabled nav icons, no gated tabs, no Roles tab.
//
// It is deliberately NOT inferred from `permissions.length`: "feature on, user
// holds no grants" and "feature off" are different states, and only the first
// one may gate anything.
// ---------------------------------------------------------------------------
export function isCustomRolesEnabled(): boolean {
  return userData?.customRolesEnabled === true;
}

// True when the user holds any dynamic-RBAC custom-role grant. Like a
// tenant-wide role, a custom-grant holder's per-account session lists are NOT
// authoritative for their reach — custom grants are tenant-global operation
// surface and are never reflected in `accountIds`/`readOnlyAccountIds`/… — so a
// pure custom-role user has EMPTY session lists yet can reach accounts across
// the tenant. Callers deciding "is this account in the user's tenant?" must
// therefore consult the live accounts list for these users too, not the
// session (which would wrongly block them). See useAccountGuard.
export function hasAnyCustomGrant(): boolean {
  return isCustomRolesEnabled() && (userData?.permissions?.length ?? 0) > 0;
}

// True for a user whose access comes PURELY from dynamic-RBAC grants: no
// tenant-wide role and no account/namespace grant in the session. Their built-in
// role authorizes nothing, so per-module UI gating is meaningful for them and
// only for them — every other user keeps every surface, exactly as before.
//
// Returns false whenever the feature is off, which is what makes the whole
// permission-gating layer (nav icons, cluster tabs, quick links, Troubleshoot
// sub-tabs) vanish with the switch. `accountId === 'demo'` callers pass the demo
// account so the shared product showcase is never hobbled.
export function isGrantsOnlyUser(accountId?: string): boolean {
  if (!isCustomRolesEnabled()) {
    return false;
  }
  if (accountId === 'demo') {
    return false;
  }
  return !isTenantWideRole() && getSessionAccountIds().length === 0;
}

// Union of every account id the session explicitly grants the user (in the
// current tenant). Authoritative ONLY for non-tenant-wide users — see
// isTenantWideRole. Useful as an instant hint before the live accounts list
// resolves.
export function getSessionAccountIds(): string[] {
  return [
    ...(userData?.accountIds ?? []),
    ...(userData?.readOnlyAccountIds ?? []),
    ...(userData?.namespacedAccountIds ?? []),
    ...(userData?.namespacedReadOnlyAccountIds ?? []),
  ];
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
  // Namespace-scoped admins (k8s_namespace_admin / *_readonly) hold read access to the
  // account that contains their namespaces. When no specific namespace is requested
  // (account-level read, e.g. starting an Ask-Nudgebee investigation), grant access if
  // the account appears in either namespaced set — otherwise these users are falsely
  // denied even though the backend authorizes them (#32887). The per-namespace block
  // below still applies the finer namespace check when a namespace IS supplied.
  if (accountId && !namespace) {
    if (userData?.namespacedAccountIds?.includes(accountId)) {
      return true;
    }
    if (userData?.namespacedReadOnlyAccountIds?.includes(accountId)) {
      return true;
    }
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

// Dynamic-RBAC: does the current user hold a custom-role grant for
// (module, class)? UI-gating helper only (hide buttons / tabs) — the
// authoritative gate is the rpcGateway + Go handlers. `module`/`class` must be
// the normalized values from @lib/permissionCatalog (e.g. hasPermission(
// 'notifications', 'Write')).
export function hasPermission(module: string, permissionClass: 'Read' | 'Write' | 'Execute'): boolean {
  if (!isCustomRolesEnabled()) {
    return false;
  }
  return userData?.permissions?.includes(`${module}:${permissionClass}`) ?? false;
}

// Standard message for a disabled/greyed control the current user lacks the grant
// for. Names the exact `<module>:<Class>` permission so the user can ask an admin
// for precisely that grant (e.g. `tickets:Write`, `k8s:Read`). Use as the tooltip
// on any permission-disabled tab/button.
export function missingPermissionMessage(permission: string): string {
  return `You need the "${permission}" permission. Ask an admin to grant it.`;
}

// Modules backing the sections of the Admin page (/user-management): Users,
// Groups, Audits, Notifications, Integrations, Ownership, and the EE Roles &
// Permissions tab. Kept in sync with baseFilters in
// app/src/pages/user-management/index.jsx.
// Advisory mirror of the backend SecurityContext.CanManage(module, class): a
// full tenant admin, OR a custom-role holder with the (module, class) grant.
// Use to show/hide write actions in tenant-config surfaces (the Admin page).
// tenant_admin_readonly is intentionally excluded (isTenantAdmin() is false for
// it) so read-only admins never see write controls. The backend re-checks, so
// this is purely to avoid surfacing actions that would 403.
export function canManage(module: string, permissionClass: 'Read' | 'Write' | 'Execute'): boolean {
  return isTenantAdmin() || hasPermission(module, permissionClass);
}

const ADMIN_SURFACE_MODULES = ['users', 'usergroups', 'audits', 'notifications', 'integrations', 'ownership', 'customroles', 'roles'];

// Should the Admin sidebar tab / route be reachable for the current user?
// True for tenant-wide admins (hasReadAccess) and for any custom-role holder
// with a grant on an Admin-page module — e.g. an Audit-Read-only reviewer. The
// per-section visibility inside the page is gated separately (disabled tabs).
export function hasAdminSurfaceAccess(): boolean {
  if (hasReadAccess()) {
    return true;
  }
  if (!isCustomRolesEnabled()) {
    return false;
  }
  const perms: string[] = userData?.permissions ?? [];
  return perms.some((p) => ADMIN_SURFACE_MODULES.includes(p.split(':')[0]));
}

/**
 * Deployment-level UI toggle (a `UI_ENABLE_*` env var on the pod), read off the
 * session — see `uiFeatures` in [...nextauth].ts.
 *
 * This is NOT a tenant feature flag: it is per-deployment, not per-tenant, so
 * `hasFeatureAccess` / `requiresFeature` can't answer it. Use it when a surface
 * is gated on one of these vars but the code asking isn't the page that receives
 * them as props — e.g. the guided-tour catalog deciding whether /optimise will
 * render its AI Gateway tab.
 *
 * Sync and needs no warming (unlike `hasFeatureAccessCached`): `withAuth`
 * populates `userData` before any of this renders. Fails closed if the session
 * predates the field.
 */
export function isUiFeatureEnabled(feature: 'llmGateway'): boolean {
  return Boolean(userData?.uiFeatures?.[feature]);
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

/**
 * Synchronous read of the already-fetched tenant feature flags. Returns false if
 * the flags haven't been loaded yet — call `fetchFeatureFlagsForTenant()` first
 * (or rely on another page having warmed the cache). Use for render-time gating
 * where the async `hasFeatureAccess` can't be awaited.
 */
export function hasFeatureAccessCached(featureName: string): boolean {
  const tenantFeatures: any[] | undefined = featureData[getTenantKey()];
  if (!tenantFeatures) {
    return false;
  }
  return tenantFeatures.some((f) => f['feature_id'] === featureName && f['status'] === 'enabled');
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

/**
 * Account-or-tenant feature check, mirroring runbook-server's
 * `common.IsFeatureEnabledForAccount`: an account-scoped row wins, then a
 * tenant-wide row, otherwise disabled.
 *
 * `hasFeatureAccess` alone reads only tenant-wide rows
 * (`account_id IS NULL`), so a per-account rollout — the documented way to
 * pilot a feature — turns the backend on while leaving the UI believing the
 * feature is off. Any UI gate gating the same capability as a server-side
 * account-aware check must use this instead.
 */
export async function hasFeatureAccessForAccount(featureName: string, accountId?: string): Promise<boolean> {
  if (accountId) {
    const accountFlags = await fetchFeatureFlagsForAccount(accountId);
    if (accountFlags.some((f) => f['feature_id'] === featureName && f['status'] === 'enabled')) {
      return true;
    }
  }
  return hasFeatureAccess(featureName);
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
