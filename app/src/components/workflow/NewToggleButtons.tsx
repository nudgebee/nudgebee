import React from 'react';
import { Box, Button } from '@mui/material';
import SafeIcon from '@shared/icons/SafeIcon';

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
      background: 'var(--ds-background-100)',
      color: 'var(--ds-blue-500)',
      boxShadow: '0 1px 3px var(--ds-gray-alpha-300)',
      hoverBackground: 'var(--ds-background-100)',
      iconFilter: 'brightness(0) saturate(100%) invert(45%) sepia(76%) saturate(521%) hue-rotate(179deg) brightness(93%) contrast(108%)',
    };
  }
  if (isActive) {
    return {
      background: 'var(--ds-brand-500)',
      color: 'var(--ds-background-100)',
      boxShadow: '0 var(--ds-space-0) calc(var(--ds-space-0) * 10) color-mix(in srgb, black 15%, transparent)',
      hoverBackground: 'var(--ds-brand-500)',
      iconFilter: 'brightness(0) invert(1)',
    };
  }
  if (isSmall) {
    return {
      background: 'transparent',
      color: 'var(--ds-gray-400)',
      boxShadow: 'none',
      hoverBackground: 'var(--ds-background-200)',
      iconFilter: 'brightness(0) saturate(100%) invert(50%) sepia(0%) hue-rotate(0deg)',
    };
  }
  return {
    background: 'transparent',
    color: 'var(--ds-brand-500)',
    boxShadow: 'none',
    hoverBackground: 'transparent',
    iconFilter: 'none',
  };
}

const ToggleButtons: React.FC<ToggleButtonsProps> = ({ options, activeValue, width, size = 'default', noShadow, onChange }) => {
  const sizeConfig = {
    default: {
      containerPadding: 'calc(var(--ds-space-0) * 3) var(--ds-space-2)',
      containerBorderRadius: 'var(--ds-radius-lg)',
      buttonPadding: 'calc(var(--ds-space-0) * 3) var(--ds-space-3)',
      buttonFontSize: 'var(--ds-text-body)',
      buttonBorderRadius: 'var(--ds-radius-md)',
    },
    large: {
      containerPadding: '0',
      containerBorderRadius: 'var(--ds-radius-xl)',
      buttonPadding: 'var(--ds-space-2) calc(var(--ds-space-0) * 10)',
      buttonFontSize: 'var(--ds-text-title)',
      buttonBorderRadius: 'var(--ds-radius-lg)',
    },
    sm: {
      containerPadding: 'var(--ds-space-1)',
      containerBorderRadius: 'var(--ds-radius-lg)',
      buttonPadding: 'calc(var(--ds-space-0) * 3) calc(var(--ds-space-0) * 5)',
      buttonFontSize: 'var(--ds-text-small)',
      buttonBorderRadius: 'var(--ds-radius-md)',
    },
  };

  const config = sizeConfig[size];

  const isSmall = size === 'sm';

  return (
    <Box
      sx={{
        display: 'flex',
        backgroundColor: isSmall ? 'var(--ds-gray-100)' : 'white',
        borderRadius: config.containerBorderRadius,
        border: isSmall ? 'none' : '1px solid var(--ds-gray-200)',
        boxShadow:
          noShadow || isSmall
            ? 'none'
            : '0 var(--ds-space-1) calc(var(--ds-space-0) * 7.5) -1px var(--ds-gray-100), 0 var(--ds-space-0) calc(var(--ds-space-0) * 10) 0 var(--ds-gray-100)',
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
              color: option.disabled ? 'var(--ds-gray-600)' : styles.color,
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
              gap: isSmall ? 'var(--ds-space-1)' : 'var(--ds-space-2)',
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
