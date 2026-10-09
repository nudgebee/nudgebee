import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import { Banner } from '@ui/Banner';
import ListingLayout from '@ui/ListingLayout';
import apiVm, { VmMetricsState, VmMetricsStatus } from '@api1/vm';
import { useLatestRequest } from './common';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';

const MAX_LISTED = 10;

/** The Observability tab of the integrations page, where Prometheus is listed. */
const CONNECT_URL = '/user-management?integration=observability#integrations';

const BADGE: Record<VmMetricsState, { tone: 'success' | 'warning' | 'critical' | 'neutral'; text: string }> = {
  connected: { tone: 'success', text: 'Connected' },
  no_data: { tone: 'warning', text: 'No data' },
  unreachable: { tone: 'critical', text: 'Unreachable' },
  check_failed: { tone: 'critical', text: 'Check failed' },
  not_configured: { tone: 'neutral', text: 'Not connected' },
};

/**
 * One label per reporting agent. VMs cloned from one image keep the image's hostname,
 * so two agents can share one; listing the name twice reads as a duplicate bug while
 * collapsing it would disagree with the "N VMs" count, so repeats get the start of
 * their instance id to tell them apart.
 */
export const reportingNames = (agents: Array<{ instance: string; hostname?: string }>): string[] => {
  const base = agents.map((a) => a.hostname || a.instance);
  const counts = new Map<string, number>();
  base.forEach((name) => counts.set(name, (counts.get(name) || 0) + 1));
  return agents.map((a, i) => ((counts.get(base[i]) || 0) > 1 ? `${base[i]} (${a.instance.slice(0, 8)})` : base[i]));
};

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** What the other agents in a shared store add to the sentence, so a count never silently includes someone else's VMs. */
const othersNote = (status: VmMetricsStatus) => {
  const n = status.other_agents || 0;
  return n > 0 ? ` ${plural(n, 'other agent is', 'other agents are')} writing to the same store.` : '';
};

/** The one line under the title. Failures are not repeated here: they get a banner. */
const description = (status: VmMetricsStatus, brand: string): string => {
  const n = status.reporting.length;
  const store = status.integration_name || 'the metrics store';
  const accountScoped = status.scope === 'account';
  switch (status.state) {
    case 'connected':
      return accountScoped
        ? `${n} of ${plural(status.inventory || n, 'VM', 'VMs')} writing metrics to ${store}.${othersNote(status)}`
        : `${plural(n, 'agent', 'agents')} writing to ${store}. This account has no VMs on record yet to match them against.`;
    case 'no_data':
      return accountScoped
        ? `${store} is reachable, but none of this account's ${plural(
            status.inventory || 0,
            'VM has',
            'VMs has'
          )} written to it in the last 10 minutes.${othersNote(status)}`
        : `${store} is reachable, but no VM agent has written to it in the last 10 minutes. Install the node agent on a VM and point its metrics endpoint at this store.`;
    case 'unreachable':
      return `${store} could not be reached.`;
    case 'check_failed':
      return brand ? `${brand} could not check this account's metrics.` : "Could not check this account's metrics.";
    default:
      return 'No metrics store is connected to this account. Add a Prometheus integration (connection mode: VM agent for a store only a forager can reach) and make it the default metrics provider.';
  }
};

/**
 * Whether this self-hosted account has metrics: a store Nudgebee can read, and this
 * account's VMs writing to it. A K8s account gets this from its in-cluster agent; a VM
 * account has none, so it is asked for here. It queries the store through a forager, so
 * it loads on its own and does not block the rest of the page.
 */
const VmMetricsStatusCard = ({ accountId }: { accountId: string }) => {
  const router = useRouter();
  const { title: brand } = useBrandingConfig();
  const [status, setStatus] = useState<VmMetricsStatus>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  const load = useCallback(async () => {
    if (!accountId) return;
    const isLatest = beginRequest();
    setLoading(true);
    setError(undefined);
    try {
      const result = await apiVm.getMetricsStatus(accountId);
      if (isLatest()) setStatus(result);
    } catch (err: any) {
      if (!isLatest()) return;
      setStatus(undefined);
      setError(err?.message || 'Failed to check metrics');
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, beginRequest]);

  // A recheck keeps the old result on screen while it runs, but a different account's
  // result must never stand in for this one's.
  const shownFor = useRef(accountId);
  useEffect(() => {
    if (shownFor.current !== accountId) {
      shownFor.current = accountId;
      setStatus(undefined);
      setError(undefined);
    }
    load();
  }, [accountId, load]);

  const badge = status ? BADGE[status.state] : undefined;
  const hosts = reportingNames(status?.reporting || []);
  // The reason for a failure, shown as one banner like the page's other connection problems.
  const failure = error
    ? `Could not check metrics: ${error}`
    : status && ['unreachable', 'check_failed'].includes(status.state)
    ? status.error
    : undefined;

  return (
    <ListingLayout id='vm-metrics-status' sx={{ mt: ds.space[5] }}>
      <ListingLayout.Header
        title={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Box component='span'>Metrics</Box>
            {badge ? <Label id='vm-metrics-badge' size='md' dot tone={badge.tone} text={badge.text} /> : null}
            {loading && !status ? <Label size='md' dot tone='neutral' text='Checking…' /> : null}
          </Box>
        }
        description={status ? description(status, brand) : undefined}
        actions={
          <Box sx={{ display: 'flex', gap: ds.space[2] }}>
            {status?.state === 'not_configured' ? (
              <Button id='vm-metrics-connect' tone='secondary' size='sm' onClick={() => router.push(CONNECT_URL)}>
                Connect
              </Button>
            ) : null}
            <Button id='vm-metrics-recheck' tone='ghost' size='sm' onClick={load} loading={loading}>
              Recheck
            </Button>
          </Box>
        }
      />
      {failure || hosts.length ? (
        <ListingLayout.Body sx={{ pt: ds.space[4], pb: ds.space[5] }}>
          {failure ? <Banner surface='section' tone='critical' id='vm-metrics-failure' message={failure} /> : null}
          {hosts.length ? (
            <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], wordBreak: 'break-word' }}>
              {hosts.slice(0, MAX_LISTED).join(', ')}
              {hosts.length > MAX_LISTED ? ` and ${hosts.length - MAX_LISTED} more` : ''}
            </Typography>
          ) : null}
        </ListingLayout.Body>
      ) : null}
    </ListingLayout>
  );
};

export default VmMetricsStatusCard;
