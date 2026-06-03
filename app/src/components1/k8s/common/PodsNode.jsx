import { Box } from '@mui/material';
import React from 'react';
import ValueWithHeading from './ValueWithHeading';
import { ds } from '@utils/colors';

const PodsNode = ({ node = {}, forWorkload = false }) => {
  const { scheduled, unScheduled } = node;
  const total = (scheduled ?? 0) + (unScheduled ?? 0);

  const scheduledPercentage = `${(scheduled / total) * 100}%`;
  const unScheduledPercentage = `${(unScheduled / total) * 100}%`;

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 'var(--ds-space-1)',
      }}
    >
      <Box sx={{ display: 'flex', gap: forWorkload ? ds.space[2] : ds.space.mul(0, 10), marginBottom: 'var(--ds-space-2)' }}>
        <ValueWithHeading forWorkload={forWorkload} forCostSummary iconColor={ds.green[400]} heading='Scheduled' value={scheduled} hideLogo />
        <ValueWithHeading forWorkload={forWorkload} forCostSummary iconColor={ds.green[300]} heading='Unscheduled' value={unScheduled} hideLogo />
      </Box>

      <Box
        sx={{
          display: 'flex',
          overflow: 'hidden',
          width: forWorkload ? ds.space.mul(0, 73) : ds.space.mul(0, 94),
          height: ds.space[2],
          borderRadius: 'var(--ds-radius-xl)',
        }}
      >
        <Box
          sx={{
            height: '100%',
            backgroundColor: 'var(--ds-green-400)',
            width: scheduledPercentage,
          }}
        />
        <Box
          sx={{
            height: '100%',
            backgroundColor: 'var(--ds-green-300)',
            width: unScheduledPercentage,
          }}
        />
      </Box>
    </Box>
  );
};

export default PodsNode;
