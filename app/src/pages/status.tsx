import * as React from 'react';
import Head from 'next/head';
import { Box } from '@mui/material';
import CheckCircleRoundedIcon from '@mui/icons-material/CheckCircleRounded';
import ReportProblemRoundedIcon from '@mui/icons-material/ReportProblemRounded';
import ErrorRoundedIcon from '@mui/icons-material/ErrorRounded';
import BuildRoundedIcon from '@mui/icons-material/BuildRounded';
import HelpRoundedIcon from '@mui/icons-material/HelpRounded';
import { Card, type CardTone } from '@ui/Card';
import Tooltip from '@ui/Tooltip';
import { ds } from '@utils/colors';
import type { ComponentStatus, NoticeState, StatusPayload } from './api/public/status';

// Public status page, styled after Atlassian Statuspage / status.datadoghq.com:
// a single white card floating on a grey page, holding a full-width saturated
// status bar, a clean list of components each shown in its semantic colour, and
// any active incident as a card.
//
// Rendered bare (no PageLayout/DataProvider) via the public-route check in
// _app.tsx, so it must not depend on session, cluster selection, or DataContext.
//
// It is served by the same cluster it reports on and therefore cannot report its
// own total outage — during a full outage this page is unreachable, which is
// exactly when a status page matters most. Treat it as an at-a-glance view for
// partial degradation, not the customer-facing trust artifact that an
// externally-hosted status page (Statuspage/Instatus/BetterStack) provides.

// Matched to the endpoint's cache TTL: polling faster than that only returns the
// same cached payload, so it would cost requests without buying freshness.
const POLL_INTERVAL_MS = 10_000;

// No ds token is pure white — the status-bar text sits on a saturated fill and
// must stay white in either theme, so it is intentionally a literal here.
const ON_SOLID = '#fff';

const COMPONENT_LABEL: Record<ComponentStatus, string> = {
  operational: 'Operational',
  degraded: 'Degraded Performance',
  outage: 'Major Outage',
  maintenance: 'Under Maintenance',
  unknown: 'Not Deployed',
};

const SUMMARY_LABEL: Record<ComponentStatus, string> = {
  operational: 'All Systems Operational',
  degraded: 'Partially Degraded Service',
  outage: 'Major Service Outage',
  maintenance: 'Under Maintenance',
  unknown: 'Status Unavailable',
};

// Hover explanation. Honest to what the probe actually knows: these describe
// reachability, not root cause — the check cannot see why a dependency is
// impaired, only whether the service answered. Kept in sync with the footer
// disclaimer on that point.
const STATUS_EXPLANATION: Record<ComponentStatus, string> = {
  operational: 'Reachable and responding to its health check.',
  degraded: 'Reachable but the service reported itself unhealthy (HTTP 5xx). Some functionality may be impaired.',
  outage: 'Did not respond to its health check — the service may be down or unreachable.',
  maintenance: 'Under planned maintenance. Some disruption is expected during the window.',
  unknown: 'Not configured in this environment, so it is not being checked.',
};

// `solid` fills the top status bar (white text over it); `fg` colours the
// per-component status word and its dot in the list below.
const STATUS_COLOR: Record<ComponentStatus, { solid: string; fg: string }> = {
  operational: { solid: ds.green[600], fg: ds.green[700] },
  degraded: { solid: ds.amber[600], fg: ds.amber[700] },
  outage: { solid: ds.red[600], fg: ds.red[700] },
  maintenance: { solid: ds.blue[600], fg: ds.blue[700] },
  unknown: { solid: ds.gray[500], fg: ds.gray[500] },
};

const STATUS_ICON: Record<ComponentStatus, typeof CheckCircleRoundedIcon> = {
  operational: CheckCircleRoundedIcon,
  degraded: ReportProblemRoundedIcon,
  outage: ErrorRoundedIcon,
  maintenance: BuildRoundedIcon,
  unknown: HelpRoundedIcon,
};

const NOTICE_TITLE: Record<NoticeState, string> = {
  investigating: 'Investigating',
  identified: 'Issue Identified — Fix in Progress',
  monitoring: 'Fix Deployed — Monitoring',
  maintenance: 'Scheduled Maintenance',
};

// Left-accent colour of the incident card (Card variant="accent" tone axis).
const NOTICE_TONE: Record<NoticeState, CardTone> = {
  investigating: 'warning',
  identified: 'warning',
  monitoring: 'info',
  maintenance: 'info',
};

