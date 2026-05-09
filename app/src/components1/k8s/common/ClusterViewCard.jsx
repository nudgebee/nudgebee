import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Divider } from '@mui/material';
import ClusterNameWithRegion from './ClusterNameWithRegion';
import KubernetesNodePodStatus from './KubernetesNodePodStatus';
import CostView from '@components1/common/CostView';
import apiKubernetes from '@api1/kubernetes';
import Link from 'next/link';
import { ArrowRightBlueIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

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
        minHeight: '124px',
        flexShrink: 0,
        position: 'relative',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        backgroundColor: '#F8FAFF',
        p: '16px',
        overflow: 'hidden',
        borderRadius: '10px',
        border: '1px solid #DBEAFE',
        boxSizing: 'border-box',
      }}
    >
      <Box
        sx={{
          left: 0,
          top: 0,
          position: 'absolute',
          display: 'flex',
          backgroundColor: '#3B82F6',
          height: '100%',
          width: '4px',
          borderRadius: '4px 0 0 4px',
        }}
      />
      <Box
        sx={{
          paddingLeft: '8px',
          height: '100%',
          width: '100%',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
          gap: '10px',
        }}
      >
        <Link id={clusterName} passHref href={`/kubernetes/details/${accountId}#summary`} className='link'>
          <Box sx={{ display: 'flex', justifyContent: 'space-between' }}>
            <ClusterNameWithRegion hideIcon isTargetURL={true} font='20px' fontWeight={500} id={accountId} name={clusterName} />
            <SafeIcon src={ArrowRightBlueIcon} alt='right icon' style={{ marginTop: '5px', zIndex: '2' }} />
          </Box>
        </Link>
        <Divider sx={{ stroke: '#B9B9B9', width: '100%', height: '1px' }} />
        <Box sx={{ display: 'flex', gap: '6px', alignItems: 'baseline', flexDirection: 'column' }}>
          <KubernetesNodePodStatus node data={nodeData} />
          <KubernetesNodePodStatus data={podData} />
        </Box>
        <Divider sx={{ stroke: '#B9B9B9', width: '100%', height: '1px' }} />
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
