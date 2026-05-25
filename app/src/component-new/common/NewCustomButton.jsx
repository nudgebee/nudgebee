/**
 * NewCustomButton (a.k.a. CustomButton) — domain composition for app-styled buttons.
 *
 * For NEW buttons, prefer `{ Button } from '@components1/ds/Button'` (5 tones,
 * 4 sizes, declarative composition variants, --ds-* tokens, proper `loading`
 * state, strict aria-label for icon-only).
 *
 * For existing call sites that depend on any of the following, keep using
 * NewCustomButton — `ds/Button` does not cover them:
 *   - Variants `tertiary` (white+blue), `blueButton` (hardcoded rgb),
 *     `iconButton` (square chrome), `lightButton` (gray+shadow). These are
 *     app-specific brand variants outside the DS palette.
 *   - Sizes `Medium` (36px) and `xLarge` (44px). `ds/Button` caps at 40px (lg).
 *   - Both `startIcon` AND `endIcon` simultaneously (`ds/Button` has only one
 *     `icon` slot).
 *   - Built-in `showTooltip` + `toolTipTitle` + `tooltipPlacement` wrapping.
 *     Per spec callers should compose `<Tooltip><Button/></Tooltip>` themselves
 *     — but ~160 sites already lean on the inline option here.
 *   - `startIcon`/`endIcon` accepting a string-path (auto-wrapped via SafeIcon)
 *     in addition to ReactNode.
 *
 * Previously deprecated 2026-05-07 → soft-demoted to domain composition
 * 2026-05-07. Hard migration to `ds/Button` was rejected: 160 importers, ~70%
 * mappable cleanly but the long tail of brand variants + tooltip wrapping +
 * dual-icon support would require either site-by-site refactor or DS-feature
 * builds. The two primitives co-exist: `ds/Button` for DS-clean buttons,
 * `NewCustomButton` for the existing app-styled call sites.
 */
import { Button, Tooltip, tooltipClasses, CircularProgress } from '@mui/material';
import PropTypes from 'prop-types';
import styled from '@emotion/styled';
// TODO(ds-migration): SafeIcon is a leaf-utility not yet mirrored into component-new/; resolve to the legacy path until it is.
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import React from 'react';

const CustomTooltip = styled(({ className, ...props }) => <Tooltip {...props} classes={{ tooltip: className }} />)(({ marginLeft }) => ({
  [`&.${tooltipClasses.tooltip}`]: {
    backgroundColor: 'black',
    minWidth: '20px',
    padding: '8px 12px',
    ...(marginLeft && { marginLeft: '-12px !important' }),
  },
}));

const legacyVariants = ['blueButton', 'iconButton', 'lightButton', 'link'];

const CustomButton = ({
  showTooltip = false,
  toolTipTitle = '',
  tooltipPlacement = 'bottom-end',
  startIcon,
  text,
  variant = 'primary',
  onClick,
  disabled = false,
  sx = {},
  id = 'customButton',
  size = 'Small',
  type = '',
  endIcon,
  loading = false,
  className = '',
  marginLeft = false,
  label = '',
}) => {
  const buttonComponent = (
    <Button
      type={type}
      sx={{
        textTransform: 'unset',
        boxShadow: 'unset',
        color: colors.text.black,
        fontSize: '0.875rem',
        fontWeight: 500,
        minWidth: 'auto',
        padding:
          !text && startIcon && size === 'xSmall'
            ? '5px 8px'
            : !text && startIcon && size === 'Small'
            ? '5px 9px'
            : !text && startIcon && size === 'Medium'
            ? '5px 14px'
            : !text && startIcon && size === 'Large'
            ? '5px 13px'
            : !text && startIcon && size === 'xLarge'
            ? '5px 15px'
            : 'auto',

        '& .MuiButton-startIcon': {
          marginRight: !text ? '0px' : '5px',
          marginLeft: '0px',
        },
        ...buttonStyles[variant],
        ...(legacyVariants.includes(variant) ? {} : buttonSizes[size]),
        ...sx,
      }}
      onClick={onClick}
      disabled={disabled || loading}
      id={id}
      startIcon={startIcon && (React.isValidElement(startIcon) ? startIcon : <SafeIcon src={startIcon} alt={text} width={18} height={18} />)}
      endIcon={endIcon && (React.isValidElement(endIcon) ? endIcon : <SafeIcon src={endIcon} alt={text} width={18} height={18} />)}
      className={className}
      aria-label={label || undefined}
    >
      {loading ? <CircularProgress size={16} sx={{ color: colors.text.white, marginRight: '10px' }} /> : ''}
      {text}
    </Button>
  );
  return showTooltip ? (
    <CustomTooltip title={toolTipTitle} placement={tooltipPlacement} marginLeft={marginLeft}>
      <span>{buttonComponent}</span>
    </CustomTooltip>
  ) : (
    buttonComponent
  );
};

CustomButton.propTypes = {
  toolTipTitle: PropTypes.any,
  startIcon: PropTypes.any,
  text: PropTypes.any,
  variant: PropTypes.oneOf(['primary', 'secondary', 'tertiary', 'blueButton', 'iconButton', 'lightButton', 'link']),
  onClick: PropTypes.func,
  disabled: PropTypes.bool,
  sx: PropTypes.object,
  id: PropTypes.string,
  showTooltip: PropTypes.bool,
  size: PropTypes.oneOf(['xSmall', 'Small', 'Medium', 'Large', 'xLarge']),
  endIcon: PropTypes.any,
  type: PropTypes.string,
  tooltipPlacement: PropTypes.string,
  loading: PropTypes.bool,
  className: PropTypes.string,
  marginLeft: PropTypes.bool,
  label: PropTypes.string,
};

