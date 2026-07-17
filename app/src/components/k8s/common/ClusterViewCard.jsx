import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Divider } from '@ui/Divider';
import { CostCallout } from '@ui/CostCallout';
import ClusterNameWithRegion from './ClusterNameWithRegion';
import KubernetesNodePodStatus from './KubernetesNodePodStatus';
import apiKubernetes from '@api1/kubernetes';
import Link from 'next/link';
import { ArrowRightBlueIcon } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';
import { ds } from '@utils/colors';

const ClusterViewCard = ({ clusterName = '', accountId = '', nodeData = [], podData = [] }) => {
  const [cost, setCost] = useState([]);

  useEffect(() => {
    if (accountId) {
      getForeCastMonthData(accountId);
    }
  }, [accountId]);

  const getForeCastMonthData = async (accountId) => {
    try {
      const response = await apiKubernetes.listk8ClustersYearlySaving(accountId);
      const data = response?.data;
      const costData = [
        { name: 'MTD Cost', cost: data?.mtd_cost || '-' },
        { name: 'Last Month', cost: data?.previous_cost || '-' },
        { name: 'Forecast Month', cost: data?.current_month_projected_spend },
      ];
      setCost(costData);
    } catch (error) {
      console.error(error);
    }
  };

  const lastMonthCost = cost[1]?.cost;
  const forecastCost = cost[2]?.cost;
  const hasValidComparison = typeof lastMonthCost === 'number' && typeof forecastCost === 'number';
  const forecastTone = hasValidComparison ? (lastMonthCost > forecastCost ? 'high-savings' : 'waste') : 'neutral';
  const forecastArrow = hasValidComparison ? (lastMonthCost > forecastCost ? 'down' : 'up') : 'none';

  return (
    <Box
      sx={{
        minHeight: ds.space.mul(0, 62),
        flexShrink: 0,
        position: 'relative',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
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
          display: 'flex',
          backgroundColor: 'var(--ds-blue-500)',
          height: '100%',
          width: ds.space[1],
          borderRadius: 'var(--ds-radius-sm) 0 0 var(--ds-radius-sm)',
        }}
      />
      <Box
        sx={{
          paddingLeft: 'var(--ds-space-2)',
          height: '100%',
          width: '100%',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
          gap: 'var(--ds-space-2)',
        }}
      >
        <Link id={clusterName} passHref href={`/kubernetes/details/${accountId}#summary`} className='link'>
          <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
            <ClusterNameWithRegion hideIcon isTargetURL={true} font={ds.text.heading} fontWeight={500} id={accountId} name={clusterName} />
            <SafeIcon src={ArrowRightBlueIcon} alt='right icon' style={{ marginTop: 'var(--ds-space-1)', zIndex: '2' }} />
          </Box>
        </Link>
        <Divider sx={{ stroke: 'var(--ds-brand-300)', width: '100%', height: '1px' }} />
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-1)', alignItems: 'baseline', flexDirection: 'column' }}>
          <KubernetesNodePodStatus node data={nodeData} />
          <KubernetesNodePodStatus data={podData} />
        </Box>
        <Divider sx={{ stroke: 'var(--ds-brand-300)', width: '100%', height: '1px' }} />
        {cost?.length > 0 ? (
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'space-between' }}>
            {cost.map((entry, index) => (
              <Box key={index}>
                <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                  {entry?.name}
                </Typography>
                {typeof entry?.cost === 'number' ? (
                  <CostCallout
                    value={entry.cost}
                    size='lg'
                    tone={entry.name === 'Forecast Month' ? forecastTone : 'neutral'}
                    arrow={entry.name === 'Forecast Month' ? forecastArrow : 'none'}
                  />
                ) : (
                  <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-600)' }}>
                    {entry?.cost}
                  </Typography>
                )}
              </Box>
            ))}
          </Box>
        ) : (
          <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)' }}>No cost data provided.</Typography>
        )}
      </Box>
    </Box>
  );
};

ClusterViewCard.propTypes = {
  clusterName: PropTypes.string,
  accountId: PropTypes.string,
  id: PropTypes.string,
  nodeData: PropTypes.array,
  podData: PropTypes.array,
  costData: PropTypes.array,
};

export default ClusterViewCard;
