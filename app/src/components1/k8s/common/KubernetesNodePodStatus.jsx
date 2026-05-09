import { Box, Divider, Typography, Tooltip, Grid } from '@mui/material';
import React from 'react';
import OnDemandIcon from '@assets/on-demand-icon.svg';
import FallbackIcon from '@assets/fallback-icon.svg';
import SpotIcon from '@assets/spot-icon.svg';
import SafeIcon from '@components1/common/SafeIcon';
import { Text } from '@components1/common';
import { truncateText } from 'src/utils/common';

const styles = {
  text: {
    color: '#374151',
    fontSize: '14px',
    fontWeight: 500,
  },
  image: {
    width: '12px',
    height: '12px',
  },
};

function KubernetesNodePodStatus({ data = [], node }) {
  let total = 0;

  if (node) {
    data.forEach((item) => {
      total += item.count;
    });
  } else {
    for (let d of data) {
      if (d.type == 'Total') {
        total = d.count;
        break;
      }
    }
  }

  const RenderPods = () => {
    const filteredPods = data?.filter((i) => i?.type !== 'Total');

    return (
      <Grid container>
        {filteredPods.map((item, index) => (
          <Grid item xs={6} key={index}>
            <Box ml={'19px'}>
              <Tooltip title={item.type}>
                <Typography sx={{ fontSize: '12px', color: '#9F9F9F', fontWeight: 400 }}>{truncateText(item.type || '-', 15)}</Typography>
              </Tooltip>
              <Typography sx={{ color: '#374151', fontSize: '14px', fontWeight: 500 }}>{item.count || '-'}</Typography>
            </Box>
          </Grid>
        ))}
      </Grid>
    );
  };

  const RenderNodes = () => {
    const nodeOrder = ['demand', 'fallback', 'spot'];
    const nodeMap = {
      demand: { icon: OnDemandIcon, title: 'on-demand' },
      fallback: { icon: FallbackIcon, title: 'fallback' },
      spot: { icon: SpotIcon, title: 'spot' },
    };

    const sortedData = [...data].sort((a, b) => nodeOrder.indexOf(a.type) - nodeOrder.indexOf(b.type));

    return (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'flex-end',
          color: '#768BB2',
          gap: '34px',
          marginLeft: '19px',
        }}
      >
        {sortedData?.map((item, index) => {
          const { type, count } = item;
          const { icon, title } = nodeMap[type] || {};

          return (
            <Tooltip key={index} title={title}>
              <Box>
                <SafeIcon alt={index} style={styles.image} src={icon} />
                <Typography sx={styles.text}>{count}</Typography>
              </Box>
            </Tooltip>
          );
        })}
      </Box>
    );
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center' }}>
      {
        <Box mr={'12px'} minWidth={'40px'}>
          <Text value={node ? 'Node' : 'Pods'} secondaryText sx={{ fontWeight: 500 }} />
          <Text value={total || '-'} sx={{ fontSize: '20px', fontWeight: 600 }} />
        </Box>
      }
      <Divider sx={{ height: '24px', stroke: '#B9B9B9' }} orientation='vertical' variant='middle' />
      {node ? <RenderNodes /> : <RenderPods />}
    </Box>
  );
}

export default KubernetesNodePodStatus;
