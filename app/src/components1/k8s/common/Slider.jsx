import * as React from 'react';
import PropTypes from 'prop-types';
import Slider from '@mui/material/Slider';
import { styled } from '@mui/material/styles';
import Tooltip from '@mui/material/Tooltip';
import { Box, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';

const iOSBoxShadow = '0 3px 1px rgba(0,0,0,0.1),0 4px 8px rgba(0,0,0,0.13),0 0 0 1px rgba(0,0,0,0.02)';

const IOSSlider = styled(Slider)(({ theme }) => ({
  color: colors.text.iosSlider,
  height: 7,
  padding: '15px 0',
  '& .MuiSlider-thumb': {
    position: 'relative',
    height: 18,
    width: 18,
    backgroundColor: colors.background.white,
    boxShadow: '0 0 2px 0px rgba(0, 0, 0, 0.1)',
    border: `1px solid ${colors.primary}`,
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
    color: colors.primary,
    fontSize: 10,
    fontWeight: 600,
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
    height: 7,
    backgroundColor: colors.primary,
  },
  '& .MuiSlider-rail': {
    opacity: 0.5,
    boxShadow: 'inset 0px 0px 4px -2px #000',
    backgroundColor: colors.iconColor,
    height: 7,
  },
  ...theme.applyStyles('dark', {
    color: colors.text.iosSliderDark,
  }),
}));

const CustomTooltip = styled(({ className, ...props }) => <Tooltip {...props} classes={{ popper: className }} />)(({ _theme }) => ({
  '& .MuiTooltip-tooltip': {
    backgroundColor: 'white',
    color: 'rgba(0, 0, 0, 0.87)',
    fontSize: 16,
    padding: '10px 14px',
    boxShadow: '0px 2px 6px rgba(0, 0, 0, 0.15)',
    border: '1px solid rgba(0, 0, 0, 0.1)',
    maxWidth: 'none',
  },
  '& .MuiTooltip-arrow': {
    color: 'white',
    '&::before': {
      border: '1px solid rgba(0, 0, 0, 0.1)',
    },
  },
}));

export default function CustomizedSlider({
  name,
  defaultValue = 0,
  marks = [],
  width = 320,
  isLoading = false,
  onChange,
  min = 0,
  max = 100,
  step = 1,
  value,
  paddingTop = '10px',
  paddingLeft = '10px',
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
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Typography
          sx={{
            fontSize: '14px',
            color: '#111827',
            whiteSpace: 'nowrap',
            marginRight: '3px',
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
            <CustomTooltip title={name} placement='top' arrow>
              {slider}
            </CustomTooltip>
          ) : (
            slider
          );
        })()}
        <Typography
          sx={{
            fontSize: '14px',
            color: '#111827',
            whiteSpace: 'nowrap',
            marginLeft: '3px',
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
