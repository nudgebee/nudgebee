import { Box } from '@mui/material';
import React from 'react';
import CallMergeIcon from '@mui/icons-material/CallMerge';
import PropTypes from 'prop-types';
import Tooltip from '@components1/ds/Tooltip';

const extractPRIdentifier = (url) => {
  if (!url) return url;
  const match = url.match(/\/pull\/(\d+)/);
  if (match) return `#${match[1]}`;
  const parts = url.split('/').filter(Boolean);
  return parts.length > 0 ? `#${parts[parts.length - 1]}` : url;
};

const CustomPRLink = ({ prURL, statusMessage }) => {
  if (!prURL) return null;

  const prId = extractPRIdentifier(prURL);

  return (
    <Tooltip title={statusMessage || 'Open Pull Request'} arrow>
      <Box
        component='a'
        href={prURL}
        target='_blank'
        rel='noopener noreferrer'
        onClick={(e) => e.stopPropagation()}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          backgroundColor: 'var(--ds-green-100)',
          border: '1px solid var(--ds-green-200)',
          borderRadius: 'var(--ds-radius-sm)',
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          textDecoration: 'none',
          cursor: 'pointer',
          mt: 'var(--ds-space-1)',
          transition: 'background-color 120ms ease',
          '&:hover': {
            backgroundColor: 'var(--ds-green-200)',
          },
        }}
      >
        <CallMergeIcon sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-green-600)' }} />
        <Box
          component='span'
          sx={{
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-green-600)',
            lineHeight: 'var(--ds-text-caption-lh)',
          }}
        >
          PR {prId}
        </Box>
      </Box>
    </Tooltip>
  );
};

CustomPRLink.propTypes = {
  prURL: PropTypes.string,
  statusMessage: PropTypes.string,
};

export default CustomPRLink;
