import * as React from 'react';
import { useRouter } from 'next/router';

// Was pushing during render, unguarded — every visit to `/` painted `/home`
// for a frame before the setup gate (also evaluating at that instant) could
// object and correct it to `/setup`. The destination was always right; the
// paint was not. Routing belongs in an effect, and the target should be the
// gate's own default rather than a second opinion this page has to keep in
// sync with it.
//
// Default landing page is Nubi (/ask-nudgebee), not the dashboard (/home) —
// /home is still a real page, just no longer where a fresh sign-in lands.
function RedirectPage(): null {
  const router = useRouter();
  React.useEffect(() => {
    router.replace('/ask-nudgebee');
  }, [router]);
  return null;
}

export default RedirectPage;
