import React from 'react';
import { Box, Typography } from '@mui/material';
import TrendArrowPercentage from './widgets/TrendArrowPercentage';
import Currency from './format/Currency';

const CostView = ({ data }) => {
  if (!data || data.length === 0) {
    return <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)' }}>No cost data provided.</Typography>;
  }

  return (
    <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', justifyContent: 'space-between' }}>
      {data.map((entry, index) => (
        <Box key={index}>
          <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
            {entry?.name}
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center' }}>
            <Currency sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)' }} value={entry?.cost} />
            {entry?.name === 'Forecast Month' && (
              <TrendArrowPercentage
                sign={data[1].cost > data[2].cost ? 1 : -1}
                value={(Math.abs(data[1].cost - data[2].cost) * 100) / data[1].cost}
              />
            )}
          </Box>
        </Box>
      ))}
    </Box>
  );
};

export default CostView;
