import React from 'react';
import { Box, Grid, Typography } from '@mui/material';
import { Divider } from '@ui/Divider';
import { Skeleton } from '@ui/Skeleton';
import { SeverityIcon } from '@ui/SeverityIcon';
import Text from '@shared/format/Text';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { ArrowRightBlueIcon } from '@assets';
import { SEVERITY_ORDER } from '@api1/vm';
import { toSeverityLevel } from '@utils/common';
import { ds } from '@utils/colors';
import { SELF_HOSTED_PROVIDER } from './providers';
import type { VmAccountOverviewSummary } from '@api1/overview';

/**
 * One self-hosted VM fleet on the Account Overview page.
 *
 * A self-hosted account has no provider API and therefore no spend, resources
 * or alarms to roll up — what it does have is machines and the CVEs their
 * installed packages match. So the card keeps the shared shape (identity rail +
 * wide panel) but fills the wide panel with the vulnerability severity mix,
 * which is what /vm's own Summary tab leads with.
 *
 * `/vm` is a single tenant-level route with no account path segment, so opening
 * a fleet has to move the header's selected account too — hence `onOpen` rather
 * than a plain <Link>.
 */

interface Props {
  accountId: string;
  accountName: string;
  summary?: VmAccountOverviewSummary;
  loading?: boolean;
  onOpen: (accountId: string) => void;
}

const VmAccountOverviewCard = ({ accountId, accountName, summary, loading = false, onOpen }: Props) => {
  const severities = summary?.severities || {};
  // Fixed order, and only the severities actually present — an all-zero row of
  // six badges reads as "six problems" at a glance.
  const presentSeverities = SEVERITY_ORDER.filter((severity) => (severities[severity] || 0) > 0);

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
            <Box
              id={`vm-account-link-${accountId}`}
              role='link'
              tabIndex={0}
              onClick={() => onOpen(accountId)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                  event.preventDefault();
                  onOpen(accountId);
                }
              }}
              sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--ds-space-2)', cursor: 'pointer' }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
                <CloudProviderIcon cloud_provider={SELF_HOSTED_PROVIDER} width='20px' height='20px' />
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
            <Divider sx={{ stroke: 'var(--ds-brand-300)', width: '100%', height: '1px' }} />
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'space-between' }}>
              <Box>
                <Text value='Virtual Machines' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
                {loading ? (
                  <Skeleton shape='rect' width='60%' height='24px' />
                ) : (
                  <Text value={summary?.vmCount || 0} sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)' }} />
                )}
              </Box>
            </Box>
            <Divider sx={{ stroke: 'var(--ds-brand-300)', width: '100%', height: '1px' }} />
            <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
              Self-hosted fleet
            </Typography>
          </Box>
        </Box>
      </Box>

      <Grid container alignItems='stretch' spacing='14px' columns={{ xs: 4, sm: 8, md: 12 }} sx={{ minWidth: 0, overflow: 'visible' }}>
        <Grid item md={12} sm={8} xs={4}>
          <Box
            sx={{
              minHeight: ds.space.mul(0, 55),
              boxSizing: 'border-box',
              height: '100%',
              display: 'flex',
              flexDirection: 'column',
              gap: 'var(--ds-space-4)',
              borderRadius: 'var(--ds-radius-lg)',
              padding: 'var(--ds-space-4)',
              background: 'var(--ds-pink-100)',
              border: '1px solid var(--ds-red-200)',
            }}
          >
            <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-3)' }}>
              <Text value='Open Vulnerabilities' sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
              {loading ? (
                <Skeleton shape='rect' width={ds.space.mul(0, 20)} height='20px' />
              ) : (
                <Text
                  value={summary?.vulnerabilityCount || 0}
                  sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)' }}
                />
              )}
            </Box>
            {loading ? (
              <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 14)} />
            ) : presentSeverities.length > 0 ? (
              <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-3)' }}>
                {presentSeverities.map((severity) => (
                  <SeverityIcon key={severity} level={toSeverityLevel(severity)} label={severity} count={severities[severity]} />
                ))}
              </Box>
            ) : (
              <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)' }}>
                No open package vulnerabilities on this fleet.
              </Typography>
            )}
          </Box>
        </Grid>
      </Grid>
    </Box>
  );
};

export default VmAccountOverviewCard;
