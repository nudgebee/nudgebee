// Account-guard extension seam (ENTERPRISE feature).
//
// The cross-tenant account guard — a red banner shown when a deep-linked
// `account_id` belongs to a tenant the user isn't signed into — only makes
// sense in a multi-tenant deployment. OSS builds are single-tenant by design,
// so no account_id can ever be "foreign": the EE bundle is stripped, nothing
// registers here, and `withAccountGuard` (@shared/AccountGuard) is a plain
// pass-through that renders the page unchanged.
//
// This mirrors `@lib/slots`, but for a behavioral page wrapper rather than a
// layout-position component. Living in @lib (OSS) keeps it on the allowed side
// of the OSS/EE boundary: both the OSS seam and the EE implementation import
// it, and no OSS file ever imports `@ee/*` (see scripts/check-oss-boundary.sh).
import type { ComponentType } from 'react';

export interface AccountGuardOptions {
  /**
   * When true, a cross-tenant account hides the page content (the red banner is
   * shown in its place) — use for pages that would otherwise fetch/render
   * another tenant's data. When false, the banner sits above the page and the
   * page still renders (use when account_id is an optional filter).
   */
  hideContent?: boolean;
  /** Resolve the account id from router.query. Defaults to query.accountId. */
  getAccountId?: (query: Record<string, any>) => string | string[] | undefined;
}

/**
 * EE-supplied implementation. Receives the wrapped page component, the guard
 * options, and the page's own props, and renders either the page or the
 * cross-tenant banner. Registered once at app load from `@ee/init`.
 */
export type AccountGuardImpl = ComponentType<{
  Component: ComponentType<any>;
  options: AccountGuardOptions;
  componentProps: any;
}>;

// Module-scoped, like `@lib/slots` — the guard is client-only, so unlike the
// server-side authHooks registry it needs no globalThis dual-graph handling.
let impl: AccountGuardImpl | null = null;

export function registerAccountGuard(component: AccountGuardImpl): void {
  impl = component;
}

export function getAccountGuard(): AccountGuardImpl | null {
  return impl;
}

/**
 * True only when the EE bundle has registered a guard implementation. OSS code
 * (e.g. ClusterDropDown) uses this to keep multi-tenant-only behavior inert in
 * single-tenant builds.
 */
export function isAccountGuardActive(): boolean {
  return impl !== null;
}
