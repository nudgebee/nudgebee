import React, { useState, useRef, useEffect } from 'react';
import { Box, Button, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const ExpandableText = ({ text = '', maxLines = 1, sx = {}, ...props }) => {
  const [isExpanded, setIsExpanded] = useState(false);
  const [hasOverflow, setHasOverflow] = useState(false);
  const textRef = useRef(null);

  useEffect(() => {
    const element = textRef.current;
    if (!element) {
      return;
    }

    const checkOverflow = () => {
      const wasExpanded = isExpanded;
      if (wasExpanded) {
        element.style.display = '-webkit-box';
        element.style.webkitLineClamp = maxLines.toString();
        element.style.webkitBoxOrient = 'vertical';
        element.style.overflow = 'hidden';
      }

      const isOverflowing = element.scrollHeight > element.clientHeight;
      setHasOverflow(isOverflowing);

      if (wasExpanded) {
        element.style.display = '';
        element.style.webkitLineClamp = '';
        element.style.webkitBoxOrient = '';
        element.style.overflow = '';
      }
    };

    const resizeObserver = new ResizeObserver(checkOverflow);
    resizeObserver.observe(element);

    checkOverflow();

    return () => {
      resizeObserver.disconnect();
    };
  }, [text, maxLines, isExpanded]);

  const toggleExpand = (event) => {
    event.preventDefault();
    event.stopPropagation();
    setIsExpanded(!isExpanded);
  };

  const getTextColor = () => {
    if (props.secondaryText) {
      return colors.text.secondaryDark;
    }
    return props.color || colors.text.secondary;
  };

  const getLineClampStyles = () => {
    if (isExpanded) {
      return {};
    }

    return {
      display: '-webkit-box',
      WebkitLineClamp: maxLines,
      WebkitBoxOrient: 'vertical',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
    };
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start' }}>
      <Typography
        ref={textRef}
        sx={{
          fontSize: props.secondaryText ? '12px' : '14px',
          fontWeight: 400,
          color: getTextColor(),
          my: '0px',
          wordBreak: 'break-word',
          lineHeight: 1.4,
          ...getLineClampStyles(),
          ...sx,
        }}
      >
        {text}
      </Typography>

      {hasOverflow && (
        <Button
          onClick={toggleExpand}
          sx={{
            fontSize: '11px',
            color: colors.text.primary,
            fontWeight: 400,
            textTransform: 'capitalize',
            padding: '0px',
            marginTop: '4px',
            minWidth: 'auto',
            alignSelf: 'flex-start',
            display: 'flex',
          }}
        >
          Show {isExpanded ? 'Less' : 'More'}
        </Button>
      )}
    </Box>
  );
};

ExpandableText.propTypes = {
  text: PropTypes.string,
  maxLines: PropTypes.number,
  sx: PropTypes.object,
  secondaryText: PropTypes.bool,
  color: PropTypes.string,
};

export default ExpandableText;
