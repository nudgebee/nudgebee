import React from 'react';
import { Box, Typography } from '@mui/material';
import TrendArrowPercentage from './widgets/TrendArrowPercentage';
import Currency from './format/Currency';

const CostView = ({ data }) => {
  if (!data || data.length === 0) {
    return <Typography sx={{ color: '#9F9F9F', fontSize: '12px' }}>No cost data provided.</Typography>;
  }

  return (
    <Box sx={{ display: 'flex', gap: '17px', justifyContent: 'space-between' }}>
      {data.map((entry, index) => (
        <Box key={index}>
          <Typography sx={{ color: '#9F9F9F', fontSize: '12px', fontWeight: 500 }}>{entry?.name}</Typography>
          <Box sx={{ display: 'flex', alignItems: 'center' }}>
            <Currency sx={{ fontSize: '16px', fontWeight: 500 }} value={entry?.cost} />
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
