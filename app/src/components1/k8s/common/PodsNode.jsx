import { Box } from '@mui/material';
import React from 'react';
import ValueWithHeading from './ValueWithHeading';

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
        gap: '4px',
      }}
    >
      <Box sx={{ display: 'flex', gap: forWorkload ? '8px' : '20px', marginBottom: '8px' }}>
        <ValueWithHeading forWorkload={forWorkload} forCostSummary iconColor='#29CC57' heading='Scheduled' value={scheduled} hideLogo />
        <ValueWithHeading forWorkload={forWorkload} forCostSummary iconColor='#90DFAB' heading='Unscheduled' value={unScheduled} hideLogo />
      </Box>

      <Box
        sx={{
          display: 'flex',
          overflow: 'hidden',
          width: forWorkload ? '146px' : '188px',
          height: '9px',
          borderRadius: '14px',
        }}
      >
        <Box
          sx={{
            height: '100%',
            backgroundColor: '#29CC57',
            width: scheduledPercentage,
          }}
        />
        <Box
          sx={{
            height: '100%',
            backgroundColor: '#90DFAB',
            width: unScheduledPercentage,
          }}
        />
      </Box>
    </Box>
  );
};

export default PodsNode;
