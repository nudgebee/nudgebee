/**
 * @deprecated Use <Button tone="ghost" composition="icon-only" icon={<Share/>} aria-label="Share" /> from '@components1/ds/Button' instead.
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
import { Box, Tooltip } from '@mui/material';
import React from 'react';
import SafeIcon from '@components1/common/SafeIcon';
import ShareIcon from '@assets/share-f.svg';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const ShareButton = ({ onClick, width = '32px', height = '32px' }) => {
  return (
    <Tooltip title='Coming Soon'>
      <Box
        onClick={onClick}
        sx={{
          height: height,
          width: width,
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          cursor: 'pointer',
          borderRadius: '6px',
          boxShadow: '0 4px 4px rgba(0, 0, 0, 0.04)',
          border: '1px solid #e2e2e2c4',
          background: colors.background.white,
          '&:hover': {
            backgroundColor: colors.background.tertiaryLightest,
            color: colors.text.secondary,
          },
        }}
      >
        <SafeIcon src={ShareIcon} alt='share icon' />
      </Box>
    </Tooltip>
  );
};

ShareButton.propTypes = {
  onClick: PropTypes.func,
  width: PropTypes.any,
  height: PropTypes.any,
};

export default ShareButton;
