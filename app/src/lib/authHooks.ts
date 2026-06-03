// Auth-flow extension hooks. Defaults live here; deployments that need
// custom behavior (e.g. cross-tenant super-admin lookup from a database,
// license-bound onboarding policies) can replace any hook via the
// register*() functions at module-init time.
//
// Hooks intentionally manipulate plain shapes (token, role list, decision
// objects) rather than encapsulating role-specific business logic. The
// data fields are part of the shared type system; only the production
// rules are pluggable.

import type { ComponentType, ReactNode } from 'react';
import type { Account, User } from 'next-auth';

// Avoid importing NudgebeeSession from `[...nextauth].ts` directly — that
// would create a circular dependency between this shared hook module and a
// NextAuth route module. Hook consumers should narrow the untyped shape.
type SessionLike = { tier?: string } | null | undefined;

type TokenEnricher = (token: Record<string, unknown>, userId: string) => Promise<void>;
type SessionEnricher = (session: Record<string, unknown>, token: Record<string, unknown>) => void | Promise<void>;
type RoleElevator = (userRoles: string[], jwt: Record<string, unknown>) => string[];

// Resolves a user against a licensed single-tenant deployment (typically
// the LDAP / Teleport flow). Default throws — callers reach this only
// when an extension was expected to be registered (gated upstream by
// `license.tenantId !== ''`).
type LicensedTenantUserResolver = (input: { email: string; provider: string; providerId: string; name: string; groups: string[] }) => Promise<User>;

// Hook for returning-user OAuth signin. Lets a registered extension
// perform post-signin work (e.g. backfilling allowed_domains for the
// license tenant). Default no-op.
type OnReturningOAuthSignIn = (user: User) => Promise<void>;

// Hook for unknown-user OAuth signin. Default rejects (no automatic
// account creation in the OSS singleton-tenant flow). A registered
// extension can validate and onboard the user, returning true on success.
type OnUnknownOAuthSignIn = (input: { user: User; account: Account | null }) => Promise<boolean>;

// Hook registry lives on globalThis so it survives Next.js's dual module
// graph between the Node-runtime (`instrumentation.ts`) and page-route
// contexts. With module-scoped `let`s, `instrumentation.ts` → `@ee/init-server`
// → `superAdmin.ts:registerTokenEnricher(...)` mutated *one* instance of
// this file while `[...nextauth].ts:enrichAuthToken(...)` read from *another*
// — registrations were ghost-mutated and the EE behavior silently no-op'd.
// Same pattern as `globalThis.__nbBypassGraphQLAsServer` from
// `instrumentation.ts`.
type AuthHooksRegistry = {
  enricher: TokenEnricher;
  sessionEnricher: SessionEnricher;
  elevator: RoleElevator;
  licensedTenantUserResolver: LicensedTenantUserResolver | null;
  onReturningOAuthSignIn: OnReturningOAuthSignIn;
  onUnknownOAuthSignIn: OnUnknownOAuthSignIn;
  headerMenuExtras: HeaderMenuExtra[];
  userManagementFilters: UserManagementFilter[];
};

const _g = globalThis as unknown as { __nbAuthHooks?: AuthHooksRegistry };
if (!_g.__nbAuthHooks) {
  _g.__nbAuthHooks = {
    enricher: async () => {},
    sessionEnricher: () => {},
    elevator: (roles) => roles,
    licensedTenantUserResolver: null,
    onReturningOAuthSignIn: async () => {},
    onUnknownOAuthSignIn: async () => false,
    headerMenuExtras: [],
    userManagementFilters: [],
  };
}
const _hooks = _g.__nbAuthHooks!;

export function registerTokenEnricher(h: TokenEnricher): void {
  _hooks.enricher = h;
}

export function registerSessionEnricher(h: SessionEnricher): void {
  _hooks.sessionEnricher = h;
}

export function registerRoleElevator(h: RoleElevator): void {
  _hooks.elevator = h;
}

export function registerLicensedTenantUserResolver(h: LicensedTenantUserResolver): void {
  _hooks.licensedTenantUserResolver = h;
}

export function registerOnReturningOAuthSignIn(h: OnReturningOAuthSignIn): void {
  _hooks.onReturningOAuthSignIn = h;
}

export function registerOnUnknownOAuthSignIn(h: OnUnknownOAuthSignIn): void {
  _hooks.onUnknownOAuthSignIn = h;
}

