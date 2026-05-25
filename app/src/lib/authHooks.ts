// Auth-flow extension hooks. Defaults live here; deployments that need
// custom behavior (e.g. cross-tenant super-admin lookup from a database,
// license-bound onboarding policies) can replace any hook via the
// register*() functions at module-init time.
//
// Hooks intentionally manipulate plain shapes (token, role list, decision
// objects) rather than encapsulating role-specific business logic. The
// data fields are part of the shared type system; only the production
// rules are pluggable.

import type { ComponentType } from 'react';
import type { Account, User } from 'next-auth';

// Avoid importing NudgebeeSession from `[...nextauth].ts` directly — that
// would create a circular dependency between this shared hook module and a
// NextAuth route module. Hook consumers should narrow the untyped shape.
type SessionLike = { tier?: string } | null | undefined;

type TokenEnricher = (token: Record<string, unknown>, userId: string) => Promise<void>;
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

let _enricher: TokenEnricher = async () => {};
let _elevator: RoleElevator = (roles) => roles;
let _licensedTenantUserResolver: LicensedTenantUserResolver | null = null;
let _onReturningOAuthSignIn: OnReturningOAuthSignIn = async () => {};
let _onUnknownOAuthSignIn: OnUnknownOAuthSignIn = async () => false;

export function registerTokenEnricher(h: TokenEnricher): void {
  _enricher = h;
}

export function registerRoleElevator(h: RoleElevator): void {
  _elevator = h;
}

export function registerLicensedTenantUserResolver(h: LicensedTenantUserResolver): void {
  _licensedTenantUserResolver = h;
}

export function registerOnReturningOAuthSignIn(h: OnReturningOAuthSignIn): void {
  _onReturningOAuthSignIn = h;
}

export function registerOnUnknownOAuthSignIn(h: OnUnknownOAuthSignIn): void {
  _onUnknownOAuthSignIn = h;
}

/**
 * Augments a NextAuth JWT with deployment-specific claims after the user
 * is resolved. Default is a no-op; a registered enricher can look up
 * additional role data and set token.isSuperAdmin / isSuperAdminReadonly.
 */
export async function enrichAuthToken(token: Record<string, unknown>, userId: string): Promise<void> {
  await _enricher(token, userId);
}

/**
 * Expands a request's allowed-role set based on JWT claims. Used by
 * rpcGateway when forwarding Hasura actions.
 */
export function elevateRoles(userRoles: string[], jwt: Record<string, unknown>): string[] {
  return _elevator(userRoles, jwt);
}

/**
 * Resolves a user against a licensed single-tenant deployment. Throws when
 * called without a registered resolver — callers must gate on the operational
 * precondition (e.g. `license.tenantId !== ''`) before invoking.
 */
export async function resolveLicensedTenantUser(input: Parameters<LicensedTenantUserResolver>[0]): Promise<User> {
  if (!_licensedTenantUserResolver) {
    throw new Error('No licensed-tenant user resolver registered — this auth path requires the matching extension bundle');
  }
  return _licensedTenantUserResolver(input);
}

/**
 * Called after a known OAuth user signs back in successfully. Default no-op.
 */
export async function onReturningOAuthSignIn(user: User): Promise<void> {
  await _onReturningOAuthSignIn(user);
}

/**
 * Called when an unknown OAuth user attempts to sign in. Default rejects
 * (returns false). Registered extensions decide whether to onboard.
 */
export async function onUnknownOAuthSignIn(input: { user: User; account: Account | null }): Promise<boolean> {
  return _onUnknownOAuthSignIn(input);
}

// ---------------------------------------------------------------------------
// UI extension points — lists of items that surfaces (header user menu,
// user-management filter sidebar) build from. Each registered item provides
// its own self-render (or self-decision) so the consuming surface code stays
// neutral about what's registered or why.
// ---------------------------------------------------------------------------

export type HeaderMenuExtra = {
  id: string;
  // Renders the actual MenuItem. Receives the session so the registration can
  // decide whether to render at all (return null) — keeps tier/feature checks
  // inside the registering bundle.
  Component: ComponentType<{ session: SessionLike; onClose: () => void }>;
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

const _headerMenuExtras: HeaderMenuExtra[] = [];
const _userManagementFilters: UserManagementFilter[] = [];

export function registerHeaderMenuExtra(item: HeaderMenuExtra): void {
  if (_headerMenuExtras.some((e) => e.id === item.id)) return;
  _headerMenuExtras.push(item);
}

export function headerMenuExtras(): HeaderMenuExtra[] {
  return _headerMenuExtras;
}

export function registerUserManagementFilter(item: UserManagementFilter): void {
  if (_userManagementFilters.some((e) => e.fragment === item.fragment)) return;
  _userManagementFilters.push(item);
}

export function userManagementFilters(session: SessionLike): UserManagementFilter[] {
  return _userManagementFilters.filter((f) => f.shouldShow?.(session) ?? true);
}
