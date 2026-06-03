import { IconButton } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const CustomIconButton = ({
  id = '',
  sx = {},
  children,
  onClick,
  variant,
  size,
  isDisabled = false,
  tooltip,
  tooltipPlacement = 'bottom',
  tooltipStyle,
  tooltipClassName,
}) => {
  const buttonStyle = {
    ...sx,
    fontWeight: 600,
    fontFamily: 'Roboto',
    borderRadius: '6px',
    fontSize: '0.875rem',
  };
  switch (variant) {
    case 'primary':
      buttonStyle.color = colors.text.white;
      buttonStyle.background = colors.background.primaryDark;
      buttonStyle.border = `2px solid ${colors.border.primary}`;
      buttonStyle[':hover'] = {
        border: `2px solid ${colors.border.tertiaryDisabledText}`,
        color: colors.text.primary,
        background: colors.background.white,
      };
      break;

    case 'outline':
      buttonStyle.color = colors.text.secondary;
      buttonStyle.background = colors.background.white;
      buttonStyle.border = '1px solid #e2e2e2c4';
      buttonStyle.boxShadow = '0 4px 4px rgba(0, 0, 0, 0.04)';
      buttonStyle[':hover'] = {
        backgroundColor: colors.background.tertiaryLightest,
        color: colors.text.secondary,
      };
      break;

    case 'secondary':
      buttonStyle.color = colors.text.tertiary;
      buttonStyle.background = colors.background.white;
      buttonStyle.border = '1px solid #e2e2e2c4';
      buttonStyle.boxShadow = '0 4px 4px rgba(0, 0, 0, 0.04)';
      buttonStyle[':hover'] = {
        backgroundColor: colors.background.tertiaryLightest,
        color: colors.text.secondary,
      };
      break;
    case 'iconButton':
      buttonStyle.borderRadius = '4px';
      buttonStyle.border = `0.5px solid ${colors.border.secondary}`;
      buttonStyle.fontSize = '0.875rem';
      buttonStyle.background = colors.background.white;
      buttonStyle['&:hover'] = {
        border: `0.5px solid ${colors.border.primary}`,
      };
      break;
    case 'no-border-white':
      buttonStyle.borderRadius = '6px';
      buttonStyle.padding = '8px';
      buttonStyle.border = '0px';
      buttonStyle.fontSize = '0.875rem';
      buttonStyle.background = colors.background.white;
      buttonStyle['&:hover'] = {
        border: '0px',
      };
      break;
  }

  switch (size) {
    case 'xsmall':
      buttonStyle.lineHeight = '8px';
      buttonStyle.padding = '6px 16px';
      buttonStyle.fontSize = '10px';
      break;
    case 'small':
      buttonStyle.lineHeight = '16px';
      buttonStyle.padding = '8px 20px';
      buttonStyle.fontSize = '14px';
      break;
    case 'large':
      buttonStyle.lineHeight = '20px';
      buttonStyle.padding = '8px 24px';
      buttonStyle.fontSize = '14px';
      break;
  }

  if (isDisabled) {
    buttonStyle['&:disabled'] = {
      color: colors.text.tertiary,
      background: colors.background.tertiaryLight,
      border: `0.5px solid ${colors.border.vertical}`,
    };
  }

  const iconButton = (
    <IconButton id={`common-${id}`} variant='outlined' sx={buttonStyle} onClick={onClick} disabled={isDisabled} className='custom_icon_button'>
      {children}
    </IconButton>
  );

  if (tooltip) {
    return (
      <Tooltip title={tooltip} placement={tooltipPlacement} tooltipStyle={tooltipStyle} tooltipClassName={tooltipClassName}>
        {iconButton}
      </Tooltip>
    );
  }

  return iconButton;
};

CustomIconButton.propTypes = {
  id: PropTypes.string,
  sx: PropTypes.object,
  children: PropTypes.node.isRequired,
  onClick: PropTypes.func.isRequired,
  variant: PropTypes.string,
  size: PropTypes.oneOf(['small', 'xsmall', 'medium', 'large']),
  isDisabled: PropTypes.bool,
  tooltip: PropTypes.string,
  tooltipPlacement: PropTypes.string,
  tooltipStyle: PropTypes.object,
  tooltipClassName: PropTypes.string,
};

export default CustomIconButton;
