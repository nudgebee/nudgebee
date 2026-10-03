// components/CustomStepper.tsx
import React from 'react';
import { Box } from '@mui/material';
import { Stepper, type StepperStep } from '@ui/Stepper';
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
  stepErrors?: boolean[];
  nextButtonText?: string[]; // Array of button text for each step
  submitButtonText?: string; // Text for the final submit button
  backButtonText?: string; // Text for the back button
  accountId?: string; // Optional account ID for access checks
  isSubmitting?: boolean; // Disable buttons during form submission
}

const CustomStepper: React.FC<CustomStepperProps> = ({
  steps,
  activeStep,
  onStepChange,
  onNext,
  onBack,
  children,
  onSubmit,
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

  const stepperSteps: StepperStep[] = steps.map((label, index) => ({
    id: label,
    label,
    ...(stepErrors[index] ? { state: 'failed' as const } : {}),
  }));

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Box
        sx={{
          p: 'var(--ds-space-4) var(--ds-space-7)',
          borderBottom: `1px solid var(--ds-gray-200)`,
          flexShrink: 0, // Prevent stepper from shrinking
        }}
      >
        <Stepper
          steps={stepperSteps}
          current={activeStep - 1}
          orientation='horizontal'
          interactivity='all-clickable'
          onStepClick={(_id, index) => onStepChange(index + 1)}
        />
      </Box>

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
