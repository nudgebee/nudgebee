import React, { useState } from 'react';
import { Box, Typography } from '@mui/material';
import CheckIcon from '@mui/icons-material/Check';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import CustomTooltip from './CustomTooltip';
import { CopyIcon } from '@assets';

const TextWithTooltipAndCopy = ({
  value,
  maxSize = 30,
  className = 'text-value',
  tooltipPlacement = 'top',
  copyIconSize = 12,
  showCopyIcon = true,
  sx = {},
}) => {
  const [copied, setCopied] = useState(false);

  // Helper function to copy text to clipboard
  const copyToClipboard = (text) => {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text);
    } else {
      // Fallback for older browsers
      const textArea = document.createElement('textarea');
      textArea.value = text;
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
    }
  };

  const truncatedValue = value && value.length > maxSize ? `${value.substring(0, maxSize)}...` : value;
  const shouldShowTooltip = value && value.length > maxSize;

  const handleCopy = (e) => {
    e.stopPropagation();
    copyToClipboard(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const textComponent = (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', ...sx }}>
      <Typography className={className}>{truncatedValue || '-'}</Typography>
      {showCopyIcon && value && (
        <button
          type='button'
          onClick={handleCopy}
          aria-label={copied ? 'Copied' : 'Copy to clipboard'}
          style={{
            background: 'transparent',
            border: 'none',
            padding: 0,
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          {copied ? (
            <CheckIcon
              sx={{
                width: `${copyIconSize}px`,
                height: `${copyIconSize}px`,
                color: 'green',
              }}
            />
          ) : (
            <SafeIcon
              src={CopyIcon}
              alt=''
              style={{
                width: `${copyIconSize}px`,
                height: `${copyIconSize}px`,
                transition: 'opacity 0.2s ease',
              }}
            />
          )}
        </button>
      )}
    </Box>
  );

  return shouldShowTooltip ? (
    <CustomTooltip title={value} placement={tooltipPlacement}>
      {textComponent}
    </CustomTooltip>
  ) : (
    textComponent
  );
};

TextWithTooltipAndCopy.propTypes = {
  value: PropTypes.string,
  maxSize: PropTypes.number,
  className: PropTypes.string,
  tooltipPlacement: PropTypes.string,
  copyIconSize: PropTypes.number,
  showCopyIcon: PropTypes.bool,
  sx: PropTypes.object,
};

export default TextWithTooltipAndCopy;
