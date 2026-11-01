import { useSession } from 'next-auth/react';
import { useCallback, useEffect, useRef } from 'react';
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

// Set for the lifetime of the tab once we've already spent our one in-place
// recovery reload. If we land back on the unauthenticated branch with this set,
// the session really is gone and we sign the user out for real.
const SESSION_RECOVERY_FLAG = 'nb.session-recovery';

// Back-off between session reads that failed to reach the server: three
// retries, ~17s in all — enough for a Wi-Fi / VPN switch to settle — before we
// give up and sign the user out.
const SESSION_READ_RETRY_DELAYS_MS = [2_000, 5_000, 10_000];

// Caps a single read, so a request stranded on the old network can't hold the
// user on <Loader /> indefinitely.
const SESSION_READ_TIMEOUT_MS = 10_000;

type SessionRead = 'present' | 'absent' | 'unreachable';

// Reads /api/auth/session directly rather than through next-auth's getSession(),
// which swallows every failure and returns null — a request that never reached
// the server would look exactly like a signed-out user. 'absent' is the server
// answering 200 `{}`: a real answer. Anything else that isn't a session (fetch
// threw or timed out, non-2xx, a non-JSON body from a captive portal) is
// 'unreachable'.
async function readSession(): Promise<SessionRead> {
  // Not AbortSignal.timeout(): Safari < 16 lacks it, and there every read would
  // throw and end in a sign-out.
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), SESSION_READ_TIMEOUT_MS);
  try {
    const res = await fetch('/api/auth/session', { cache: 'no-store', signal: controller.signal });
    if (!res.ok) {
      return 'unreachable';
    }
    const body = await res.json();
    return Object.keys(body ?? {}).length > 0 ? 'present' : 'absent';
  } catch {
    return 'unreachable';
  } finally {
    clearTimeout(timer);
  }
}

// Waits out a retry delay and then, if the browser knows it's offline, for the
// connection to come back. Being offline says nothing about the session, so
// that wait has no deadline and doesn't use up a retry.
function waitBeforeRetry(delayMs: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(() => {
      if (navigator.onLine) {
        resolve();
      } else {
        window.addEventListener('online', () => resolve(), { once: true });
      }
    }, delayMs);
  });
}

async function readSessionWithRetry(): Promise<SessionRead> {
  let result = await readSession();
  for (const delayMs of SESSION_READ_RETRY_DELAYS_MS) {
    if (result !== 'unreachable') {
      break;
    }
    await waitBeforeRetry(delayMs);
    result = await readSession();
  }
  return result;
}

// What to do when next-auth reports the session as unauthenticated.
//
// next-auth's default `required: true` handler bounces to /signin on the FIRST
// unauthenticated render, and <SessionProvider> can never recover on its own
// afterwards: once `__NEXTAUTH._session === null`, both the poll timer and the
// visibilitychange handler early-return. So a single /api/auth/session response
// that came back empty — prod logs show these 12-49 min after login, well
// inside the 1h `maxAge`, because the browser occasionally sends no session
// cookie at all — costs the user a full re-login.
//
// The same dead end is hit when the poll never reaches the server at all:
// next-auth turns a failed fetch into `null` too, so switching networks
// (Chrome's ERR_NETWORK_CHANGED) or waking a laptop offline signed users out.
//
// Re-read the session before giving up. If it's really gone the server says so
// and we sign out exactly as before; if it was a blip we reload in place so the
// user keeps the page they were on. A read that can't reach the server proves
// nothing, so it is retried (see readSessionWithRetry) and we only sign out
// once every retry has failed. One recovery reload per tab, so a genuinely dead
// session can't loop.
//
// Never rejects. The caller has no catch, so an unhandled rejection would
// strand the user on <Loader /> forever — worse than the sign-out this is
// trying to avoid. `window.sessionStorage` itself throws a SecurityError where
// the browser blocks site data, and that case must fall through to 'signin'
// specifically: with no storage we can't record that the recovery reload was
// spent, so returning 'reload' would loop.
export async function resolveUnauthenticatedSession(): Promise<'reload' | 'signin'> {
  try {
    if (window.sessionStorage.getItem(SESSION_RECOVERY_FLAG)) {
      return 'signin';
    }
    if ((await readSessionWithRetry()) !== 'present') {
      return 'signin';
    }
    window.sessionStorage.setItem(SESSION_RECOVERY_FLAG, '1');
    return 'reload';
  } catch {
    return 'signin';
  }
}

