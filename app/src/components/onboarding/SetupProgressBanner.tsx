/**
 * Progress banner — spec §3.2. Sits at the top of `/home` once the gate is
 * open, naming what is still outstanding. `/setup` is a first-run-only surface,
 * so the banner does not link back to it: "View connections" opens the admin
 * Overview tab's Connections sub-view, where every connected account and its
 * logs/metrics/traces coverage is listed.
 *
 * `readiness` comes from `useOnboardingReadiness()` — the same hook
 * `SetupCenter` itself uses — so the two can never show a different "X of N"
 * for the same tenant. They used to: this banner previously read a cheaper
 * snapshot off `SetupGateContext` that never saw K8s agent health,
 * auto-detected telemetry or Knowledge Base, so a K8s account still waiting
 * on its agent could count as connected here but not on the card. `loading`/
 * `locked`/`canManageSetup` still come from the gate — a separate, cheaper
 * lookup that decides whether the tenant is gated at all, unrelated to what
 * this banner shows once they aren't (it is no longer accounts-only itself
 * either, just still far cheaper than this hook's own fetch — see
 * `SetupGateContext.tsx`'s own doc comment).
 */
import * as React from 'react';
import { useRouter } from 'next/router';
import { useSession } from 'next-auth/react';
import { Box } from '@mui/material';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { ProgressBar } from '@ui/ProgressBar';
import { useSetupGate } from '@context/SetupGateContext';
import { useOnboardingReadiness } from '@hooks/useOnboardingReadiness';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import apiUser from '@api1/user';

const SNOOZE_MS = 24 * 60 * 60 * 1000;
/** Admin Overview → Connections sub-view (AdminOverview reads the `action` slug). */
const CONNECTIONS_HREF = '/user-management?action=overview-connections#overview';
/** Outstanding items named individually before the rest collapse into a count. */
const NAMED_ITEM_LIMIT = 3;

// "Not needed" reuses the same per-tenant hidden-until preference Snooze
// already writes to (api1/user/index.js) — there is no separate "dismissed
// forever" flag, just a snooze whose "until" is far enough out to never
// practically return. This hides *this banner* only: it does not touch
// `outstandingRequired`/the item's tracked state, so the Setup Center card
// still shows the same item as outstanding — no Skip control on the
// checklist itself (setup-center-build-spec.md §6.4: "no Skip control,
// ever"), just a way to stop being nagged about something optional.
const NOT_NEEDED_UNTIL = '9999-12-31T23:59:59.999Z';

// Scoped per tenant (api1/user/index.js) — a snooze recorded while viewing
// one tenant must not silently suppress the banner on another tenant sharing
// the same browser (known issue #18: this used to be a single flat key).
const readHiddenUntil = (tenantId: string | undefined): number => {
  if (!tenantId) return 0;
  try {
    const raw = apiUser.getSetupBannerHiddenUntil(tenantId);
    const parsed = raw ? Date.parse(raw) : NaN;
    return Number.isNaN(parsed) ? 0 : parsed;
  } catch {
    // Storage unavailable — never let that suppress the banner permanently.
    return 0;
  }
};

