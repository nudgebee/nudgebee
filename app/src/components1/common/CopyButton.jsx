import { IconButton } from '@mui/material';
import React, { useState } from 'react';
import FileCopyIcon from '@mui/icons-material/FileCopy';
import CheckIcon from '@mui/icons-material/Check';
import CustomTooltip from './CustomTooltip';
import { colors } from 'src/utils/colors';

const CopyButton = ({ onClick, sx }) => {
  const [isCopied, setIsCopied] = useState(false);

  const handleClick = (e) => {
    if (onClick) {
      onClick(e);
    }
    setIsCopied(true);
    setTimeout(() => {
      setIsCopied(false);
    }, 2000);
  };

  return (
    <CustomTooltip title={isCopied ? 'Copied!' : 'Copy'}>
      <IconButton
        aria-label={isCopied ? 'Copied' : 'Copy to clipboard'}
        sx={{
          ...sx,
          borderRadius: 'var(--ds-radius-sm)',
          border: `0.5px solid ${colors.border.secondary}`,
          height: '32px',
          width: '32px',
          background: 'var(--ds-background-100)',
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          cursor: onClick ? 'pointer' : 'default',
          '&:hover': {
            border: `0.5px solid ${colors.border.primary}`,
          },
        }}
        onClick={handleClick}
      >
        {isCopied ? <CheckIcon sx={{ fontSize: '1.25rem', color: colors.text.success }} /> : <FileCopyIcon sx={{ fontSize: '1.25rem' }} />}
      </IconButton>
    </CustomTooltip>
  );
};

export default CopyButton;
