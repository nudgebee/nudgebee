import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import { Label } from '@ui/Label';
import { Button } from '@ui/Button';
import { Banner } from '@ui/Banner';
import ListingLayout from '@ui/ListingLayout';
import Datetime from '@shared/format/Datetime';
import apiVm, { VmAlertWebhookState, VmAlertWebhookStatus } from '@api1/vm';
import { OBSERVABILITY_INTEGRATIONS_URL, useLatestRequest } from './common';
import { ds } from '@utils/colors';

const BADGE: Record<VmAlertWebhookState, { tone: 'success' | 'warning' | 'neutral'; text: string }> = {
  receiving: { tone: 'success', text: 'Receiving alerts' },
  no_alerts: { tone: 'warning', text: 'No alerts yet' },
  disabled: { tone: 'warning', text: 'Disabled' },
  not_configured: { tone: 'neutral', text: 'Not connected' },
};

const DESCRIPTION: Record<VmAlertWebhookState, string> = {
  receiving: 'Alerts posted to this account’s Alertmanager webhook are attached to their VM.',
  no_alerts: 'The webhook is enabled, but no alert has reached this account yet. Point an Alertmanager receiver at its webhook URL.',
  disabled: 'The Alertmanager webhook integration is disabled, so alerts sent to it are dropped.',
  not_configured: 'No Alertmanager webhook is connected to this account. Add a Prometheus Alertmanager integration and link it to this account.',
};

/**
 * Whether this self-hosted account is receiving Alertmanager alerts. A K8s account gets
 * its Alert Manager row from the in-cluster agent; a VM account has no agent, and the
 * webhook is push-only, so the status is the integration's state and when it last
 * delivered.
 */
const VmAlertsStatusCard = ({ accountId }: { accountId: string }) => {
  const router = useRouter();
  const [status, setStatus] = useState<VmAlertWebhookStatus>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  const load = useCallback(async () => {
    if (!accountId) return;
    const isLatest = beginRequest();
    setLoading(true);
    setError(undefined);
    try {
      const result = await apiVm.getAlertWebhookStatus(accountId);
      if (isLatest()) setStatus(result);
    } catch (err: any) {
      if (!isLatest()) return;
      setStatus(undefined);
      setError(err?.message || 'Failed to check alerts');
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, beginRequest]);

  // A different account's result must never stand in for this one's.
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

  return (
    <ListingLayout id='vm-alerts-status' sx={{ mt: ds.space[5] }}>
      <ListingLayout.Header
        title={
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <Box component='span'>Alert Manager</Box>
            {badge ? <Label id='vm-alerts-badge' size='md' dot tone={badge.tone} text={badge.text} /> : null}
            {loading && !status ? <Label size='md' dot tone='neutral' text='Checking…' /> : null}
          </Box>
        }
        description={status ? DESCRIPTION[status.state] : undefined}
        actions={
          <Box sx={{ display: 'flex', gap: ds.space[2], alignItems: 'center' }}>
            {status?.last_received_at ? (
              <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                Last alert <Datetime value={status.last_received_at} />
              </Box>
            ) : null}
            {status?.state === 'not_configured' || status?.state === 'disabled' ? (
              <Button id='vm-alerts-connect' tone='secondary' size='sm' onClick={() => router.push(OBSERVABILITY_INTEGRATIONS_URL)}>
                {status.state === 'disabled' ? 'Manage' : 'Connect'}
              </Button>
            ) : null}
            <Button id='vm-alerts-recheck' tone='ghost' size='sm' onClick={load} loading={loading}>
              Recheck
            </Button>
          </Box>
        }
      />
      {error ? (
        <ListingLayout.Body sx={{ pt: ds.space[4], pb: ds.space[5] }}>
          <Banner surface='section' tone='critical' id='vm-alerts-failure' message={`Could not check alerts: ${error}`} />
        </ListingLayout.Body>
      ) : null}
    </ListingLayout>
  );
};

export default VmAlertsStatusCard;
