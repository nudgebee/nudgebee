import React from 'react';
import Link from 'next/link';
import { Box, Grid, Typography } from '@mui/material';
import { Divider } from '@ui/Divider';
import { CostCallout } from '@ui/CostCallout';
import { Skeleton } from '@ui/Skeleton';
import Text from '@shared/format/Text';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import ClusterPotentialSaving from '@components/k8s/common/ClusterPotentialSaving';
import { ArrowRightBlueIcon } from '@assets';
import { getBudgetExpectedMonthlyExpense } from '@lib/budget';
import { ds } from '@utils/colors';
import type { CloudAccountOverviewSummary } from '@api1/overview';

/**
 * One AWS / Azure / GCP / CloudFoundry account on the Account Overview page.
 *
 * Deliberately laid out like the K8s cluster card next to it — identity rail,
 * wide metric panel, an incident panel, a savings panel — so a mixed fleet
 * reads as one list rather than two designs stacked. What differs is the
 * content: a cloud account has no nodes or pods, so the wide panel carries the
 * spend picture (which for K8s lives on the identity rail) and the incident
 * panel counts provider alarms rather than pod/node issues.
 *
 * All numbers come pre-batched from apiOverview.listCloudAccountSummaries — the
 * card issues no requests of its own.
 */

const PANEL_SX = {
  minHeight: ds.space.mul(0, 55),
  boxSizing: 'border-box',
  height: '100%',
  display: 'flex',
  flexDirection: 'column',
  borderRadius: 'var(--ds-radius-lg)',
  padding: 'var(--ds-space-4)',
  gap: 'var(--ds-space-4)',
};

interface Props {
  accountId: string;
  accountName: string;
  cloudProvider: string;
  summary?: CloudAccountOverviewSummary;
  loading?: boolean;
}

const CountBlock = ({ label, value, loading }: { label: string; value: number; loading?: boolean }) => (
  <Box>
    <Text value={label} sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
    {loading ? (
      <Skeleton shape='rect' width='60%' height='24px' />
    ) : (
      <Text value={value ?? 0} sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)' }} />
    )}
  </Box>
);

const CloudAccountOverviewCard = ({ accountId, accountName, cloudProvider, summary, loading = false }: Props) => {
  const detailsHref = `/cloud-account/details/${accountId}#summary`;
  const forecast = getBudgetExpectedMonthlyExpense(summary?.mtdSpend || 0);
  const lastMonth = summary?.lastMonthSpend || 0;
  // Same convention as the K8s card: tone the forecast against last month, and
  // stay neutral when there is nothing to compare it with.
  const hasComparison = lastMonth > 0 && forecast > 0;
  const forecastTone = hasComparison ? (lastMonth > forecast ? 'high-savings' : 'waste') : 'neutral';
  const forecastArrow = hasComparison ? (lastMonth > forecast ? 'down' : 'up') : 'none';

  const costs: Array<{ name: string; value: number; tone?: string; arrow?: string }> = [
    { name: 'MTD Cost', value: summary?.mtdSpend || 0 },
    { name: 'Last Month', value: lastMonth },
    { name: 'Forecast Month', value: forecast, tone: forecastTone, arrow: forecastArrow },
    { name: 'YTD Cost', value: summary?.ytdSpend || 0 },
  ];

  return (
    <Box display='flex' gap={ds.space[4]} alignItems='flex-start'>
      <Box sx={{ width: { xs: ds.space.mul(0, 130), md: ds.space.mul(0, 160) }, flexShrink: 0 }}>
        <Box
          sx={{
            minHeight: ds.space.mul(0, 62),
            position: 'relative',
            display: 'flex',
            backgroundColor: 'var(--ds-background-200)',
            p: 'var(--ds-space-4)',
            overflow: 'hidden',
            borderRadius: 'var(--ds-radius-lg)',
            border: '1px solid var(--ds-blue-200)',
            boxSizing: 'border-box',
          }}
        >
          <Box
            sx={{
              left: 0,
              top: 0,
              position: 'absolute',
              backgroundColor: 'var(--ds-blue-500)',
              height: '100%',
              width: ds.space[1],
              borderRadius: 'var(--ds-radius-sm) 0 0 var(--ds-radius-sm)',
            }}
          />
          <Box
            sx={{
              paddingLeft: 'var(--ds-space-2)',
              width: '100%',
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'space-between',
              gap: 'var(--ds-space-2)',
            }}
          >
            <Link id={`account-link-${accountId}`} passHref href={detailsHref} className='link'>
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
                  <CloudProviderIcon cloud_provider={cloudProvider} width='20px' height='20px' />
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-heading)',
                      fontWeight: 500,
                      color: 'var(--ds-brand-500)',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                    title={accountName}
                  >
                    {accountName}
                  </Typography>
                </Box>
                <SafeIcon src={ArrowRightBlueIcon} alt='right icon' style={{ zIndex: '2' }} />
              </Box>
            </Link>
            <Divider sx={{ stroke: 'var(--ds-brand-300)', width: '100%', height: '1px' }} />
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'space-between' }}>
              <CountBlock label='Resources' value={summary?.resourceCount || 0} loading={loading} />
              <CountBlock label='Recommendations' value={summary?.recommendationCount || 0} loading={loading} />
            </Box>
            <Divider sx={{ stroke: 'var(--ds-brand-300)', width: '100%', height: '1px' }} />
            <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
              {cloudProvider} account
            </Typography>
          </Box>
        </Box>
      </Box>

      <Grid container alignItems='stretch' spacing='14px' columns={{ xs: 4, sm: 8, md: 12 }} sx={{ minWidth: 0, overflow: 'visible' }}>
        <Grid item md={7} sm={8} xs={4} sx={{ overflow: 'visible' }}>
          <Box sx={{ ...PANEL_SX, background: 'var(--ds-background-200)', border: '1px solid var(--ds-gray-200)' }}>
            <Text value='Cloud Spend' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'space-between', flexWrap: 'wrap' }}>
              {costs.map((entry) => (
                <Box key={entry.name}>
                  <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                    {entry.name}
                  </Typography>
                  {loading ? (
                    <Skeleton shape='rect' width={ds.space.mul(0, 40)} height='24px' />
                  ) : (
                    <CostCallout value={entry.value} size='lg' tone={(entry.tone as any) || 'neutral'} arrow={(entry.arrow as any) || 'none'} />
                  )}
                </Box>
              ))}
            </Box>
          </Box>
        </Grid>
        <Grid item md={3} sm={4} xs={4}>
          <Box sx={{ ...PANEL_SX, background: 'var(--ds-pink-100)', border: '1px solid var(--ds-red-200)' }}>
            <CountBlock label='Alerts (last 7 days)' value={summary?.alertCount || 0} loading={loading} />
          </Box>
        </Grid>
        <Grid item md={2} sm={4} xs={4}>
          <ClusterPotentialSaving
            savingPotentialSummary={{ yearly_recommendation_saving: (summary?.estimatedSavings || 0) * 12 }}
            loading={loading}
          />
        </Grid>
      </Grid>
    </Box>
  );
};

export default CloudAccountOverviewCard;