export default function StatusPage() {
  const [payload, setPayload] = React.useState<StatusPayload | null>(null);
  const [failed, setFailed] = React.useState(false);

  React.useEffect(() => {
    let cancelled = false;

    const load = async () => {
      try {
        const res = await fetch('/api/public/status', { headers: { accept: 'application/json' } });
        if (!res.ok) throw new Error(String(res.status));
        const data: StatusPayload = await res.json();
        if (!cancelled) {
          setPayload(data);
          setFailed(false);
        }
      } catch {
        // Keep the last good payload on screen; only the sub-line reflects staleness.
        if (!cancelled) setFailed(true);
      }
    };

    load();
    const timer = setInterval(load, POLL_INTERVAL_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, []);

  const summary: ComponentStatus = failed && !payload ? 'unknown' : payload?.status ?? 'unknown';
  const notice = payload?.notice;
  const HeroIcon = STATUS_ICON[summary];

  const subLine = payload
    ? `As of ${new Date(payload.checked_at).toLocaleTimeString()}${failed ? ' · refresh failed, showing last known' : ''}`
    : failed
    ? 'Unable to reach the status service'
    : 'Checking…';

  return (
    <>
      <Head>
        <title>Nudgebee Status</title>
        <meta name='robots' content='noindex' />
      </Head>

      <Box sx={{ minHeight: '100vh', bgcolor: ds.gray[100], px: ds.space[4], py: ds.space[7] }}>
        {/* Everything sits inside a single elevated white card floating on the grey page. */}
        <Card variant='elevated' size='lg' sx={{ maxWidth: 880, mx: 'auto' }}>
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
            <Box component='h1' sx={{ m: 0, fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700], letterSpacing: '-0.01em' }}>
              Nudgebee Status
            </Box>

            {/* Saturated status bar — the Statuspage / Datadog signature element. */}
            <Box
              data-testid='status-summary'
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: ds.space[3],
                px: ds.space[5],
                py: ds.space[4],
                borderRadius: ds.radius.lg,
                bgcolor: STATUS_COLOR[summary].solid,
                color: ON_SOLID,
              }}
            >
              <HeroIcon sx={{ fontSize: 26 }} />
              <Box sx={{ flex: 1, fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold }}>{SUMMARY_LABEL[summary]}</Box>
              <Box sx={{ fontSize: ds.text.small, color: 'rgba(255,255,255,0.85)', whiteSpace: 'nowrap' }}>{subLine}</Box>
            </Box>

            {/* Active incident / maintenance notice. Flat so its shadow doesn't stack on the outer card. */}
            {notice && (
              <Card variant='accent' tone={NOTICE_TONE[notice.state]} size='md' elevation='flat' data-testid='status-notice-banner'>
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                  <Box sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>{NOTICE_TITLE[notice.state]}</Box>
                  <Box sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>{notice.message}</Box>
                  {notice.started_at && (
                    <Box sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Since {new Date(notice.started_at).toLocaleString()}</Box>
                  )}
                </Box>
              </Card>
            )}

            <Card variant='outlined' size='md' elevation='flat' data-testid='status-components-card'>
              <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                {(payload?.components ?? []).map((component, index) => {
                  const DotIcon = STATUS_ICON[component.status];
                  const color = STATUS_COLOR[component.status].fg;
                  return (
                    <Box
                      key={component.id}
                      data-testid={`status-component-${component.id}`}
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        gap: ds.space[3],
                        py: ds.space[3],
                        borderTop: index === 0 ? 'none' : `1px solid ${ds.gray[200]}`,
                      }}
                    >
                      <Box sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{component.name}</Box>
                      <Tooltip
                        variant='explainer'
                        title={COMPONENT_LABEL[component.status]}
                        desc={component.reason ?? STATUS_EXPLANATION[component.status]}
                      >
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], cursor: 'help' }}>
                          <DotIcon sx={{ fontSize: 16, color }} />
                          <Box sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color }}>{COMPONENT_LABEL[component.status]}</Box>
                        </Box>
                      </Tooltip>
                    </Box>
                  );
                })}
                {!payload && (
                  <Box sx={{ py: ds.space[3], fontSize: ds.text.small, color: ds.gray[500] }}>
                    {failed ? 'No component data available.' : 'Loading components…'}
                  </Box>
                )}
              </Box>
            </Card>

            <Box sx={{ fontSize: ds.text.caption, color: ds.gray[500], lineHeight: 1.6 }}>
              Checks report whether each service is reachable. They do not yet verify each service&apos;s own dependencies, so a component can read
              Operational while an underlying database or queue is impaired.
            </Box>
          </Box>
        </Card>
      </Box>
    </>
  );
}
