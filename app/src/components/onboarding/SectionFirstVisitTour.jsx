import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import apiUser, {
  PREFERENCE_APP_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_INVESTIGATIONS_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_KG_TOUR_SEEN,
} from '@api1/user';
import { fetchFeatureFlagsForTenant } from '@lib/auth';
import { useTour, TOURS, canAccessTour } from '@components/common/tour';
import TourWelcomeDialog from './TourWelcomeDialog';

/**
 * Section-scoped "first visit" tours. The first time a user lands on a section's
 * view we offer that view's guided tour once — the same welcome-card → "Let's
 * go" flow as the first-login app overview ([[FirstLoginTour]]), but keyed to the
 * route (and, for tabbed pages like Troubleshoot, the top-level hash fragment)
 * with its own localStorage flag.
 *
 * Rendered once inside the persistent sidebar layout, so it survives client-side
 * navigation and reacts to route/hash changes. Renders nothing until a view
 * matches. Add a view by adding an entry to `SECTION_TOURS` plus a preference key
 * in `@api1/user` — no other wiring. Guides that gate on role/feature are skipped
 * for users who can't complete them (`canAccessTour`); the KG guide is
 * feature-gated, so we warm the tenant flag cache before evaluating.
 *
 * Sequencing: we hold off until the first-login app overview has been seen
 * (`app_tour_seen`), so a brand-new user who deep-links straight into a view gets
 * the app overview first instead of two welcome cards stacked at once.
 */
const SECTION_TOURS = [
  // Hash-scoped entries first so `.find` prefers a sub-view over the default
  // overview when a user deep-links straight into it (e.g. /troubleshoot#kg).
  {
    path: '/troubleshoot',
    hash: 'investigations',
    tourId: 'investigations',
    pref: PREFERENCE_TROUBLESHOOT_INVESTIGATIONS_TOUR_SEEN,
    welcome: {
      title: 'Investigations',
      description:
        'This tab holds every root-cause analysis — the ones Nudgebee runs automatically when incidents fire, and the ones you start yourself. Here’s a quick tour.',
    },
  },
  {
    path: '/troubleshoot',
    hash: 'kg',
    tourId: 'knowledge-graph',
    pref: PREFERENCE_TROUBLESHOOT_KG_TOUR_SEEN,
    welcome: {
      title: 'Knowledge Graph',
      description:
        'This is your live service map — every service, workload, and cloud resource, and the real dependencies between them. Here’s a quick tour of how to read and drive it.',
    },
  },
  {
    // Default Troubleshoot view (All Events). No `hash` → matches the landing
    // view (see matchesSection).
    path: '/troubleshoot',
    tourId: 'troubleshoot-overview',
    pref: PREFERENCE_TROUBLESHOOT_TOUR_SEEN,
    welcome: {
      title: 'Welcome to Troubleshoot',
      description:
        'This is where you root-cause what’s happening across your clusters — events, investigations, and your live service map. Here’s a quick tour so you know where to look.',
    },
  },
];

/** The top-level hash fragment of a route, e.g. '/troubleshoot#kg/x' → 'kg'. */
function topFragment(asPath) {
  const hash = asPath.split('#')[1];
  if (!hash) {
    return '';
  }
  return decodeURIComponent(hash).split('/')[0];
}

function matchesSection(section, pathname, fragment) {
  if (section.path !== pathname) {
    return false;
  }
  if (section.hash) {
    return fragment === section.hash;
  }
  // Default entry: the section's landing view — no sub-view hash, or All Events.
  return fragment === '' || fragment === 'all-events';
}

const SectionFirstVisitTour = () => {
  const router = useRouter();
  const { start, isActive } = useTour();
  const [pending, setPending] = useState(null);
  const [flagsReady, setFlagsReady] = useState(false);
  // Offer each view at most once per session, so Snooze (which leaves the flag
  // unset) doesn't re-prompt every time the user returns to it.
  const offeredRef = useRef(new Set());

  const pathname = router.pathname;
  const fragment = topFragment(router.asPath);

  // Warm the tenant feature-flag cache so feature-gated section tours (Knowledge
  // Graph) evaluate correctly. Cheap — a no-op network-wise if another view
  // already warmed it. `flagsReady` re-runs the effect below once it resolves.
  useEffect(() => {
    let cancelled = false;
    fetchFeatureFlagsForTenant().finally(() => {
      if (!cancelled) {
        setFlagsReady(true);
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    // A tour is running (handleStart already cleared any pending) — leave it be.
    if (isActive) {
      return;
    }
    const section = SECTION_TOURS.find((s) => matchesSection(s, pathname, fragment));

    // Already prompting for the current view — keep the dialog up.
    if (pending && section && pending.tourId === section.tourId) {
      return;
    }
    // Navigated away from any tour-enabled view, or to one already offered this
    // session — dismiss a now-stale welcome (e.g. a browser back/forward moved
    // the route out from under an open dialog) and don't (re-)offer.
    if (!section || offeredRef.current.has(section.tourId)) {
      if (pending) {
        setPending(null);
      }
      return;
    }

    const tour = TOURS[section.tourId];
    if (!tour || !canAccessTour(tour)) {
      if (pending) {
        setPending(null);
      }
      return;
    }

    let prefs = {};
    try {
      prefs = apiUser.getUserPreferences() || {};
    } catch {
      prefs = {};
    }
    // Sequence after the first-login app overview (see docstring), and skip a
    // view whose tour the user has already seen.
    if (!prefs[PREFERENCE_APP_TOUR_SEEN] || prefs[section.pref]) {
      if (pending) {
        setPending(null);
      }
      return;
    }

    offeredRef.current.add(section.tourId);
    setPending(section);
  }, [pathname, fragment, isActive, pending, flagsReady]);

  const handleStart = () => {
    if (!pending) {
      return;
    }
    const section = pending;
    setPending(null);
    try {
      apiUser.storeUserPreferences(section.pref, true);
    } catch {
      // localStorage unavailable — still run the tour this session.
    }
    start(section.tourId);
  };

  const handleSnooze = () => {
    // Leave the flag unset → the welcome returns on the next session.
    setPending(null);
  };

  if (!pending) {
    return null;
  }

  return (
    <TourWelcomeDialog open title={pending.welcome.title} description={pending.welcome.description} onStart={handleStart} onSnooze={handleSnooze} />
  );
};

export default SectionFirstVisitTour;