function signInRedirect() {
  window.location.href = `/api/auth/signin?${new URLSearchParams({
    error: 'SessionRequired',
    callbackUrl: window.location.href,
  })}`;
}

export function withAuth(Component: React.ComponentType<any | string>) {
  const WithAuthComponent = (props: any) => {
    const handlingRef = useRef(false);

    const onUnauthenticated = useCallback(() => {
      if (handlingRef.current) return;
      handlingRef.current = true;

      resolveUnauthenticatedSession().then((action) => {
        if (action === 'reload') {
          window.location.reload();
        } else {
          signInRedirect();
        }
      });
    }, []);

    const { data, status } = useSession({ required: true, onUnauthenticated });

    useEffect(() => {
      if (status !== 'authenticated') return;
      try {
        window.sessionStorage.removeItem(SESSION_RECOVERY_FLAG);
      } catch {
        // Site data blocked — nothing was ever written, so nothing to clear.
        // Throwing here would take down the whole page on commit.
      }
    }, [status]);

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

// Dynamic-RBAC: may the current user READ the tenant's custom roles? Mirrors
// ee/customrole/service.go canReadCustomRoles — tenant-wide roles (including the
// read-only flavors, which isTenantAdmin() deliberately excludes) plus the
// `customroles:Read` grant. There is no Write counterpart: authoring and
// assigning roles is privilege administration and stays tenant-admin-only, so
// the customroles write actions are non-grantable (@lib/permissionCatalog).
//
// Use it to decide whether to CALL listCustomRoles() at all — the action 403s
// for everyone else, and 400s tenant-wide when the CUSTOM_ROLES feature is off.
export function canReadCustomRoles(): boolean {
  if (!isCustomRolesEnabled()) {
    return false;
  }
  return isTenantWideRole() || hasPermission('customroles', 'Read');
}

// Standard message for a disabled/greyed control the current user lacks the grant
// for. Names the exact `<module>:<Class>` permission so the user can ask an admin
// for precisely that grant (e.g. `tickets:Write`, `k8s:Read`). Use as the tooltip
// on any permission-disabled tab/button.
export function missingPermissionMessage(permission: string): string {
  return `You need the "${permission}" permission. Ask an admin to grant it.`;
}

// Modules backing the sections of the Admin page (/user-management): Users,
// Groups, Audits, Notifications, Integrations, Ownership, the EE Roles &
// Permissions tab, Tenant Settings, and AI & Tools (Agents/Tools & MCP/
// Functions/Budgets & Limits/Memory Policy/RCA Format share `ai`; Providers
// reuses `integrations`; Gateway reuses `llm`; Egress Filter is
// `egressfilter`). Kept in sync with baseFilters in
// app/src/pages/user-management/index.jsx.
// Cross-tenant super admin (full, not the read-only flavor). Kept distinct from
// isTenantWideRole() because destructive / write gates must not accept
// super_admin_readonly.
export function isSuperAdmin(): boolean {
  return userData?.isSuperAdmin === true;
}

// Advisory mirror of the backend SecurityContext.CanManage(module, class): a
// full tenant admin, a full super admin, OR a custom-role holder with the
// (module, class) grant.
// Use to show/hide write actions in tenant-config surfaces (the Admin page).
// tenant_admin_readonly and super_admin_readonly are intentionally excluded
// (isTenantAdmin() / isSuperAdmin() are false for them) so read-only admins never
// see write controls. The backend re-checks, so this is purely to avoid surfacing
// actions that would 403.
export function canManage(module: string, permissionClass: 'Read' | 'Write' | 'Execute'): boolean {
  return isTenantAdmin() || isSuperAdmin() || hasPermission(module, permissionClass);
}

// EDIT gate for the Tenant Settings Admin tab — mirrors CanManage("tenants","Write") in
// api-server/services/tenant/service.go, which gates all four writes the tab
// makes (tenant_attribute_upsert / _delete, tenant_update_name,
// featureflag_upsert). Note a `tenants:Write` grant still cannot flip the
// privileged keys carved out in tenant/privileged_config.go (entitlement_bypass,
// RBAC_K8S, …) — those stay tenant-admin/super-admin only, and the backend
// rejects them per-key rather than per-request.
//
// There's no separate canViewTenantSettings(): reachability is decided by
// hasAdminSurfaceAccess() now that this is a page tab, not an avatar-menu
// modal with its own independent view/edit split.
export function canEditTenantSettings(): boolean {
  return canManage('tenants', 'Write');
}

// 'ai', 'llm' and 'egressfilter' cover Admin → AI & Tools (docs/ia-consolidation-plan.md,
// PR 3) — the dynamic-RBAC modules its own sub-tabs gate on (see baseFilters
// in pages/user-management/index.jsx). Keep this list in sync with those
// `module` fields, same rule as every other Admin-page module here.
const ADMIN_SURFACE_MODULES = [
  'users',
  'usergroups',
  'audits',
  'notifications',
  'integrations',
  'ownership',
  'customroles',
  'roles',
  'tenants',
  'ai',
  'llm',
  'egressfilter',
];

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

// Tenant-flag requests still in flight. featureData fills only once a fetch lands, so callers
// firing together on mount (each `hasFeatureAccess`) would otherwise each send their own request.
const tenantFlagsInFlight: Record<string, Promise<any[]>> = Object.create(null);

function loadTenantFeatureFlags(tenantKey: string, fresh: boolean = false): Promise<any[]> {
  // queryGraphQL resolves (it does not reject) on network and GraphQL errors, so a failed request
  // has to be recognised here and thrown. Returning [] would be cached as "no flags enabled" for
  // the session, and every caller sharing this request would read it.
  const request = () =>
    queryGraphQL(LIST_TENANT_FEATURE_FLAGS, 'GetTenantFeatureFlags', {}).then((response) => {
      const list = response?.data?.data?.featureflags_list;
      if (!list || response?.data?.errors?.length) {
        throw new Error(response?.data?.errors?.[0]?.message || 'Failed to fetch tenant feature flags');
      }
      return list.rows || [];
    });
  // A refresh must reach the network, not join a request started before it.
  if (fresh) {
    return request();
  }
  if (!tenantFlagsInFlight[tenantKey]) {
    tenantFlagsInFlight[tenantKey] = request().finally(() => {
      delete tenantFlagsInFlight[tenantKey];
    });
  }
  return tenantFlagsInFlight[tenantKey];
}

export async function hasFeatureAccess(featureName: string): Promise<boolean> {
  const tenantKey = getTenantKey();
  if (!Object.hasOwn(featureData, tenantKey)) {
    try {
      featureData[tenantKey] = await loadTenantFeatureFlags(tenantKey);
    } catch (error) {
      console.log('failed to fetch feature flags-', error);
    }
  }
  // Unset when the fetch failed: answer "not enabled" now, and let the next call retry.
  const tenantFeatures: any[] = featureData[tenantKey] ?? [];
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
    featureData[tenantKey] = await loadTenantFeatureFlags(tenantKey, refresh);
  } catch (error) {
    // Not cached: a failure must not read as "no flags", and a failed refresh must not wipe
    // flags already loaded. Callers get what is known (or []), and the next call retries.
    console.log('Failed to fetch feature flags -', error);
    return featureData[tenantKey] ?? [];
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
