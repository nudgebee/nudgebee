import { useEffect, useRef, useState } from 'react';
import apiUser, { PREFERENCE_APP_TOUR_SEEN } from '@api1/user';
import { useTour, TOURS } from '@components/common/tour';
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
 * Snooze leaves the flag unset, so the welcome reappears next visit. Starting
 * the tour sets the flag, so it won't auto-show again.
 */
const FirstLoginTour = () => {
  const { start } = useTour();
  const [welcomeOpen, setWelcomeOpen] = useState(false);
  const firedRef = useRef(false);

  const tour = TOURS[APP_TOUR_ID];

  useEffect(() => {
    // Guard against React 18 StrictMode double-invoke and layout remounts.
    if (firedRef.current) {
      return;
    }
    firedRef.current = true;

    let seen = false;
    try {
      seen = Boolean(apiUser.getUserPreferences()?.[PREFERENCE_APP_TOUR_SEEN]);
    } catch {
      seen = false;
    }
    if (!seen) {
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
    // Leave the flag unset → welcome reappears on the next visit.
    setWelcomeOpen(false);
  };

  if (!tour?.welcome) {
    return null;
  }

  return (
    <TourWelcomeDialog
      open={welcomeOpen}
      title={tour.welcome.title}
      description={tour.welcome.description}
      totalSteps={tour.steps.length + 1}
      onStart={handleStart}
      onSnooze={handleSnooze}
    />
  );
};

export default FirstLoginTour;