const SetupProgressBannerContent = () => {
  const router = useRouter();
  const { data: session } = useSession();
  const tenantId = (session as { tenant?: { id?: string } } | null)?.tenant?.id;
  const { loading, locked, canManageSetup } = useSetupGate();
  const { readiness } = useOnboardingReadiness();
  const { title: brandTitle } = useBrandingConfig();
  const [hiddenUntil, setHiddenUntil] = React.useState<number>(0);

  // `tenantId` resolves asynchronously (the session), so — unlike the old
  // flat-key read — it is not necessarily known on the very first render;
  // this re-reads once it is, and again only if it changes. It never fires
  // again from a Dismiss in the same session, since that does not change
  // `tenantId`, so it cannot stomp the fresher local state `handleDismiss`
  // below sets.
  React.useEffect(() => {
    setHiddenUntil(readHiddenUntil(tenantId));
  }, [tenantId]);

  // Read-only viewers never see it (spec §3.2, §6b): it would be a recurring
  // instruction to perform a task they are not permitted to perform, since
  // every Add button on the card they would land on is hidden for them.
  if (loading || !readiness || locked || !canManageSetup) {
    return null;
  }

  const outstanding = readiness.outstandingRequired;
  if (outstanding.length === 0 || Date.now() < hiddenUntil) {
    return null;
  }

  const named = outstanding.slice(0, NAMED_ITEM_LIMIT);
  const remaining = outstanding.length - named.length;

  const hideUntil = (until: string) => {
    try {
      apiUser.setSetupBannerHiddenUntil(until, tenantId);
    } catch {
      // Best-effort — still hides for this session even if persistence fails.
    }
    setHiddenUntil(Date.parse(until));
  };

  // A snooze, not a dismissal: it returns in 24h.
  const handleDismiss = () => hideUntil(new Date(Date.now() + SNOOZE_MS).toISOString());
  // Not a snooze: see NOT_NEEDED_UNTIL's doc comment for what this does and
  // doesn't do.
  const handleNotNeeded = () => hideUntil(NOT_NEEDED_UNTIL);

  return (
    // mt: gives this banner room from whatever sits above it on /home
    // (typically PendingAgentInstallBanner, layout/index.jsx:964) — the two
    // used to sit flush against each other with no visual separation.
    <Box data-testid='setup-progress-banner' sx={{ mt: 'var(--ds-space-4)' }}>
      <Banner
        tone='info'
        surface='page'
        title={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-3)' }}>
            <Box component='span'>{brandTitle ? `Finish setting up ${brandTitle}` : 'Finish setting up your workspace'}</Box>
            <Box sx={{ width: '220px', flexShrink: 0 }}>
              <ProgressBar
                id='setup-progress-banner-bar'
                value={readiness.percent}
                size='sm'
                tone='info'
                // Same fixed-width label constraint SetupCenter's own bar
                // documents — keep this to 'N of N', nothing longer.
                label={`${readiness.done} of ${readiness.total}`}
              />
            </Box>
          </Box>
        }
        message={
          // Banner's own `actions` prop renders its own internal button, not
          // `@ui/Button`, and caps at two — neither works for three real,
          // equally-weighted DS buttons in one row, and `actionsPlacement`
          // only repositions that same `actions` prop, so there is no slot
          // that puts custom buttons at the row's right edge. This flex row
          // is composed here instead: text+tags on the left, buttons pinned
          // to the right instead of stacked below them.
          <Box sx={{ display: 'flex', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--ds-space-4)' }}>
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', flex: 1, minWidth: 0 }}>
              <Box component='div'>
                {brandTitle
                  ? `To make the most of ${brandTitle}, we recommend connecting what's still outstanding across your tenant:`
                  : "To make the most of your workspace, we recommend connecting what's still outstanding across your tenant:"}
              </Box>
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-1)' }}>
                {named.map((item) => (
                  <Chip key={item.id} size='xs' tone='neutral'>
                    {item.name}
                  </Chip>
                ))}
                {remaining > 0 && (
                  <Chip size='xs' tone='subtle'>
                    +{remaining} more
                  </Chip>
                )}
              </Box>
            </Box>
            {/* "Snooze", not "Remind me later": the house term for a temporary
                defer, used the same way by recommendations, security findings,
                events, and FirstLoginTour. */}
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', flexShrink: 0 }}>
              <Button tone='secondary' size='sm' onClick={() => router.push(CONNECTIONS_HREF)}>
                View connections
              </Button>
              <Button tone='ghost' size='sm' onClick={handleNotNeeded}>
                Not needed…
              </Button>
              <Button tone='ghost' size='sm' onClick={handleDismiss}>
                Snooze
              </Button>
            </Box>
          </Box>
        }
      />
    </Box>
  );
};

// `UI_ENABLE_SETUP_CENTER` off: never mount the content at all — not just
// render nothing — because `useOnboardingReadiness()` fires its account,
// capability, agent-health and knowledge-base lookups on mount, and every
// `/home` load would pay for an answer nothing reads. See `SetupGateContext.tsx`.
const SetupProgressBanner = () => {
  const { setupCenterEnabled } = useSetupGate();
  return setupCenterEnabled ? <SetupProgressBannerContent /> : null;
};

export default SetupProgressBanner;
