import * as React from 'react';
import { useRouter } from 'next/router';
import { Box, CircularProgress } from '@mui/material';
import { useSetupGate, SETUP_ROUTE } from '@context/SetupGateContext';
import { TENANT_SWITCH_FLAG } from '@lib/tenantSwitchOverlay';

// How long this is allowed to hold the page before giving up and showing it
// anyway — a stuck lookup must never trade a brief flash for an indefinite
// blank screen. Generous: the gate's own account (and, for K8s/self-hosted,
// agent-health) lookups are real network round trips.
const MAX_WAIT_MS = 8000;

/**
 * Masks the "old page, then home page, then Setup page" flash a tenant
 * switch produces (#41898) — SwitchTenant's `window.location.href` is a hard
 * reload (required by tabTenant.ts's pin invariant, so it can't become a
 * client-side navigation instead), and `SetupGateContext`'s own `loading`
 * deliberately stays "unlocked" while the gate is still resolving (see that
 * file's doc comment — correct for the common, ungated case, where it avoids
 * flashing a locked nav for no reason). Neither of those is this file's to
 * change. This sits on top of both instead: it only ever appears for the one
 * page load right after a tenant switch (SwitchTenant sets the flag this
 * reads, right before the reload), and it only stays up long enough for the
 * gate to finish deciding where this tenant actually lands.
 */
export default function TenantSwitchOverlay(): React.ReactElement | null {
  // Starts `false` on every render, server or client — reading sessionStorage
  // straight into this state (even lazily) would make the client's first
  // render disagree with the server's the moment a switch really is in
  // flight, which is a hydration mismatch, not just a cosmetic one-frame
  // delay. Reading it in an effect instead means it can only ever run after
  // hydration has already reconciled, on the client only.
  const [active, setActive] = React.useState(false);
  const { loading, locked } = useSetupGate();
  const router = useRouter();

  // `locked` means a redirect to SETUP_ROUTE is coming (SetupGateContext's
  // own effect fires it); staying up until the route actually gets there
  // avoids revealing the wrong page for the one render between "loading
  // finished" and "the redirect landed".
  const ready = !loading && (!locked || router.pathname === SETUP_ROUTE);

  React.useEffect(() => {
    try {
      if (sessionStorage.getItem(TENANT_SWITCH_FLAG) === '1') setActive(true);
    } catch {
      // ignore
    }
    // Runs once, right after this page's hydration — a later tenant switch
    // sets the flag on a page this component remounts fresh for, not this
    // instance, so there's nothing to react to it changing again here.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  React.useEffect(() => {
    if (!active) return undefined;
    if (ready) {
      try {
        sessionStorage.removeItem(TENANT_SWITCH_FLAG);
      } catch {
        // ignore
      }
      setActive(false);
      return undefined;
    }
    const timer = setTimeout(() => {
      try {
        sessionStorage.removeItem(TENANT_SWITCH_FLAG);
      } catch {
        // ignore
      }
      setActive(false);
    }, MAX_WAIT_MS);
    return () => clearTimeout(timer);
  }, [active, ready]);

  if (!active || ready) return null;

  return (
    <Box
      data-testid='tenant-switch-overlay'
      sx={{
        position: 'fixed',
        inset: 0,
        zIndex: 9999,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        backgroundColor: 'var(--ds-background-100)',
      }}
    >
      <CircularProgress aria-label='Switching tenant' sx={{ color: 'var(--ds-brand-500)' }} />
    </Box>
  );
}
