import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { useRouter } from 'next/router';
import Heading from '@components/common/Heading';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import Card from '@ui/Card';
import SeverityIcon from '@ui/SeverityIcon';
import { Skeleton } from '@ui/Skeleton';
import k8sApi from '@api1/kubernetes';
import apiVm, { VmAlertWebhookStatus } from '@api1/vm';
import { buildSeverityData } from '@components/k8s/clusterEventSeverity';
import { toSeverityLevel } from '@utils/common';
import VmAlerts from './VmAlerts';
import { OBSERVABILITY_INTEGRATIONS_URL, useLatestRequest } from './common';
import { ds } from '@utils/colors';

/** The same window the embedded list opens with, so the counts above it match its rows. */
const WINDOW_MS = 24 * 60 * 60 * 1000;

const PRIORITY_COLUMNS = ['event_count', 'count_priority_high', 'count_priority_medium', 'count_priority_low'];

/** The list below leaves these out unless a priority is picked, so the counts must too. */
const LIST_HIDDEN_PRIORITIES = ['DEBUG', 'INFO'];

interface Counts {
  total: number;
  severities: Array<{ label: string; level: string; value: number }>;
}

/**
 * The Events/Errors block of the Summary: the severity counts, then the first rows of
 * the Alerts tab's own list — same columns, same row actions — rather than a second
 * way of summarising them.
 */
const VmEventsCard = ({ accountId, onViewAll }: { accountId: string; onViewAll: () => void }) => {
  const router = useRouter();
  const [counts, setCounts] = useState<Counts>();
  const [webhook, setWebhook] = useState<VmAlertWebhookStatus>();
  const [webhookError, setWebhookError] = useState<string>();
  const beginWebhook = useLatestRequest();
  const [loading, setLoading] = useState(true);
  const beginRequest = useLatestRequest();

  useEffect(() => {
    if (!accountId) return;
    const isLatest = beginWebhook();
    setWebhook(undefined);
    setWebhookError(undefined);
    apiVm
      .getAlertWebhookStatus(accountId)
      .then((status) => isLatest() && setWebhook(status))
      .catch((err) => isLatest() && setWebhookError(err?.message || 'Failed to check Alertmanager'));
  }, [accountId, beginWebhook]);

  useEffect(() => {
    if (!accountId) return;
    const isLatest = beginRequest();
    const end = new Date();
    setLoading(true);
    k8sApi
      .getK8sEventGroupings(
        1,
        0,
        { account_id: accountId, start_date: new Date(end.getTime() - WINDOW_MS), end_date: end, priority_nin: LIST_HIDDEN_PRIORITIES },
        ['tenant_id', 'account_id'],
        PRIORITY_COLUMNS
      )
      .then((res: any) => {
        if (!isLatest()) return;
        const row = res?.data?.event_groupings?.[0];
        setCounts(
          res?.data
            ? {
                total: Number(row?.event_count) || 0,
                severities: buildSeverityData(row).filter((s: { label: string }) => !['Info', 'Debug'].includes(s.label)),
              }
            : undefined
        );
      })
      .catch((error: unknown) => {
        console.error('Failed to load VM event counts:', error);
        if (isLatest()) setCounts(undefined);
      })
      .finally(() => {
        if (isLatest()) setLoading(false);
      });
  }, [accountId, beginRequest]);

  return (
    <Card size='md' id='vm-summary-events'>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: ds.space[3] }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
          <Heading value='Events/Errors' borderWidth='md' />
          {loading ? (
            <Skeleton shape='text' width='70px' />
          ) : counts ? (
            <Typography
              sx={{
                border: `0.5px solid ${ds.red[400]}`,
                backgroundColor: ds.red[100],
                p: `${ds.space[0]} ${ds.space[2]}`,
                borderRadius: ds.radius.sm,
                color: ds.gray[700],
                fontWeight: ds.weight.medium,
              }}
            >
              {counts.total}
            </Typography>
          ) : null}
        </Box>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[4] }}>
          {loading ? (
            <Skeleton shape='text' width='240px' />
          ) : (
            counts?.severities.map((data) => (
              <SeverityIcon
                key={data.label}
                level={toSeverityLevel(data.level)}
                count={data.value || 0}
                variant='square'
                size={16}
                aria-label={data.label}
              />
            ))
          )}
          <Button id='vm-summary-events-view-all' tone='ghost' size='sm' onClick={onViewAll}>
            View all
          </Button>
        </Box>
      </Box>
      {webhookError ? (
        <Box sx={{ mt: ds.space[3] }}>
          <Banner id='vm-summary-alerts-failure' tone='critical' surface='section' title='Alertmanager unavailable' message={webhookError} />
        </Box>
      ) : webhook && (webhook.state === 'not_configured' || webhook.state === 'disabled') ? (
        <Box sx={{ mt: ds.space[3] }}>
          <Banner
            id='vm-summary-alerts-not-connected'
            tone={webhook.state === 'disabled' ? 'warning' : 'info'}
            surface='section'
            title={webhook.state === 'disabled' ? 'Alertmanager webhook is disabled' : 'Alertmanager is not connected'}
            message={
              webhook.state === 'disabled'
                ? 'Alerts sent to this account’s webhook are dropped until the integration is enabled.'
                : 'Add a Prometheus Alertmanager integration for this account and point a receiver at its webhook to see alerts here.'
            }
            actions={[
              {
                id: 'vm-summary-alerts-connect',
                label: webhook.state === 'disabled' ? 'Manage' : 'Connect Alertmanager',
                tone: 'primary',
                onClick: () => router.push(OBSERVABILITY_INTEGRATIONS_URL),
              },
            ]}
          />
        </Box>
      ) : null}
      <Box sx={{ mt: ds.space[3] }}>
        <VmAlerts accountId={accountId} compact />
      </Box>
    </Card>
  );
};

export default VmEventsCard;
