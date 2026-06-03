import React from 'react';
import { Box, Button, Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTooltip from './CustomTooltip';
import { checklistIcon } from '@assets';

interface VerticalStepNavigationProps {
  steps: any[];
  activeStep: number;
  onStepChange: (step: number, id: string) => void;
  title?: string;
  icon?: React.ReactNode;
}

const VerticalStepNavigation: React.FC<VerticalStepNavigationProps> = ({ steps, activeStep, onStepChange, title = 'Upgrade Steps', icon }) => {
  return (
    <Box
      sx={{
        height: '100%',
        display: 'flex',
        flexDirection: 'column',
        backgroundColor: colors.background.white,
        position: 'sticky',
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        border: '1px solid var(--ds-gray-200)',
        borderRadius: 'var(--ds-radius-lg) !important',
        top: 0,
      }}
    >
      {/* Header */}
      <Box
        sx={{
          marginBottom: 'var(--ds-space-2)',
          padding: 'var(--ds-space-3) 0px var(--ds-space-3) var(--ds-space-2)',
          borderBottom: `1px solid rgba(188, 188, 188, 0.2)`,
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'left', gap: 'var(--ds-space-2)' }}>
          <Box
            sx={{
              width: '24px',
              height: '24px',
              display: 'flex',
              alignItems: 'left',
              justifyContent: 'left',
              borderRadius: 'var(--ds-radius-md)',
              color: 'white',
            }}
          >
            {icon || <SafeIcon src={checklistIcon} alt='checklist' width={24} height={24} />}
          </Box>
          <Typography
            variant='h6'
            fontFamily={`"Poppins", sans-serif`}
            sx={{
              fontSize: 'var(--ds-text-body-lg)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: colors.text.secondary,
              letterSpacing: '-0.02em',
            }}
          >
            {title}
          </Typography>
        </Box>
      </Box>

      {/* Steps List */}
      <Box sx={{ flex: 1, overflowY: 'auto', gap: 'var(--ds-space-2)' }}>
        {steps.map((step, index) => {
          const stepNumber = index + 1;
          const isActive = activeStep === stepNumber;

          return (
            <CustomTooltip title={step.description || ''} placement='right' key={index} tooltipClassName='custom-tooltip'>
              <Button
                key={index}
                onClick={() => onStepChange(stepNumber, step.id)}
                sx={{
                  width: '100%',
                  display: 'flex',
                  alignItems: 'flex-start',
                  justifyContent: 'flex-start',
                  gap: 'var(--ds-space-3)',
                  p: 'var(--ds-space-3) var(--ds-space-3)',
                  borderRadius: 'var(--ds-radius-lg)',
                  textTransform: 'none',
                  minHeight: '40px',
                  backgroundColor: isActive ? colors.background.primaryLightest : 'transparent',
                  border: isActive ? `1px solid ${colors.border.primaryLightest}` : '1px solid transparent',
                  borderLeft: isActive ? `4px solid ${colors.border.primaryLightest}` : '1px solid transparent',
                  boxShadow: isActive ? '0 2px 8px rgba(59, 130, 246, 0.2)' : 'none',
                  transition: 'all 0.2s ease-in-out',
                  '&:hover': {
                    backgroundColor: colors.background.primaryLightest,
                    transform: 'translateX(2px)',
                  },
                }}
              >
                {/* Step Number Circle */}
                <Box
                  sx={{
                    width: 28,
                    height: 28,
                    borderRadius: '50%',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    backgroundColor: isActive ? colors.background.secondary : 'var(--ds-brand-150)',
                    color: isActive ? colors.text.white : 'var(--ds-gray-600)',
                    flexShrink: 0,
                    boxShadow: isActive ? '0 2px 4px rgba(0, 0, 0, 0.1)' : 'none',
                  }}
                >
                  {stepNumber}
                </Box>
                {/* Step Title */}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: isActive ? 500 : 400,
                    color: isActive ? colors.text.primary : colors.text.secondary,
                    textAlign: 'left',
                    lineHeight: 1.4,
                    flex: 1,
                    pt: 0.5,
                  }}
                >
                  {step.title}
                </Typography>
              </Button>
            </CustomTooltip>
          );
        })}
      </Box>
    </Box>
  );
};

export default VerticalStepNavigation;