/**
 * Augments a NextAuth JWT with deployment-specific claims after the user
 * is resolved. Default is a no-op; a registered enricher can look up
 * additional role data and set token.isSuperAdmin / isSuperAdminReadonly.
 */
export async function enrichAuthToken(token: Record<string, unknown>, userId: string): Promise<void> {
  await _hooks.enricher(token, userId);
}

/**
 * Augments the session object (passed to the client via NextAuth) with
 * deployment-specific attributes. Runs once per session callback, after the
 * core OSS fields are set. Default is a no-op; an enricher registered in a
 * bundled extension can surface attributes that extension-bundled components
 * consume at render time. Keeping the writes behind this hook — rather than
 * inlining `process.env.X` reads in `[...nextauth].ts` — keeps OSS
 * extractions free of dead session fields.
 */
export async function enrichSession(session: Record<string, unknown>, token: Record<string, unknown>): Promise<void> {
  await _hooks.sessionEnricher(session, token);
}

/**
 * Expands a request's allowed-role set based on JWT claims. Used by
 * rpcGateway when forwarding RPC actions.
 */
export function elevateRoles(userRoles: string[], jwt: Record<string, unknown>): string[] {
  return _hooks.elevator(userRoles, jwt);
}

/**
 * Resolves a user against a licensed single-tenant deployment. Throws when
 * called without a registered resolver — callers must gate on the operational
 * precondition (e.g. `license.tenantId !== ''`) before invoking.
 */
export async function resolveLicensedTenantUser(input: Parameters<LicensedTenantUserResolver>[0]): Promise<User> {
  if (!_hooks.licensedTenantUserResolver) {
    throw new Error('No licensed-tenant user resolver registered — this auth path requires the matching extension bundle');
  }
  return _hooks.licensedTenantUserResolver(input);
}

/**
 * Called after a known OAuth user signs back in successfully. Default no-op.
 */
export async function onReturningOAuthSignIn(user: User): Promise<void> {
  await _hooks.onReturningOAuthSignIn(user);
}

/**
 * Called when an unknown OAuth user attempts to sign in. Default rejects
 * (returns false). Registered extensions decide whether to onboard.
 */
export async function onUnknownOAuthSignIn(input: { user: User; account: Account | null }): Promise<boolean> {
  return _hooks.onUnknownOAuthSignIn(input);
}

// ---------------------------------------------------------------------------
// UI extension points — lists of items that surfaces (header user menu,
// user-management filter sidebar) build from. Each registered item provides
// its own self-render (or self-decision) so the consuming surface code stays
// neutral about what's registered or why.
// ---------------------------------------------------------------------------

export type HeaderMenuExtraItem = {
  label: ReactNode;
  icon?: ReactNode;
  onSelect: () => void;
};

export type HeaderMenuExtra = {
  id: string;
  // Returns a DropdownMenu item descriptor for the help menu, or `null` to
  // hide the entry. Receives the session so the registration can decide
  // whether to surface itself (tier/feature checks live in the registering
  // bundle). `close` is the menu's own close handler — call it before
  // performing any deferred side-effect (window.open, $chatwoot.toggle, …)
  // so the menu unmounts first.
  getItem: (session: SessionLike, close: () => void) => HeaderMenuExtraItem | null;
};

export type UserManagementFilter = {
  // Display name + URL-hash fragment (also serves as the routing key).
  name: string;
  fragment: string;
  // Asset reference for the filter icon (passed through to AnchorComponent).
  icon?: unknown;
  betaIcon?: boolean;
  disabled?: boolean;
  // Body component rendered when this filter is selected.
  Body: ComponentType<{ session: SessionLike }>;
  // Optional show/hide gate. Defaults to "always show". Tier checks belong
  // in this predicate so the literal stays bundled with the registration,
  // not in the consuming page.
  shouldShow?: (session: SessionLike) => boolean;
};

export function registerHeaderMenuExtra(item: HeaderMenuExtra): void {
  if (_hooks.headerMenuExtras.some((e) => e.id === item.id)) return;
  _hooks.headerMenuExtras.push(item);
}

export function headerMenuExtras(): HeaderMenuExtra[] {
  return _hooks.headerMenuExtras;
}

export function registerUserManagementFilter(item: UserManagementFilter): void {
  if (_hooks.userManagementFilters.some((e) => e.fragment === item.fragment)) return;
  _hooks.userManagementFilters.push(item);
}

export function userManagementFilters(session: SessionLike): UserManagementFilter[] {
  return _hooks.userManagementFilters.filter((f) => f.shouldShow?.(session) ?? true);
}
