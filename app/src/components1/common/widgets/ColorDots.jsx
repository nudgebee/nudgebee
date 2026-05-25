import React from 'react';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

function ColorDots({ severity, active = false }) {
  let backgroundColor;
  switch (severity.toLowerCase()) {
    case 'highest':
      backgroundColor = colors.highest;
      break;
    case 'high':
      backgroundColor = colors.high;
      break;
    case 'medium':
      backgroundColor = colors.medium;
      break;
    case 'low':
      backgroundColor = colors.low;
      break;
    case 'lowest':
      backgroundColor = colors.lowest;
      break;
    case 'open':
      backgroundColor = colors.open;
      break;
    case 'to do':
      backgroundColor = colors.toDo;
      break;
    case 'in progress':
      backgroundColor = colors.inProgress;
      break;
    case 'done':
      backgroundColor = colors.done;
      break;
    case 'critical':
      backgroundColor = colors.critical;
      break;
    default:
      backgroundColor = colors.black;
  }
  return <Box sx={{ height: '38px', width: active ? '7px' : '4px', borderRadius: '2px', backgroundColor, transition: 'width 0.2s ease' }} />;
}

ColorDots.propTypes = {
  severity: PropTypes.string.isRequired,
  active: PropTypes.bool,
};

export default ColorDots;
