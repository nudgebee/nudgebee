import * as React from 'react';
import { getAccountGuard, type AccountGuardOptions } from '@lib/accountGuard';

// AccountGuard — OSS-neutral seam for the ENTERPRISE cross-tenant account
// guard. `withAccountGuard` wraps a page so that, in EE builds, a deep link
// carrying an `account_id` from another tenant renders a red "not in your
// tenant" banner instead of the page (see app/src/ee/components/AccountGuard).
//
// In OSS (single-tenant) builds the EE implementation isn't registered, so this
// is a pass-through: the page renders exactly as if it were never wrapped. That
// keeps the EE multi-tenant UX — and its accounts-list lookup — out of OSS
// entirely, while letting the page files import `@shared/AccountGuard`
// unconditionally (no `@ee/*` import, so the OSS/EE boundary check stays green).
//
// Pair it with a page via `withAccountGuard` on the export line.

export type { AccountGuardOptions };

export function withAccountGuard<P extends object>(Component: React.ComponentType<P>, options: AccountGuardOptions = {}) {
  const Guarded = (props: P) => {
    // Registered once at app load from `@ee/init` (before any page renders);
    // null in OSS. `Guarded` itself calls no hooks, so branching here is safe.
    const Impl = getAccountGuard();
    if (!Impl) {
      return <Component {...props} />;
    }
    return <Impl Component={Component} options={options} componentProps={props} />;
  };

  Guarded.displayName = `withAccountGuard(${Component.displayName || Component.name || 'Component'})`;
  return Guarded;
}

export default withAccountGuard;
