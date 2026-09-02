import React from 'react';
import { Box, Button } from '@mui/material';
import SafeIcon from '@shared/icons/SafeIcon';
import Tooltip from '@ui/Tooltip';

interface ToggleOption {
  value: string;
  label: React.ReactNode;
  icon?: any;
  disabled?: boolean;
  /** When set, the button is wrapped in a tooltip. Works even while `disabled`
   *  (e.g. explaining why a tab is unavailable to a read-only user). */
  tooltip?: React.ReactNode;
}

interface ToggleButtonsProps {
  options: ToggleOption[];
  activeValue: string;
  width?: string;
  size?: 'default' | 'large' | 'sm' | 'xs';
  noShadow?: boolean;
  onChange: (value: string) => void;
}

function getButtonStyles(isActive: boolean, isSmall: boolean) {
  if (isActive && isSmall) {
    return {
      background: 'var(--ds-background-100)',
      color: 'var(--ds-brand-700)',
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
      containerPadding: 'calc(var(--ds-space-0) * 2) var(--ds-space-1)',
      containerBorderRadius: 'var(--ds-radius-lg)',
      buttonPadding: 'calc(var(--ds-space-0) * 4) var(--ds-space-3)',
      buttonFontSize: 'var(--ds-text-body)',
      buttonBorderRadius: 'var(--ds-radius-md)',
    },
    large: {
      containerPadding: '0',
      containerBorderRadius: 'var(--ds-radius-md)',
      buttonPadding: 'var(--ds-space-2) calc(var(--ds-space-0) * 10)',
      buttonFontSize: 'var(--ds-text-body)',
      buttonBorderRadius: 'var(--ds-radius-md)',
    },
    sm: {
      containerPadding: 'var(--ds-space-1)',
      containerBorderRadius: 'var(--ds-radius-lg)',
      buttonPadding: 'calc(var(--ds-space-0) * 3) calc(var(--ds-space-0) * 5)',
      buttonFontSize: 'var(--ds-text-small)',
      buttonBorderRadius: 'var(--ds-radius-md)',
    },
    xs: {
      containerPadding: 'calc(var(--ds-space-0) * 1) calc(var(--ds-space-0) * 2)',
      containerBorderRadius: 'var(--ds-radius-md)',
      buttonPadding: 'calc(var(--ds-space-0) * 4) calc(var(--ds-space-0) * 3)',
      buttonFontSize: 'var(--ds-text-caption)',
      buttonBorderRadius: 'var(--ds-radius-md)',
    },
  };

  const config = sizeConfig[size];

  const isSmall = size === 'sm' || size === 'xs';

  return (
    <Box
      sx={{
        display: 'flex',
        backgroundColor: isSmall ? 'var(--ds-gray-100)' : 'white',
        borderRadius: config.containerBorderRadius,
        border: isSmall ? 'none' : '1px solid var(--ds-gray-300)',
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

        const button = (
          <Button
            key={option.value}
            id={`workflow-tab-${option.value}`}
            onClick={() => onChange(option.value)}
            disabled={option.disabled}
            disableRipple
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
              transition: 'all 0.3s cubic-bezier(0.34, 1.56, 0.64, 1)',
              transform: isActive ? 'scale(1)' : 'scale(0.98)',
              '&:hover': { background: styles.background },
              '&:active': { outline: 'none' },
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

        // A disabled MUI Button swallows pointer events, so wrap it in a span to
        // keep the tooltip hoverable (e.g. the Editor tab for read-only users).
        if (option.tooltip) {
          return (
            <Tooltip key={option.value} title={option.tooltip}>
              <Box component='span' sx={{ flex: 1, display: 'flex', minWidth: 0 }}>
                {button}
              </Box>
            </Tooltip>
          );
        }

        return button;
      })}
    </Box>
  );
};

export default ToggleButtons;