export default CustomButton;

const buttonStyles = {
  primary: {
    backgroundColor: colors.button.primary,
    borderRadius: '6px',
    border: '0px',
    color: colors.button.primaryText,
    fontWeight: '500',
    boxShadow: '0px 1px 2px 0px #1018280D',
    padding: '0px 14px',
    '& img, svg': {
      filter: 'brightness(0) saturate(100%) invert(100%) sepia(0%) saturate(7489%) hue-rotate(351deg) brightness(116%) contrast(98%)',
    },
    '&:hover': {
      backgroundColor: colors.button.primaryHover,
      border: '0px !important',
    },
    '&.Mui-disabled': {
      border: '0px !important',
      backgroundColor: colors.button.primaryDisabled,
      color: colors.button.primaryDisabledText,
      boxShadow: '0px 1px 2px 0px #1018280D',
    },
  },
  secondary: {
    border: `0.5px solid ${colors.button.secondaryBorder}`,
    backgroundColor: colors.button.secondary,
    borderRadius: '6px',
    color: colors.button.secondaryText,
    boxShadow: '0px 1px 2px 0px #1018280D',
    fontWeight: '500',

    '& img, svg': {
      filter: 'brightness(0) saturate(100%) invert(39%) sepia(100%) saturate(13%) hue-rotate(139deg) brightness(94%) contrast(86%)',
    },
    '&:hover': {
      backgroundColor: colors.button.secondaryHover,
      border: `0.5px solid ${colors.button.secondaryHoverBorder}`,
    },
    '&.Mui-disabled': {
      backgroundColor: colors.button.secondaryDisabled,
      color: colors.button.secondaryDisabledText,
      border: `0.5px solid ${colors.button.secondaryDisabledBorder}`,
      boxShadow: '0px 1px 2px 0px #1018280D',
      '&img, svg': {
        filter: 'brightness(0) saturate(100%) invert(100%) sepia(0%) saturate(131%) hue-rotate(152deg) brightness(87%) contrast(90%)',
      },
    },
  },
  tertiary: {
    border: `0.5px solid ${colors.button.tertiaryBorder}`,
    backgroundColor: colors.button.tertiary,
    borderRadius: '6px',
    color: colors.button.tertiaryText,
    boxShadow: '0px 1px 2px 0px #1018280D',
    fontWeight: '500',
    '& img, svg': {
      filter: 'brightness(0) saturate(100%) invert(59%) sepia(97%) saturate(4436%) hue-rotate(202deg) brightness(100%) contrast(94%)',
    },
    '&:hover': {
      backgroundColor: colors.button.tertiaryHover,
      border: `0.5px solid ${colors.button.secondaryHoverBorder}`,
    },
    '&.Mui-disabled': {
      backgroundColor: colors.button.tertiaryDisabled,
      color: colors.button.tertiaryDisabledText,
      border: `0.5px solid ${colors.button.tertiaryDisabledBorder}`,
      boxShadow: '0px 1px 2px 0px #1018280D',
      '&img, svg': {
        filter: 'brightness(0) saturate(100%) invert(72%) sepia(53%) saturate(2462%) hue-rotate(190deg) brightness(107%) contrast(118%)',
      },
    },
  },
  blueButton: {
    color: 'white',
    padding: '10px 20px',
    borderRadius: '6px',
    border: '0px',
    backgroundColor: 'rgb(59, 130, 246)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
    cursor: 'pointer',
    minWidth: '64px',
    height: '32px',
    width: 'fit-content',
    lineHeight: '1.30',
    fontSize: '0.875rem',
    textTransform: 'unset',
    '&:hover': {
      boxShadow: 'unset',
      backgroundColor: 'rgb(59, 120, 230)',
    },
  },
  iconButton: {
    borderRadius: '4px',
    padding: '16px',
    border: '0.5px solid #D0D0D0',
    height: '32px',
    minWidth: '27px',
    minHeight: '20px',
    color: '#374151',
    fontSize: '14px',
    fontWeight: 400,
    textTransform: 'unset',
    '&:hover': {
      boxShadow: 'unset',
      borderColor: '#3B82F6',
    },
  },
  lightButton: {
    color: '#374151',
    backgroundColor: '#F8F8F8',
    padding: '7px 10px',
    borderRadius: '6px',
    fontSize: '12px',
    fontWeight: '500',
    textTransform: 'unset',
    boxShadow: '0px 1px 4px 0px #0000002E',
    '&:hover': {
      boxShadow: 'unset',
      backgroundColor: '#B9B9B945',
    },
  },
  link: {
    padding: 0,
    color: '#2358BE',
    fontSize: '12px',
    fontWeight: 500,
    textDecoration: 'underline',
    textTransform: 'unset',
    backgroundColor: 'transparent',
    '&:hover': {
      boxShadow: 'unset',
      backgroundColor: '#EEF3FF99',
    },
  },
};

const buttonSizes = {
  xSmall: {
    height: '28px',
    fontSize: '12px',
    textTransform: 'capitalize',
  },
  Small: {
    height: '32px',
    fontSize: '13px',
    textTransform: 'capitalize',
  },
  Medium: {
    height: '36px',
    fontSize: '14px',
    textTransform: 'capitalize',
  },
  Large: {
    height: '40px',
    fontSize: '20px',
    textTransform: 'capitalize',
  },
  xLarge: {
    height: '44px',
    fontSize: '20px',
    textTransform: 'capitalize',
  },
};
