import React from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { formatDurationInTrace } from 'src/utils/common';
import Tooltip from '@components1/ds/Tooltip';
import { ds } from '@utils/colors';

/**
 * Duration component for displaying the time difference between two timestamps
 * @param {Object} props - Component props
 * @param {string} props.createdAt - ISO timestamp string for creation time
 * @param {string} props.updatedAt - ISO timestamp string for update time
 * @param {Object} props.sx - Additional styles to apply to the container
 * @returns {React.ReactElement} Duration component
 */
const Duration = ({ createdAt, updatedAt, sx = {} }) => {
  const createdAtDate = new Date(createdAt);
  const updatedAtDate = new Date(updatedAt);
  // Date subtraction returns milliseconds, but formatDurationInTrace expects nanoseconds when isInSeconds=false
  // Convert milliseconds to nanoseconds (1ms = 1,000,000ns)
  const millisDifference = updatedAtDate - createdAtDate;

  // Validate that millisDifference is a valid number
  if (isNaN(millisDifference) || millisDifference < 0) {
    return null;
  }

  const duration = formatDurationInTrace(millisDifference * 1000000, false);

  const durationNumber = Number(duration.replace(/\D/g, ''));

  // Check if duration is valid
  if (!duration || isNaN(durationNumber)) {
    return null;
  }

  return (
    <Tooltip title={'Time taken'}>
      <Box
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: ds.space[1],
          ...sx,
        }}
      >
        <svg width='10' height='10' viewBox='0 0 24 24' fill={'var(--ds-gray-500)'} aria-hidden='true' focusable='false'>
          <path d='M11.99 2C6.47 2 2 6.48 2 12s4.47 10 9.99 10C17.52 22 22 17.52 22 12S17.52 2 11.99 2zM12 20c-4.42 0-8-3.58-8-8s3.58-8 8-8 8 3.58 8 8-3.58 8-8 8z' />
          <path d='M12.5 7H11v6l5.25 3.15.75-1.23-4.5-2.67z' />
        </svg>
        <Typography
          sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-500)', lineHeight: 1 }}
        >
          {duration}
        </Typography>
      </Box>
    </Tooltip>
  );
};

Duration.propTypes = {
  createdAt: PropTypes.string,
  updatedAt: PropTypes.string,
  sx: PropTypes.object,
};

export default Duration;
