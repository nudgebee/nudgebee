import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/router';
import apiUser, {
  PREFERENCE_APP_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_INVESTIGATIONS_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_KG_TOUR_SEEN,
  PREFERENCE_OPTIMIZE_TOUR_SEEN,
  PREFERENCE_TICKETS_TOUR_SEEN,
  PREFERENCE_CLOUD_TOUR_SEEN,
} from '@api1/user';
import { fetchFeatureFlagsForTenant } from '@lib/auth';
import { useTour, TOURS, brandText, canAccessTour } from '@components/common/tour';
import { useBrandingConfig } from '@hooks/useTenantBranding';
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
 * for users who can't complete them (`canAccessTour`), so a feature-gated or
 * role-gated guide is never auto-offered to someone who can't run it; we warm the
 * tenant flag cache first so those evaluate correctly.
 *
 * Sequencing: we hold off until the first-login app overview has been seen
 * (`app_tour_seen`), so a brand-new user who deep-links straight into a view gets
 * the app overview first instead of two welcome cards stacked at once.
 */
export const SECTION_TOURS = [
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
        'This tab holds every root-cause analysis — the ones {brand} runs automatically when incidents fire, and the ones you start yourself. Here’s a quick tour.',
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
    landingFragments: ['', 'all-events'],
    welcome: {
      title: 'Welcome to Troubleshoot',
      description:
        'This is where you root-cause what’s happening across your clusters — events, investigations, and your live service map. Here’s a quick tour so you know where to look.',
    },
  },
  {
    path: '/optimise',
    tourId: 'optimize-overview',
    pref: PREFERENCE_OPTIMIZE_TOUR_SEEN,
    // Optimize lands on Summary; the fragment is absent until the tab bar sets it.
    landingFragments: ['', 'summary'],
    welcome: {
      title: 'Welcome to Optimize',
      description:
        'This is where you find money — right-sizing, config, and spend recommendations across every account, plus the automations that apply them for you. Here’s a quick tour.',
    },
  },
  {
    path: '/tickets',
    tourId: 'tickets-overview',
    pref: PREFERENCE_TICKETS_TOUR_SEEN,
    landingFragments: ['', 'tickets'],
    welcome: {
      title: 'Welcome to Tickets',
      description:
        'Every ticket raised from {brand} lives here, in sync with Jira, ServiceNow, or wherever it came from. Here’s a quick tour of finding the one you need.',
    },
  },
  {
    // /cloud-account is a redirector, so by the time a user is "on Cloud" the
    // route is the details page — match that, not the redirector.
    path: '/cloud-account/details/[CloudAccountDetails]',
    tourId: 'cloud-overview',
    pref: PREFERENCE_CLOUD_TOUR_SEEN,
    landingFragments: ['', 'summary'],
    welcome: {
      title: 'Welcome to Cloud',
      description:
        'Everything about a connected cloud account in one place — spend, alarms, events, and the services running in it. Here’s a quick tour.',
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

export function matchesSection(section, pathname, fragment) {
  if (section.path !== pathname) {
    return false;
  }
  if (section.hash) {
    return fragment === section.hash;
  }
  // Default entry: the section's landing view. Which fragments count as "the
  // landing view" is per-section — a page may render with no hash and then set
  // its default one (Troubleshoot → all-events, Optimize → summary) — so each
  // entry declares its own rather than sharing one hardcoded list.
  return (section.landingFragments ?? ['']).includes(fragment);
}

const SectionFirstVisitTour = () => {
  const router = useRouter();
  const { start, isActive } = useTour();
  // Subscribed for the re-render, not the value: brandText() reads the brand
  // name from a non-reactive module cache, and the branding fetch may resolve
  // after this dialog has already mounted. Without this, a slow
  // /api/public/app_config would leave the fallback name on screen.
  useBrandingConfig();
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
    <TourWelcomeDialog
      open
      title={brandText(pending.welcome.title)}
      description={brandText(pending.welcome.description)}
      onStart={handleStart}
      onSnooze={handleSnooze}
    />
  );
};

export default SectionFirstVisitTour;
