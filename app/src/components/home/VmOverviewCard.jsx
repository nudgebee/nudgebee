import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import DSCard from '@ui/Card';
import { Stat } from '@ui/Stat';
import { Button } from '@ui/Button';
import { Skeleton } from '@ui/Skeleton';
import SafeIcon from '@shared/icons/SafeIcon';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import { VmIcon } from '@assets';
import apiVm from '@api1/vm';
import { isSelfHostedProvider } from '@components/overview/providers';
import { SeverityCounts, VISIBLE_SEVERITIES } from '@components/vm/common';

/**
 * Home page's self-hosted VM fleet summary (#38461) — mirrors AutomationsCard's
 * shape (compact DSCard + Stat grid + "View all" link into the owning page).
 *
 * Data comes from the same cheap, per-account queries VmSummary.tsx's own
 * tiles use — VM count, severity counts, agents connected, last scan — never
 * the `topGroups('vm')` grouping call, which is the slow one (#38494).
 *
 * Renders nothing for a non-self-hosted account: Home is scoped to one
 * selected account, and this card only has something to say about a VM fleet.
 */
const VmOverviewCard = ({ accountId, cloudProvider, onViewAll }) => {
  const [loading, setLoading] = useState(true);
  const [summary, setSummary] = useState(null);
  const isSelfHosted = isSelfHostedProvider(cloudProvider);

  // Guards against switching the selected account before the first request
  // completes: without this, a slower response for a previously-selected
  // account can resolve last and overwrite the card with the wrong
  // account's counts. Same pattern as PendingFollowUps.jsx's
  // latestRequestRef, in this same folder.
  const latestRequestRef = useRef(0);

  const fetchSummary = useCallback(() => {
    if (!accountId || !isSelfHosted) return;
    setLoading(true);
    const requestId = ++latestRequestRef.current;
    Promise.all([apiVm.listVms({ accountId, limit: 1, offset: 0 }), apiVm.getSeverityCounts({ accountId }), apiVm.listAgents(accountId)])
      .then(([vms, severities, agents]) => {
        if (requestId !== latestRequestRef.current) return;
        setSummary({
          vmCount: vms.total,
          severities,
          agentsConnected: agents.filter((agent) => agent.connected).length,
          agentsTotal: agents.length,
        });
      })
      .catch((error) => {
        if (requestId !== latestRequestRef.current) return;
        console.error('Failed to load VM fleet overview:', error);
        setSummary(null);
      })
      .finally(() => {
        if (requestId === latestRequestRef.current) setLoading(false);
      });
  }, [accountId, isSelfHosted]);

  useEffect(() => {
    fetchSummary();
  }, [fetchSummary]);

  if (!isSelfHosted) return null;

  const presentSeverities = VISIBLE_SEVERITIES.filter((severity) => (summary?.severities?.[severity] || 0) > 0);

  const header = (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
        <Box
          sx={{
            width: 28,
            height: 28,
            borderRadius: 'var(--ds-radius-md)',
            backgroundColor: 'var(--ds-blue-100)',
            color: 'var(--ds-blue-600)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          <SafeIcon src={VmIcon} alt='VM fleet' width={16} height={16} />
        </Box>
        <Typography
          sx={{
            fontFamily: 'var(--ds-font-display)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-gray-900)',
            letterSpacing: '-0.01em',
          }}
        >
          VM Fleet
        </Typography>
      </Box>
      <Button
        tone='ghost'
        size='xs'
        icon={<KeyboardArrowRightIcon />}
        iconPlacement='end'
        onClick={() => onViewAll?.(accountId)}
        id='vm-overview-view-all'
      >
        View all
      </Button>
    </Box>
  );

  return (
    <DSCard size='sm' elevation='flat' header={header} sx={{ overflow: 'hidden' }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 'var(--ds-space-3)' }}>
          <Stat size='sm' align='center' label='Virtual Machines' value={loading ? '-' : String(summary?.vmCount || 0)} id='vm-overview-count-stat' />
          <Stat
            size='sm'
            align='center'
            label='Agents Connected'
            value={loading ? '-' : `${summary?.agentsConnected || 0} / ${summary?.agentsTotal || 0}`}
            id='vm-overview-agents-stat'
          />
        </Box>
        {loading ? (
          <Skeleton shape='rect' width='100%' height='20px' />
        ) : presentSeverities.length > 0 ? (
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2)', alignItems: 'center' }}>
            <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)' }}>Open Vulnerabilities:</Typography>
            <SeverityCounts counts={summary.severities} />
          </Box>
        ) : (
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)' }}>No open package vulnerabilities.</Typography>
        )}
      </Box>
    </DSCard>
  );
};

VmOverviewCard.propTypes = {
  accountId: PropTypes.string,
  cloudProvider: PropTypes.string,
  onViewAll: PropTypes.func,
};

export default VmOverviewCard;
