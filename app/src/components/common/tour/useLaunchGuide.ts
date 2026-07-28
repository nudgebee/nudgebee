import * as React from 'react';
import { useRouter } from 'next/router';
import { useTour } from './TourProvider';
import { TOURS } from './tours';

/** Just the pathname of a route (drop query + hash). */
function basePath(route: string): string {
  return route.split('?')[0].split('#')[0];
}

/** The query string of a route (between `?` and `#`), or '' if none. */
function queryString(route: string): string {
  return route.split('#')[0].split('?')[1] || '';
}

/**
 * Returns a `launch(tourId)` function that starts a tour, navigating to the
 * tour's `route` first when we're not already there. This is what lets a guide
 * be launched from anywhere (a central catalog, a header menu) — the tour's
 * first step waits for its anchor to mount after navigation (TourProvider is
 * global, so `start()` survives the route change).
 */
export function useLaunchGuide(): (tourId: string) => void {
  const { start } = useTour();
  const router = useRouter();

  return React.useCallback(
    (tourId: string) => {
      const tour = TOURS[tourId];
      if (!tour) {
        return;
      }
      const target = tour.route;
      if (!target) {
        start(tourId);
        return;
      }

      // Skip navigation only when we're already on the *exact* target path AND
      // every query param the target requires matches. A prefix / `startsWith`
      // match would wrongly skip when only the pathname lines up — e.g. the same
      // `/accounts/account-form` page on a different `cloudProvider` tab, or a
      // different `#hash` tab — leaving the user where the tour's anchors don't
      // exist.
      const onTargetPath = basePath(router.asPath) === basePath(target);
      const wanted = new URLSearchParams(queryString(target));
      const current = new URLSearchParams(queryString(router.asPath));
      const hasAllParams = Array.from(wanted).every(([key, value]) => current.get(key) === value);

      if (onTargetPath && hasAllParams) {
        start(tourId);
      } else {
        void router.push(target).then(() => start(tourId));
      }
    },
    [router, start]
  );
}
