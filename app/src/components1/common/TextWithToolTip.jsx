import React from 'react';
import Typography from '@mui/material/Typography';
import CustomTooltip from '@components1/common/CustomTooltip';

const stripMarkdown = (text) => {
  if (!text) return text;
  return text
    .replace(/^#{1,6}\s+/gm, '') // headings (start of line only)
    .replace(/\*\*([^*]*)\*\*/g, '$1') // bold
    .replace(/__([^_]*)__/g, '$1') // bold (underscores)
    .replace(/\*([^*]*)\*/g, '$1') // italic
    .replace(/\b_([^_]*)_\b/g, '$1') // italic (underscores, word boundary)
    .replace(/`([^`]*)`/g, '$1') // inline code
    .replace(/!\[[^\]]{0,200}\]\([^)]{0,2000}\)/g, '') // images (before links)
    .replace(/\[([^\]]{0,200})\]\([^)]{0,2000}\)/g, '$1') // links [text](url)
    .replace(/\n+/g, ' ') // newlines to spaces
    .replace(/\s+/g, ' ') // collapse whitespace
    .trim();
};

const TextWithToolTip = ({ text, size = 30, enableTooltip = true, markdown = false, lines = 1 }) => {
  const displayText = markdown ? stripMarkdown(text) : text;

  if (lines > 1) {
    return (
      <CustomTooltip title={enableTooltip && displayText ? displayText : ''}>
        <Typography
          sx={{
            color: 'var(--ds-brand-500)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-regular)',
            overflow: 'hidden',
            display: '-webkit-box',
            WebkitLineClamp: lines,
            WebkitBoxOrient: 'vertical',
            wordBreak: 'break-word',
          }}
        >
          {displayText || '-'}
        </Typography>
      </CustomTooltip>
    );
  }

  const trimmedText = displayText?.length > size ? `${displayText.slice(0, size)}...` : displayText;
  const tooltipText = enableTooltip && displayText?.length > size ? displayText : '';

  return (
    <CustomTooltip title={tooltipText}>
      <Typography
        sx={{
          color: 'var(--ds-brand-500)',
          fontSize: 'var(--ds-text-body)',
          fontWeight: 'var(--ds-font-weight-regular)',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {trimmedText || '-'}
      </Typography>
    </CustomTooltip>
  );
};

export default TextWithToolTip;
