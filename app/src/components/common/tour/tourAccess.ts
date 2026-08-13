/**
 * Resolves a guide's declared `requires` capability against the current user's
 * live permissions, so a guide is only surfaced to users who can complete it.
 *
 * Kept out of `tours.ts` (which stays a pure-data registry) and out of the
 * render path of the launchers so the same rule backs both the central Guides
 * catalog and the contextual `TourLauncher`. Reads the synchronous session
 * accessors in `@lib/auth` — the same ones the gated action buttons use — so a
 * guide's visibility matches its target button exactly.
 */
import { hasWriteAccess, hasFeatureAccessCached, isUiFeatureEnabled, getUserSession } from '@lib/auth';
import type { TourDef } from './tours';

/**
 * True when the user can write to at least one account — the resolution for
 * `requires: 'account-write'`, mirroring the `hasWriteAccess(accountId)` gate on
 * account-scoped buttons like "Create Automation", which account_admins pass.
 *
 * Deliberately an "any account" test. The Guides catalog is global and has no
 * account context, so it can't know which account a guide will land on. The
 * trade-off: an account_admin of account X browsing account Y still sees the
 * guide, and the tour ends cleanly when the gated button doesn't mount. That's
 * the lesser evil — a bare `hasWriteAccess()` hides the guide from every
 * account_admin, including on the accounts they *can* write to.
 *
 * Delegates to `hasWriteAccess(id)` per account instead of reading roles here,
 * so role semantics (e.g. tenant_admin_readonly short-circuiting to false) stay
 * defined in exactly one place.
 */
function hasWriteAccessToAnyAccount(): boolean {
  if (hasWriteAccess()) {
    return true;
  }
  const accountIds: string[] = getUserSession()?.accountIds ?? [];
  return accountIds.some((accountId) => hasWriteAccess(accountId));
}

export function canAccessTour(tour: TourDef): boolean {
  if (tour.requires === 'write' && !hasWriteAccess()) {
    return false;
  }
  if (tour.requires === 'account-write' && !hasWriteAccessToAnyAccount()) {
    return false;
  }
  // Feature-gated guides need their flag enabled. Reads the cached flags, so the
  // caller must have warmed them (GuidesMenu does on open); until then the guide
  // stays hidden rather than dead-ending.
  if (tour.requiresFeature && !hasFeatureAccessCached(tour.requiresFeature)) {
    return false;
  }
  // Deployment-level UI toggles (UI_ENABLE_*). Read straight off the session, so
  // no warming is needed — unlike the tenant flags above.
  if (tour.requiresUiFeature && !isUiFeatureEnabled(tour.requiresUiFeature)) {
    return false;
  }
  return true;
}
