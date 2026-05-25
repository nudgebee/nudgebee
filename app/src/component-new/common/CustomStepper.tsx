// components/CustomStepper.tsx
/**
 * @deprecated Use `Stepper orientation="horizontal"` from '@components1/ds/Stepper' instead.
 * V2 absorbs this + VerticalStepNavigation + NewVerticalStepper. Uses --ds-* tokens.
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
import React, { useEffect } from 'react';
import { Stepper, Step, StepLabel, Box, StepConnector, stepConnectorClasses, styled, type StepIconProps } from '@mui/material';

let _customStepperWarned = false;
import { Check, Error } from '@mui/icons-material';
import CustomButton from './NewCustomButton';
import { hasWriteAccess } from '@lib/auth';

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
    top: 10,
    left: 'calc(-50% + 16px)',
    right: 'calc(50% + 16px)',
  },
  [`&.${stepConnectorClasses.active}, &.${stepConnectorClasses.completed}`]: {
    [`& .${stepConnectorClasses.line}`]: {
      borderColor: '#16A34A',
      borderTopWidth: 2,
    },
  },
  [`& .${stepConnectorClasses.line}`]: {
    borderColor: '#D0D0D0',
    borderTopWidth: 1,
    borderRadius: 1,
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
        backgroundColor: '#EF4444',
        border: '1px solid #EF4444',
        color: 'white',
      };
    }
    if (completed) {
      return {
        backgroundColor: completedBgColor || '#4caf50',
        border: 'none',
        color: 'white',
      };
    }
    return {
      backgroundColor: 'white',
      border: active ? '1px solid #16A34A' : '1px solid #D0D0D0',
      color: active ? '#16A34A' : '#666',
    };
  };

  const styles = getStyles();

  return (
    <Box
      sx={{
        width: '24px',
        height: '24px',
        borderRadius: '50%',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        fontSize: '14px',
        fontWeight: 'bold',
        transition: 'all 0.2s ease-in-out',
        ...styles,
      }}
    >
      {hasError ? <Error sx={{ fontSize: '16px' }} /> : completed ? <Check sx={{ fontSize: '16px' }} /> : icon}
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
  useEffect(() => {
    if (_customStepperWarned) return;
    _customStepperWarned = true;
    // eslint-disable-next-line no-console
    console.warn(
      '[deprecated] CustomStepper is deprecated. Use `import { Stepper } from "@components1/ds/Stepper"` with orientation="horizontal" instead. ' +
        'Tracked for removal 2026-06-06.'
    );
  }, []);

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
          p: '16px 48px',
          borderBottom: `1px solid #EBEBEB`,
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
                    fontSize: '14px',
                    cursor: 'pointer',
                    marginTop: '10px',
                    color: hasError ? '#EF4444' : isActive ? '#374151' : 'inherit',
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
        p='16px 24px'
        sx={{
          borderTop: '0.5px solid #EBEBEB',
          backgroundColor: 'white',
          flexShrink: 0, // Prevent buttons from shrinking
          '& button': { minWidth: '140px' },
        }}
      >
        <CustomButton text={backButtonText} disabled={activeStep === 1 || isSubmitting} onClick={onBack} variant='secondary' />
        <CustomButton
          disabled={!hasWriteAccess(accountId) || isSubmitting}
          text={getCurrentButtonText()}
          variant='primary'
          onClick={activeStep === steps.length ? onSubmit : onNext}
        />
      </Box>
    </Box>
  );
};

export default CustomStepper;
