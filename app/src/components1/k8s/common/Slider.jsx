import * as React from 'react';
import PropTypes from 'prop-types';
import Slider from '@mui/material/Slider';
import { styled } from '@mui/material/styles';
import Tooltip from '@components1/ds/Tooltip';
import { Box, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';

const iOSBoxShadow = '0 3px 1px rgba(0,0,0,0.1),0 4px 8px rgba(0,0,0,0.13),0 0 0 1px rgba(0,0,0,0.02)';

const IOSSlider = styled(Slider)(({ theme }) => ({
  color: ds.blue[600],
  height: ds.space.mul(0, 3),
  padding: 'var(--ds-space-4) 0',
  '& .MuiSlider-thumb': {
    position: 'relative',
    height: ds.space.mul(0, 9),
    width: ds.space.mul(0, 9),
    backgroundColor: ds.background[100],
    boxShadow: '0 0 2px 0px rgba(0, 0, 0, 0.1)',
    border: `1px solid ${ds.blue[500]}`,
    '&:focus, &:hover, &.Mui-active': {
      boxShadow: '0px 0px 3px 1px rgba(0, 0, 0, 0.2) inset',
      '@media (hover: none)': {
        boxShadow: iOSBoxShadow,
      },
    },
    '&:before': {
      boxShadow: '0px 0px 1px 0px rgba(0,0,0,0.2), 0px 0px 0px 0px rgba(0,0,0,0.14), 0px 0px 1px 0px rgba(0,0,0,0.12)',
    },
  },
  '& .MuiSlider-valueLabel': {
    position: 'absolute',
    top: '50%',
    left: '50%',
    transform: 'translate(-50%, -50%) scale(1)',
    transformOrigin: 'center',
    background: 'transparent',
    color: ds.blue[500],
    fontSize: ds.text.caption,
    fontWeight: 'var(--ds-font-weight-semibold)',
    padding: 0,
    '&::before': {
      display: 'none',
    },
    transition: 'none',
  },
  '& .MuiSlider-valueLabel.MuiSlider-valueLabelOpen': {
    transform: 'translate(-50%, -50%)', // lock position on hover
  },
  '& .MuiSlider-track': {
    border: 'none',
    height: ds.space.mul(0, 3),
    backgroundColor: ds.blue[500],
  },
  '& .MuiSlider-rail': {
    opacity: 0.5,
    boxShadow: 'inset 0px 0px 4px -2px var(--ds-gray-700)',
    backgroundColor: ds.gray[300],
    height: ds.space.mul(0, 3),
  },
  ...theme.applyStyles('dark', {
    color: ds.blue[500],
  }),
}));

export default function CustomizedSlider({
  name,
  defaultValue = 0,
  marks = [],
  width = ds.space.mul(0, 160),
  isLoading = false,
  onChange,
  min = 0,
  max = 100,
  step = 1,
  value,
  paddingTop = ds.space.mul(0, 5),
  paddingLeft = ds.space.mul(0, 5),
  enableTooltip = true,
  unit = '',
}) {
  const [localValue, setLocalValue] = React.useState(defaultValue);

  const isControlled = value !== undefined;
  const currentValue = isControlled ? value : localValue;

  const handleChange = (_event, newValue) => {
    if (!isControlled) {
      setLocalValue(newValue);
    }
    if (onChange) {
      onChange(newValue);
    }
  };

  return (
    <Box sx={{ width: width, paddingTop: paddingTop, paddingLeft: paddingLeft, opacity: isLoading ? 0.6 : 1 }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            color: 'var(--ds-brand-700)',
            whiteSpace: 'nowrap',
            marginRight: 'var(--ds-space-1)',
          }}
        >
          {min}
          {unit}
        </Typography>
        {(() => {
          const slider = (
            <IOSSlider
              aria-label='ios slider'
              value={currentValue}
              onChange={handleChange}
              valueLabelDisplay='auto'
              disabled={isLoading}
              marks={marks}
              min={min}
              max={max}
              step={step}
            />
          );
          return enableTooltip ? (
            <Tooltip title={name} placement='top'>
              {slider}
            </Tooltip>
          ) : (
            slider
          );
        })()}
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            color: 'var(--ds-brand-700)',
            whiteSpace: 'nowrap',
            marginLeft: 'var(--ds-space-1)',
          }}
        >
          {max}
          {unit}
        </Typography>
      </Box>
    </Box>
  );
}

CustomizedSlider.propTypes = {
  name: PropTypes.string,
  width: PropTypes.number,
  isLoading: PropTypes.bool,
  onChange: PropTypes.func,
  marks: PropTypes.array,
  defaultValue: PropTypes.number,
  min: PropTypes.number,
  max: PropTypes.number,
  step: PropTypes.number,
  value: PropTypes.number,
  paddingTop: PropTypes.string,
  paddingLeft: PropTypes.string,
  enableTooltip: PropTypes.bool,
  unit: PropTypes.string,
};
