import { Box, Typography } from '@mui/material';
import { styled } from '@mui/material/styles';
import { useRef, useEffect, useState } from 'react';
import CopyableText from '@common/CopyableText';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import MarkDowns from '@common/MarkDowns';
import CustomTooltip from '@common/CustomTooltip';

const AutoEllipsisTypography = styled(Typography, {
  shouldForwardProp: (prop) => prop !== 'lineClamp',
})(({ lineClamp = 1 }) => ({
  display: '-webkit-box',
  WebkitLineClamp: lineClamp,
  WebkitBoxOrient: 'vertical',
  overflow: 'hidden',
  wordBreak: 'break-all',
}));

const Text = ({
  copyableTooltip = false,
  showAutoEllipsis = false,
  lineClamp = 1,
  requiredToolTip = true,
  value,
  defaultVal = '-',
  sx = {},
  tooltipClassName = '',
  format = 'text',
  minLength = 5,
  secondaryText = false,
  ...props
}) => {
  const textRef = useRef(null);
  const [isOverflowing, setIsOverflowing] = useState(false);

  let updatedValue = value || defaultVal;

  let toolTip = '';

  useEffect(() => {
    const checkOverflow = () => {
      if (!textRef.current || !updatedValue) {
        setIsOverflowing(false);
        return;
      }

      const element = textRef.current;

      if (showAutoEllipsis && updatedValue.length >= minLength) {
        const isTextOverflowing = element.scrollHeight > element.clientHeight;
        setIsOverflowing(isTextOverflowing);
      } else {
        setIsOverflowing(false);
      }
    };

    const resizeObserver = new ResizeObserver(() => {
      checkOverflow();
    });

    if (textRef.current) {
      resizeObserver.observe(textRef.current);
      setTimeout(checkOverflow, 0);
    }

    return () => {
      resizeObserver.disconnect();
    };
  }, [updatedValue, showAutoEllipsis]);

  if (showAutoEllipsis && updatedValue) {
    if (isOverflowing) {
      toolTip = value;
    }
  }

  if (copyableTooltip) {
    toolTip = (
      <CopyableText copyableText={value} iconColor='white' format={format}>
        {value}
      </CopyableText>
    );
  }

  const textColor = secondaryText ? colors.text.secondaryDark : props.color ? props.color : colors.text.secondary;

  const textSx = {
    fontSize: secondaryText ? 'var(--ds-text-small)' : 'var(--ds-text-body)',
    fontWeight: 400,
    color: textColor,
    ...sx,
  };

  let TextComponent;
  if (showAutoEllipsis) {
    TextComponent = AutoEllipsisTypography;
  } else {
    TextComponent = Typography;
  }

  const textElement =
    format === 'markdown' && !requiredToolTip ? (
      <MarkDowns
        data={updatedValue}
        sx={{
          ...textSx,
          width: 'auto',
          maxHeight: 'auto',
          overflowY: 'auto',
        }}
      />
    ) : (
      <TextComponent {...props} sx={textSx} ref={textRef} {...(showAutoEllipsis ? { lineClamp } : {})}>
        {updatedValue}
      </TextComponent>
    );

  return requiredToolTip && toolTip ? (
    <CustomTooltip title={toolTip} tooltipClassName={tooltipClassName} {...props}>
      <Box>{textElement}</Box>
    </CustomTooltip>
  ) : (
    <Box>{textElement}</Box>
  );
};

export default Text;

Text.propTypes = {
  copyableTooltip: PropTypes.bool,
  showAutoEllipsis: PropTypes.bool,
  lineClamp: PropTypes.number,
  requiredToolTip: PropTypes.bool,
  value: PropTypes.any,
  defaultVal: PropTypes.any,
  sx: PropTypes.object,
  secondaryText: PropTypes.bool,
  color: PropTypes.string,
  tooltipClassName: PropTypes.string,
  format: PropTypes.string,
};
