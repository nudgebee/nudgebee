import { useEffect, useRef, useState } from 'react';
import apiUser, { PREFERENCE_APP_TOUR_SEEN, PREFERENCE_APP_TOUR_SNOOZED_UNTIL } from '@api1/user';
import { useTour, TOURS, brandText } from '@components/common/tour';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import TourWelcomeDialog from './TourWelcomeDialog';

const APP_TOUR_ID = 'app-overview';

/**
 * On a user's first visit, shows a welcome card; "Let's go" launches the
 * "app-overview" sidebar walkthrough, "Snooze" defers it to the next visit.
 * Render it only where the sidebar is present (the tour anchors to the sidebar
 * nav buttons).
 *
 * "First time" is tracked in localStorage (PREFERENCE_APP_TOUR_SEEN), so it's
 * per-browser, not per-user across devices. True cross-device first-login would
 * need a server-side flag on the user record; localStorage is the pragmatic v1.
 *
 * Snooze defers the welcome for 24h (PREFERENCE_APP_TOUR_SNOOZED_UNTIL holds
 * the epoch-ms expiry) without setting the seen flag, so it reappears on the
 * first visit after the window lapses. Starting the tour sets the seen flag,
 * so it won't auto-show again.
 */
const SNOOZE_MS = 24 * 60 * 60 * 1000;

const FirstLoginTour = () => {
  const { start } = useTour();
  const [welcomeOpen, setWelcomeOpen] = useState(false);
  const firedRef = useRef(false);
  // Subscribed for the re-render, not the value: brandText() reads the brand
  // name from a non-reactive module cache, and the branding fetch may resolve
  // after this dialog has already mounted. Without this, a slow
  // /api/public/app_config would leave the fallback name on screen.
  useBrandingConfig();

  const tour = TOURS[APP_TOUR_ID];

  useEffect(() => {
    // Guard against React 18 StrictMode double-invoke and layout remounts.
    if (firedRef.current) {
      return;
    }
    firedRef.current = true;

    let seen = false;
    let snoozedUntil = 0;
    try {
      const prefs = apiUser.getUserPreferences() || {};
      seen = Boolean(prefs[PREFERENCE_APP_TOUR_SEEN]);
      snoozedUntil = Number(prefs[PREFERENCE_APP_TOUR_SNOOZED_UNTIL]) || 0;
    } catch {
      seen = false;
    }
    if (!seen && Date.now() >= snoozedUntil) {
      setWelcomeOpen(true);
    }
  }, []);

  const handleStart = () => {
    setWelcomeOpen(false);
    try {
      apiUser.storeUserPreferences(PREFERENCE_APP_TOUR_SEEN, true);
    } catch {
      // localStorage unavailable — still run the tour this session.
    }
    start(APP_TOUR_ID);
  };

  const handleSnooze = () => {
    setWelcomeOpen(false);
    try {
      apiUser.storeUserPreferences(PREFERENCE_APP_TOUR_SNOOZED_UNTIL, Date.now() + SNOOZE_MS);
    } catch {
      // localStorage unavailable — falls back to the old next-visit behavior.
    }
  };

  if (!tour?.welcome) {
    return null;
  }

  return (
    <TourWelcomeDialog
      open={welcomeOpen}
      title={brandText(tour.welcome.title)}
      description={brandText(tour.welcome.description)}
      totalSteps={tour.steps.length + 1}
      onStart={handleStart}
      onSnooze={handleSnooze}
    />
  );
};

export default FirstLoginTour;
