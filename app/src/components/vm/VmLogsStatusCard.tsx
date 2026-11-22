import React, { useCallback, useEffect, useState } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import { Banner } from '@ui/Banner';
import ListingLayout from '@ui/ListingLayout';
import apiVm, { VmLogsState, VmLogsStatus } from '@api1/vm';
import { useLatestRequest } from './common';
import { ds } from '@utils/colors';

/** The Observability tab of the integrations page, where Loki is listed. */
const CONNECT_URL = '/user-management?integration=observability#integrations';

const BADGE: Record<VmLogsState, { tone: 'success' | 'warning' | 'critical' | 'neutral'; text: string }> = {
  connected: { tone: 'success', text: 'Connected' },
  no_data: { tone: 'warning', text: 'No data' },
  unreachable: { tone: 'critical', text: 'Unreachable' },
  check_failed: { tone: 'critical', text: 'Check failed' },
  not_configured: { tone: 'neutral', text: 'Not connected' },
};

/** The one line under the title. Failures are not repeated here: they get a banner. */
const description = (status: VmLogsStatus): string => {
  const store = status.integration_name || 'the log store';
  switch (status.state) {
    case 'connected':
      return `${store} holds logs from the last 10 minutes.`;
    case 'no_data':
      return `${store} is reachable, but holds no logs from the last 10 minutes. Point your log shipper at this store.`;
    case 'unreachable':
      return `${store} could not be reached.`;
    case 'check_failed':
      return "Nudgebee could not check this account's logs.";
    default:
      return 'No log store is connected to this account. Add a Loki integration (connection mode: VM agent for a store only a forager can reach) and make it the default log provider.';
  }
};

/**
 * Whether this self-hosted account has logs: a store Nudgebee can read, holding recent
 * logs. A K8s account gets this from its in-cluster agent; a VM account has none, so it
 * is asked for here. It queries the store through a forager, so it loads on its own and
 * does not block the rest of the page.
 */
const VmLogsStatusCard = ({ accountId }: { accountId: string }) => {
  const router = useRouter();
  const [status, setStatus] = useState<VmLogsStatus>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  const load = useCallback(async () => {
    if (!accountId) return;
    const isLatest = beginRequest();
    setLoading(true);
    setError(undefined);
    try {
      const result = await apiVm.getLogsStatus(accountId);
      if (isLatest()) setStatus(result);
    } catch (err: any) {
      if (!isLatest()) return;
      setStatus(undefined);
      setError(err?.message || 'Failed to check logs');
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, beginRequest]);

  useEffect(() => {
    load();
  }, [load]);

  const badge = status ? BADGE[status.state] : undefined;
  const failure = error
    ? `Could not check logs: ${error}`
    : status && ['unreachable', 'check_failed'].includes(status.state)
    ? status.error
    : undefined;

  return (
    <ListingLayout id='vm-logs-status' sx={{ mt: ds.space[5] }}>
      <ListingLayout.Header
        title={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Box component='span'>Logs</Box>
            {badge ? <Label id='vm-logs-badge' size='md' dot tone={badge.tone} text={badge.text} /> : null}
            {loading && !status ? <Label size='md' dot tone='neutral' text='Checking…' /> : null}
          </Box>
        }
        description={status ? description(status) : undefined}
        actions={
          <Box sx={{ display: 'flex', gap: ds.space[2] }}>
            {status?.state === 'not_configured' ? (
              <Button id='vm-logs-connect' tone='secondary' size='sm' onClick={() => router.push(CONNECT_URL)}>
                Connect
              </Button>
            ) : null}
            <Button id='vm-logs-recheck' tone='ghost' size='sm' onClick={load} loading={loading}>
              Recheck
            </Button>
          </Box>
        }
      />
      {failure ? (
        <ListingLayout.Body sx={{ pt: ds.space[4], pb: ds.space[5] }}>
          <Banner surface='section' tone='critical' id='vm-logs-failure' message={failure} />
        </ListingLayout.Body>
      ) : null}
    </ListingLayout>
  );
};

export default VmLogsStatusCard;
