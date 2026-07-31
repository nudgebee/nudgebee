import React, { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { formatDurationInTrace } from 'src/utils/common';
import Tooltip from '@ui/Tooltip';
import { ds } from '@utils/colors';

/**
 * Parses an explicit runtime (nanoseconds) from a tool-call metadata JSON blob.
 * Returns null when absent or malformed so callers fall back to timestamps.
 * @param {string|Object} metadata
 * @returns {number|null}
 */
const parseDurationNs = (metadata) => {
  if (!metadata) {
    return null;
  }
  try {
    const meta = typeof metadata === 'string' ? JSON.parse(metadata) : metadata;
    const ns = meta?.duration_ns;
    if (typeof ns === 'number' && ns > 0) {
      return ns;
    }
  } catch {
    // Malformed metadata — fall back to the timestamp diff.
  }
  return null;
};

/**
 * Duration component for displaying a tool call's runtime.
 * Prefers an explicit `duration_ns` carried in `metadata` (e.g. for steps
 * discovered by polling a subprocess, where the row-timestamp diff reflects poll
 * cadence rather than real runtime); otherwise computes `updatedAt - createdAt`.
 * When `live` is set (row still in_progress), ignores `updatedAt`/`metadata` — both
 * are stale until the row completes — and instead ticks a `createdAt`-anchored clock
 * every second, so the badge counts up in real time instead of reading "0ns" for the
 * entire run and jumping to the true total only once it finishes.
 * @param {Object} props - Component props
 * @param {string} props.createdAt - ISO timestamp string for creation time
 * @param {string} props.updatedAt - ISO timestamp string for update time
 * @param {string|Object} props.metadata - Tool-call metadata JSON (may carry `duration_ns`)
 * @param {boolean} props.live - Tick from createdAt instead of the static updatedAt diff
 * @param {Object} props.sx - Additional styles to apply to the container
 * @returns {React.ReactElement} Duration component
 */
const Duration = ({ createdAt, updatedAt, metadata, live = false, sx = {} }) => {
  const isLive = live && !!createdAt;
  const [liveElapsedMs, setLiveElapsedMs] = useState(0);

  useEffect(() => {
    if (!isLive) {
      return undefined;
    }

    const startTime = new Date(createdAt).getTime();
    const updateElapsed = () => {
      setLiveElapsedMs(Math.max(0, Date.now() - startTime));
    };

    updateElapsed(); // Set initial value immediately
    const timerId = setInterval(updateElapsed, 1000);

    return () => clearInterval(timerId);
  }, [isLive, createdAt]);

  // formatDurationInTrace expects nanoseconds when isInSeconds=false.
  let nanos;
  if (isLive) {
    nanos = liveElapsedMs * 1000000;
  } else {
    nanos = parseDurationNs(metadata);
    if (nanos == null) {
      // Date subtraction returns milliseconds; convert to nanoseconds (1ms = 1,000,000ns).
      const millisDifference = new Date(updatedAt) - new Date(createdAt);
      if (isNaN(millisDifference) || millisDifference < 0) {
        return null;
      }
      nanos = millisDifference * 1000000;
    }
  }

  const duration = formatDurationInTrace(nanos, false);

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
  metadata: PropTypes.oneOfType([PropTypes.string, PropTypes.object]),
  live: PropTypes.bool,
  sx: PropTypes.object,
};

export default Duration;
