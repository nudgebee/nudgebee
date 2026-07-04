// components/CustomStepper.tsx
import React from 'react';
import { Stepper, Step, StepLabel, Box, StepConnector, stepConnectorClasses, styled, type StepIconProps } from '@mui/material';
import { Check, Error } from '@mui/icons-material';
import { Button as DsButton } from '@ui/Button';
import { hasWriteAccess } from '@lib/auth';
import { ds } from '@utils/colors';

interface CustomStepperProps {
  steps: string[];
  activeStep: number;
  onStepChange: (step: number) => void;
  onNext: () => void;
  onBack: () => void;
  children: React.ReactNode;
  onSubmit?: () => void;
  completedBgColor?: string;
  stepErrors?: boolean[];
  nextButtonText?: string[]; // Array of button text for each step
  submitButtonText?: string; // Text for the final submit button
  backButtonText?: string; // Text for the back button
  accountId?: string; // Optional account ID for access checks
  isSubmitting?: boolean; // Disable buttons during form submission
}

const CustomConnector = styled(StepConnector)(() => ({
  [`&.${stepConnectorClasses.alternativeLabel}`]: {
    top: ds.space.mul(0, 5),
    left: `calc(-50% + ${ds.space[4]})`,
    right: `calc(50% + ${ds.space[4]})`,
  },
  [`&.${stepConnectorClasses.active}, &.${stepConnectorClasses.completed}`]: {
    [`& .${stepConnectorClasses.line}`]: {
      borderColor: 'var(--ds-green-500)',
      borderTopWidth: ds.space[0],
    },
  },
  [`& .${stepConnectorClasses.line}`]: {
    borderColor: 'var(--ds-brand-200)',
    borderTopWidth: 1,
    borderRadius: ds.radius.sm,
    transition: 'all 0.2s ease-in-out',
  },
}));

const CustomStepIcon: React.FC<
  StepIconProps & {
    completedBgColor?: string;
    hasError?: boolean;
  }
> = ({ active, completed, icon, completedBgColor, hasError }) => {
  const getStyles = () => {
    if (hasError) {
      return {
        backgroundColor: 'var(--ds-red-500)',
        border: '1px solid var(--ds-red-500)',
        color: 'white',
      };
    }
    if (completed) {
      return {
        backgroundColor: completedBgColor || 'var(--ds-green-400)',
        border: 'none',
        color: 'white',
      };
    }
    return {
      backgroundColor: 'white',
      border: active ? '1px solid var(--ds-green-500)' : '1px solid var(--ds-gray-300)',
      color: active ? 'var(--ds-green-500)' : 'var(--ds-gray-500)',
    };
  };

  const styles = getStyles();

  return (
    <Box
      sx={{
        width: ds.space[5],
        height: ds.space[5],
        borderRadius: '50%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: 'var(--ds-text-body-lg)',
        fontWeight: 'bold',
        transition: 'all 0.2s ease-in-out',
        ...styles,
      }}
    >
      {hasError ? <Error sx={{ fontSize: 'var(--ds-text-title)' }} /> : completed ? <Check sx={{ fontSize: 'var(--ds-text-title)' }} /> : icon}
    </Box>
  );
};

const CustomStepper: React.FC<CustomStepperProps> = ({
  steps,
  activeStep,
  onStepChange,
  onNext,
  onBack,
  children,
  onSubmit,
  completedBgColor,
  stepErrors = [],
  nextButtonText = [],
  submitButtonText = 'Submit',
  backButtonText = 'Back',
  accountId = '',
  isSubmitting = false,
}) => {
  // Get the current step button text
  const getCurrentButtonText = () => {
    if (activeStep === steps.length) {
      return submitButtonText;
    }
    return nextButtonText[activeStep - 1] || 'Next';
  };

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Stepper
        activeStep={activeStep - 1}
        alternativeLabel
        connector={<CustomConnector />}
        sx={{
          p: 'var(--ds-space-4) var(--ds-space-7)',
          borderBottom: `1px solid var(--ds-gray-200)`,
          flexShrink: 0, // Prevent stepper from shrinking
        }}
      >
        {steps.map((label, index) => {
          const isActive = activeStep - 1 === index;
          const hasError = stepErrors[index];

          return (
            <Step key={label} onClick={() => onStepChange(index + 1)}>
              <StepLabel
                StepIconComponent={(props) => <CustomStepIcon {...props} completedBgColor={completedBgColor} hasError={hasError} />}
                sx={{
                  '& .MuiStepLabel-label.MuiStepLabel-alternativeLabel': {
                    fontSize: 'var(--ds-text-body-lg)',
                    cursor: 'pointer',
                    marginTop: 'var(--ds-space-2)',
                    color: hasError ? 'var(--ds-red-500)' : isActive ? 'var(--ds-gray-700)' : 'inherit',
                    fontWeight: hasError || isActive ? 500 : 'normal',
                  },
                  '& .MuiStepLabel-iconContainer': {
                    paddingRight: '0px',
                  },
                }}
              >
                {label}
              </StepLabel>
            </Step>
          );
        })}
      </Stepper>

      {/* Scrollable content area */}
      <Box sx={{ flex: 1, overflowY: 'auto', minHeight: 0 }}>{children}</Box>

      {/* Fixed bottom button section */}
      <Box
        display='flex'
        justifyContent='space-between'
        alignItems='center'
        p={`${ds.space[4]} ${ds.space[5]}`}
        sx={{
          borderTop: '0.5px solid var(--ds-gray-200)',
          backgroundColor: 'white',
          flexShrink: 0, // Prevent buttons from shrinking
          '& button': { minWidth: ds.space.mul(0, 70) },
        }}
      >
        <DsButton tone='secondary' size='md' disabled={activeStep === 1 || isSubmitting} onClick={onBack}>
          {backButtonText}
        </DsButton>
        <DsButton
          tone='primary'
          size='md'
          disabled={!hasWriteAccess(accountId) || isSubmitting}
          loading={isSubmitting}
          onClick={activeStep === steps.length ? onSubmit : onNext}
        >
          {getCurrentButtonText()}
        </DsButton>
      </Box>
    </Box>
  );
};

export default CustomStepper;
