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
        padding: '8px 12px',
        border: '1px solid #EBEBEB',
        borderRadius: '8px !important',
        top: 0,
      }}
    >
      {/* Header */}
      <Box sx={{ marginBottom: '8px', padding: '12px 0px 12px 8px', borderBottom: `1px solid rgba(188, 188, 188, 0.2)` }}>
        <Box sx={{ display: 'flex', alignItems: 'left', gap: '8px' }}>
          <Box
            sx={{
              width: '24px',
              height: '24px',
              display: 'flex',
              alignItems: 'left',
              justifyContent: 'left',
              borderRadius: '6px',
              color: 'white',
            }}
          >
            {icon || <SafeIcon src={checklistIcon} alt='checklist' width={24} height={24} />}
          </Box>
          <Typography
            variant='h6'
            fontFamily={`"Poppins", sans-serif`}
            sx={{
              fontSize: '14px',
              fontWeight: 600,
              color: colors.text.secondary,
              letterSpacing: '-0.02em',
            }}
          >
            {title}
          </Typography>
        </Box>
      </Box>

      {/* Steps List */}
      <Box sx={{ flex: 1, overflowY: 'auto', gap: '8px' }}>
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
                  gap: '12px',
                  p: '12px 14px',
                  borderRadius: '8px',
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
                    fontSize: '12px',
                    fontWeight: 700,
                    backgroundColor: isActive ? colors.background.secondary : '#E5E7EB',
                    color: isActive ? colors.text.white : '#6B7280',
                    flexShrink: 0,
                    boxShadow: isActive ? '0 2px 4px rgba(0, 0, 0, 0.1)' : 'none',
                  }}
                >
                  {stepNumber}
                </Box>
                {/* Step Title */}
                <Typography
                  sx={{
                    fontSize: '13px',
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
