import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import ClusterNameWithRegion from './ClusterNameWithRegion';
import KubernetesNodePodStatus from './KubernetesNodePodStatus';
import CostView from '@components1/common/CostView';
import apiKubernetes from '@api1/kubernetes';
import Link from 'next/link';
import { ArrowRightBlueIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
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
        <CostView data={cost} />
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
