import React, { useEffect, useRef, useState } from 'react';
import { Box } from '@mui/material';
import CustomDropdown from './CustomDropdown';

interface AutoRefreshControlsProps {
  callBack: (interval: number) => void;
}

const AutoRefreshControls: React.FC<AutoRefreshControlsProps> = ({ callBack }) => {
  const [interval, setInterval] = useState('5');
  const intervalRef = useRef<number | null>(null);

  const callback2 = () => {
    let interval2 = 0;
    if (typeof interval === 'string') {
      interval2 = parseInt(interval);
    }
    callBack(interval2);
  };

  useEffect(() => {
    if (intervalRef.current) {
      clearInterval(intervalRef.current);
    }
    let interval2 = 0;
    if (typeof interval === 'string') {
      interval2 = parseInt(interval);
    }
    if (interval2 > 0) {
      intervalRef.current = window.setInterval(callback2, interval2 * 1000);
    }
    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [interval, callBack]);

  return (
    <Box display='flex' alignItems='center'>
      <CustomDropdown
        options={[
          {
            label: 'Off',
            value: '0',
          },
          {
            label: 'Live',
            value: '5',
          },
          {
            label: '10s',
            value: '10',
          },
          {
            label: '15s',
            value: '15',
          },
          {
            label: '30s',
            value: '30',
          },
          {
            label: '45s',
            value: '45',
          },
          {
            label: '60s',
            value: '60',
          },
        ]}
        minWidth='50px'
        label='Refresh'
        onChange={(e) => setInterval(e.target.value)}
        value={interval}
      />
    </Box>
  );
};

export default AutoRefreshControls;
