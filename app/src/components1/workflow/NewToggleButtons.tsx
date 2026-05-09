import React from 'react';
import { Box, Button } from '@mui/material';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';

interface ToggleOption {
  value: string;
  label: string;
  icon?: any;
  disabled?: boolean;
}

interface ToggleButtonsProps {
  options: ToggleOption[];
  activeValue: string;
  width?: string;
  size?: 'default' | 'large' | 'sm';
  noShadow?: boolean;
  onChange: (value: string) => void;
}

function getButtonStyles(isActive: boolean, isSmall: boolean) {
  if (isActive && isSmall) {
    return {
      background: colors.background.white,
      color: colors.text.primary,
      boxShadow: '0px 1px 3px rgba(16, 24, 40, 0.1)',
      hoverBackground: colors.background.white,
      iconFilter: 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)',
    };
  }
  if (isActive) {
    return {
      background: colors.background.secondary,
      color: colors.text.white,
      boxShadow: '0px 2px 20px rgba(16, 24, 40, 0.15)',
      hoverBackground: colors.background.secondary,
      iconFilter: 'brightness(0) invert(1)',
    };
  }
  if (isSmall) {
    return {
      background: 'transparent',
      color: colors.text.secondaryDark,
      boxShadow: 'none',
      hoverBackground: colors.background.tertiaryLightestest,
      iconFilter: 'brightness(0) saturate(100%) invert(50%) sepia(0%) hue-rotate(0deg)',
    };
  }
  return {
    background: 'transparent',
    color: colors.text.secondary,
    boxShadow: 'none',
    hoverBackground: 'transparent',
    iconFilter: 'none',
  };
}

const ToggleButtons: React.FC<ToggleButtonsProps> = ({ options, activeValue, width, size = 'default', noShadow, onChange }) => {
  const sizeConfig = {
    default: {
      containerPadding: '6px 8px',
      containerBorderRadius: '8px',
      buttonPadding: '6px 12px',
      buttonFontSize: '13px',
      buttonBorderRadius: '6px',
    },
    large: {
      containerPadding: '0px',
      containerBorderRadius: '12px',
      buttonPadding: '8px 20px',
      buttonFontSize: '16px',
      buttonBorderRadius: '8px',
    },
    sm: {
      containerPadding: '4px',
      containerBorderRadius: '8px',
      buttonPadding: '6px 10px',
      buttonFontSize: '12px',
      buttonBorderRadius: '6px',
    },
  };

  const config = sizeConfig[size];

  const isSmall = size === 'sm';

  return (
    <Box
      sx={{
        display: 'flex',
        backgroundColor: isSmall ? colors.background.tertiaryLightest : 'white',
        borderRadius: config.containerBorderRadius,
        border: isSmall ? 'none' : '1px solid #dee2e6',
        boxShadow: noShadow || isSmall ? 'none' : '0px 4px 15px -1px rgba(229, 229, 229, 1), 0px 2px 20px 0px rgb(233, 233, 233)',
        padding: config.containerPadding,
        width: width,
      }}
    >
      {options.map((option) => {
        const isActive = activeValue === option.value;
        const styles = getButtonStyles(isActive, isSmall);

        return (
          <Button
            key={option.value}
            id={`workflow-tab-${option.value}`}
            onClick={() => onChange(option.value)}
            disabled={option.disabled}
            sx={{
              background: styles.background,
              border: 'none',
              padding: config.buttonPadding,
              color: option.disabled ? colors.text.tertiary : styles.color,
              fontSize: config.buttonFontSize,
              fontWeight: isActive && isSmall ? 600 : 400,
              cursor: option.disabled ? 'not-allowed' : 'pointer',
              boxShadow: styles.boxShadow,
              borderRadius: config.buttonBorderRadius,
              textTransform: 'none',
              flex: 1,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              gap: isSmall ? '4px' : '8px',
              minWidth: 0,
              whiteSpace: 'nowrap',
              lineHeight: 1,
              opacity: option.disabled ? 0.8 : 1,
              '&:hover': {
                background: option.disabled ? styles.background : styles.hoverBackground,
              },
            }}
          >
            {option.icon && (
              <Box
                sx={{
                  display: 'inline-flex',
                  '& img, & svg': {
                    filter: styles.iconFilter,
                    transition: 'filter 0.25s ease',
                  },
                }}
              >
                <SafeIcon src={option.icon} alt='' height={isSmall ? 14 : 24} width={isSmall ? 14 : 24} />
              </Box>
            )}
            {option.label}
          </Button>
        );
      })}
    </Box>
  );
};

export default ToggleButtons;
