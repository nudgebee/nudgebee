import { CheckRounded as Check } from '@mui/icons-material';
import { Box } from '@mui/material';
import { useState } from 'react';
import CopyIconImge from '@assets/copy-icon.svg';
import PropTypes from 'prop-types';
import MarkDowns from './MarkDowns';
import SafeIcon from './SafeIcon';
import { snackbar } from './snackbarService';

const CopyableText = ({
  children,
  copyableText = '',
  iconColor,
  onCopy,
  sx = {},
  iconPosition = 'start',
  format = 'text',
  showCopyIconOnHover = false,
  iconSize = 12,
  iconOnly = false,
  snackbarMessage = '',
}) => {
  const [copied, setCopied] = useState(false);

  const handleCopy = (event) => {
    event.preventDefault();
    event.stopPropagation();
    if (copied) {
      return;
    }

    if (navigator.clipboard) {
      navigator.clipboard.writeText(copyableText);
    } else {
      copyToClipboard(copyableText);
    }
    setCopied(true);
    onCopy?.(copyableText);
    if (snackbarMessage) {
      snackbar.success(snackbarMessage);
    }
    setTimeout(() => setCopied(false), 2000);
  };

  const iconElement = copied ? (
    <Check
      sx={{
        width: `${iconSize}px`,
        height: `${iconSize}px`,
        color: iconColor || '#22C55E',
        cursor: 'pointer',
      }}
    />
  ) : (
    <SafeIcon src={CopyIconImge} alt='copy' height={iconSize} width={iconSize} style={{ cursor: 'pointer' }} />
  );

  const iconWrapperSx = {
    display: 'flex',
    alignItems: 'center',
    cursor: 'pointer',
    opacity: showCopyIconOnHover ? 0 : 1,
    transition: 'opacity 0.2s ease',
  };

  const textElement = (
    <Box
      className='primaryText'
      sx={{
        ...sx,
        width: '100%',
        minWidth: 0,
        wordBreak: 'break-word',
        overflowWrap: 'break-word',
        '@media (max-width: 1350px)': {
          fontSize: '11px',
        },
      }}
    >
      {format === 'markdown' ? (
        <MarkDowns
          data={children}
          sx={{
            ...sx,
            width: 'auto',
            maxHeight: 'auto',
            overflowY: 'auto',
            '@media (max-width: 1350px)': {
              fontSize: '11px',
            },
          }}
        />
      ) : (
        children
      )}
    </Box>
  );

  return (
    <Box
      component='button'
      type='button'
      tabIndex={0}
      onClick={handleCopy}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          handleCopy(event);
        }
      }}
      sx={{
        background: 'none',
        border: 'none',
        padding: 0,
        cursor: 'pointer',
        font: 'inherit',
        color: 'inherit',
        textAlign: 'inherit',
        display: 'inline-flex',
        alignItems: 'center',
        gap: '5px',
        maxWidth: '100%',
        minWidth: 0,
        ...(showCopyIconOnHover && {
          '&:hover .copy-icon': {
            opacity: 1,
          },
        }),
        '& p': {
          '@media (max-width: 1350px)': {
            fontSize: '12px',
          },
        },
        '& svg': {
          '@media (max-width: 1350px)': {
            height: '11px',
            width: '11px',
          },
        },
      }}
    >
      {iconPosition === 'start' && (
        <Box className='copy-icon' sx={iconWrapperSx}>
          {iconElement}
        </Box>
      )}
      {!iconOnly && textElement}
      {iconPosition === 'end' && (
        <Box className='copy-icon' sx={iconWrapperSx}>
          {iconElement}
        </Box>
      )}
    </Box>
  );
};

CopyableText.propTypes = {
  children: PropTypes.any,
  copyableText: PropTypes.string,
  iconColor: PropTypes.string,
  onCopy: PropTypes.any,
  sx: PropTypes.object,
  iconPosition: PropTypes.oneOf(['start', 'end']),
  showCopyIconOnHover: PropTypes.bool,
  iconSize: PropTypes.number,
  iconOnly: PropTypes.bool,
  snackbarMessage: PropTypes.string,
};

export default CopyableText;

const copyToClipboard = async (text) => {
  let textarea;
  let result;

  try {
    textarea = document.createElement('textarea');
    textarea.setAttribute('readonly', true);
    textarea.setAttribute('contenteditable', true);
    textarea.style.position = 'fixed'; // prevent scroll from jumping to the bottom when focus is set.
    textarea.value = text;

    document.body.appendChild(textarea);

    textarea.focus();
    textarea.select();

    const range = document.createRange();
    range.selectNodeContents(textarea);

    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);

    textarea.setSelectionRange(0, textarea.value.length);
    result = document.execCommand('copy');
  } catch (err) {
    console.error(err);
    result = null;
  } finally {
    document.body.removeChild(textarea);
  }

  if (!result) {
    alert('This action is not supported in this browser');
    return false;
  }
  return true;
};
