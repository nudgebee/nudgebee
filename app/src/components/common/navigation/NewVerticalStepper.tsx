import React from 'react';
import { Box, Button, Typography } from '@mui/material';
import { ds } from 'src/utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
import Tooltip from '@ui/Tooltip';
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
        backgroundColor: ds.background[100],
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
          borderBottom: `1px solid color-mix(in srgb, ${ds.gray[400]} 20%, transparent)`,
        }}
      >
        <Box sx={{ display: 'flex', alignItems: 'left', gap: 'var(--ds-space-2)' }}>
          <Box
            sx={{
              width: ds.space[5],
              height: ds.space[5],
              display: 'flex',
              alignItems: 'left',
              justifyContent: 'left',
              borderRadius: 'var(--ds-radius-md)',
              color: ds.background[100],
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
              color: ds.brand[500],
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
            <Tooltip title={step.description || ''} placement='right' key={index} tooltipClassName='custom-tooltip'>
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
                  minHeight: ds.space.mul(0, 20),
                  backgroundColor: isActive ? ds.blue[100] : 'transparent',
                  border: isActive ? `1px solid ${ds.blue[400]}` : '1px solid transparent',
                  borderLeft: isActive ? `4px solid ${ds.blue[400]}` : '1px solid transparent',
                  boxShadow: isActive ? `0 ${ds.space[0]} ${ds.space[2]} color-mix(in srgb, ${ds.blue[500]} 20%, transparent)` : 'none',
                  transition: 'all 0.2s ease-in-out',
                  '&:hover': {
                    backgroundColor: ds.blue[100],
                    transform: 'translateX(2px)',
                  },
                }}
              >
                {/* Step Number Circle */}
                <Box
                  sx={{
                    width: ds.space.mul(0, 14),
                    height: ds.space.mul(0, 14),
                    borderRadius: '50%',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    backgroundColor: isActive ? ds.brand[500] : 'var(--ds-brand-150)',
                    color: isActive ? ds.background[100] : 'var(--ds-gray-600)',
                    flexShrink: 0,
                    boxShadow: isActive ? `0 ${ds.space[0]} ${ds.space[1]} ${ds.gray.alpha[300]}` : 'none',
                  }}
                >
                  {stepNumber}
                </Box>
                {/* Step Title */}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body)',
                    fontWeight: isActive ? 500 : 400,
                    color: isActive ? ds.blue[500] : ds.brand[500],
                    textAlign: 'left',
                    lineHeight: 1.4,
                    flex: 1,
                    pt: ds.space[1],
                  }}
                >
                  {step.title}
                </Typography>
              </Button>
            </Tooltip>
          );
        })}
      </Box>
    </Box>
  );
};

export default VerticalStepNavigation;
