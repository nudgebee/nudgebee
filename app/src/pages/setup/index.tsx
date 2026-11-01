/**
 * `/setup` — the Setup Center's own page.
 *
 * The card used to render inside the home page, with a `hideWidgets` flag
 * suppressing the real dashboard around it. That made one page mean two things,
 * required every home widget to know about onboarding, and forced the card to
 * hide itself once setup was complete — which is what left a gated user with no
 * Finish button on screen and no way out.
 *
 * On its own route none of that is needed. The card always renders here, because
 * you only arrive deliberately: while the setup gate is locked every other route
 * redirects here. Nothing links here afterwards — the Home progress banner
 * points at the admin Overview's Connections view instead.
 */
import * as React from 'react';
import { useRouter } from 'next/router';
import { Box } from '@mui/material';
import SetupCenter from '@components/onboarding/SetupCenter';
import { useSetupGate } from '@context/SetupGateContext';

const SetupPage = () => {
  const router = useRouter();
  const { loading, setupCenterEnabled } = useSetupGate();

  // Defense-in-depth for a stale bookmark or a typed URL: nobody is sent here
  // organically while `UI_ENABLE_SETUP_CENTER` is off, since the gate that
  // would send them here never locks (see `SetupGateContext.tsx`). Waits for
  // `loading` so this doesn't fire on the flag's not-yet-resolved default
  // before the session answers.
  React.useEffect(() => {
    if (loading || setupCenterEnabled) return;
    router.replace('/home');
  }, [loading, setupCenterEnabled, router]);

  // Nothing until the flag is confirmed on — mounting `SetupCenter` while it
  // is still resolving (or off, mid-redirect) would flash the card and fire
  // its own readiness lookups for a page the user is about to leave. Keyed on
  // the flag alone, not `loading`: the flag reads `false` until the session
  // arrives, and `loading` goes true again on every `refresh()` — which
  // `SetupCenter` calls right after an account connects, so gating on it
  // would unmount the card mid-flow.
  if (!setupCenterEnabled) return null;

  return (
    <Box
      data-testid='setup-page'
      sx={{
        // Matches the reading width the card was given inside the home grid, so
        // moving it to its own route does not change how it looks.
        maxWidth: 1180,
        mx: 'auto',
        px: 'var(--ds-space-4)',
        pt: 'calc(var(--ds-space-0) * 14)',
        pb: 'var(--ds-space-6)',
      }}
    >
      <SetupCenter />
    </Box>
  );
};

export default SetupPage;
