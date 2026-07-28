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
import { hasWriteAccess, hasFeatureAccessCached } from '@lib/auth';
import type { TourDef } from './tours';

export function canAccessTour(tour: TourDef): boolean {
  if (tour.requires === 'write' && !hasWriteAccess()) {
    return false;
  }
  // Feature-gated guides need their flag enabled. Reads the cached flags, so the
  // caller must have warmed them (GuidesMenu does on open); until then the guide
  // stays hidden rather than dead-ending.
  if (tour.requiresFeature && !hasFeatureAccessCached(tour.requiresFeature)) {
    return false;
  }
  return true;
}
