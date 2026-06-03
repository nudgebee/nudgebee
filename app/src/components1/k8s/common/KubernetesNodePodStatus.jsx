import { Box, Typography, Grid } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import { Divider } from '@components1/ds/Divider';
import React from 'react';
import OnDemandIcon from '@assets/on-demand-icon.svg';
import FallbackIcon from '@assets/fallback-icon.svg';
import SpotIcon from '@assets/spot-icon.svg';
import SafeIcon from '@components1/common/SafeIcon';
import Text from '@common-new/format/Text';
import { truncateText } from 'src/utils/common';
import { ds } from '@utils/colors';

const styles = {
  text: {
    color: 'var(--ds-brand-500)',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-medium)',
  },
  image: {
    width: ds.space[3],
    height: ds.space[3],
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
            <Box ml={ds.space.mul(0, 9)}>
              <Tooltip title={item.type}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                  {truncateText(item.type || '-', 15)}
                </Typography>
              </Tooltip>
              <Typography sx={{ color: 'var(--ds-brand-500)', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                {item.count || '-'}
              </Typography>
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
          color: 'var(--ds-brand-400)',
          gap: 'var(--ds-space-6)',
          marginLeft: 'var(--ds-space-4)',
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
        <Box mr={ds.space[3]} minWidth={ds.space.mul(0, 20)}>
          <Text value={node ? 'Node' : 'Pods'} secondaryText sx={{ fontWeight: 'var(--ds-font-weight-medium)' }} />
          <Text value={total || '-'} sx={{ fontSize: 'var(--ds-text-heading)', fontWeight: 'var(--ds-font-weight-semibold)' }} />
        </Box>
      }
      <Divider sx={{ height: ds.space[5], stroke: 'var(--ds-brand-300)' }} orientation='vertical' variant='middle' />
      {node ? <RenderNodes /> : <RenderPods />}
    </Box>
  );
}

export default KubernetesNodePodStatus;
