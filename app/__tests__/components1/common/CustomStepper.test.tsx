import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomStepper from '@components1/common/CustomStepper';

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, disabled, variant }: any) => (
    <button onClick={onClick} disabled={disabled} data-variant={variant} data-testid={`btn-${text}`}>
      {text}
    </button>
  ),
}));

jest.mock('@lib/auth', () => ({
  hasWriteAccess: jest.fn(() => true),
}));

const steps = ['Step 1', 'Step 2', 'Step 3'];

describe('CustomStepper', () => {
  it('renders all step labels', () => {
    render(
      <CustomStepper steps={steps} activeStep={1} onStepChange={jest.fn()} onNext={jest.fn()} onBack={jest.fn()}>
        <div>Content</div>
      </CustomStepper>
    );
    expect(screen.getByText('Step 1')).toBeInTheDocument();
    expect(screen.getByText('Step 2')).toBeInTheDocument();
    expect(screen.getByText('Step 3')).toBeInTheDocument();
  });

  it('renders children content', () => {
    render(
      <CustomStepper steps={steps} activeStep={1} onStepChange={jest.fn()} onNext={jest.fn()} onBack={jest.fn()}>
        <div data-testid='step-content'>Step Content</div>
      </CustomStepper>
    );
    expect(screen.getByTestId('step-content')).toBeInTheDocument();
  });

  it('disables Back button on first step', () => {
    render(
      <CustomStepper steps={steps} activeStep={1} onStepChange={jest.fn()} onNext={jest.fn()} onBack={jest.fn()}>
        <div />
      </CustomStepper>
    );
    expect(screen.getByTestId('btn-Back')).toBeDisabled();
  });

  it('enables Back button on step > 1', () => {
    render(
      <CustomStepper steps={steps} activeStep={2} onStepChange={jest.fn()} onNext={jest.fn()} onBack={jest.fn()}>
        <div />
      </CustomStepper>
    );
    expect(screen.getByTestId('btn-Back')).not.toBeDisabled();
  });

  it('calls onNext when Next button is clicked', () => {
    const onNext = jest.fn();
    render(
      <CustomStepper steps={steps} activeStep={1} onStepChange={jest.fn()} onNext={onNext} onBack={jest.fn()}>
        <div />
      </CustomStepper>
    );
    fireEvent.click(screen.getByTestId('btn-Next'));
    expect(onNext).toHaveBeenCalledTimes(1);
  });

  it('calls onBack when Back button is clicked', () => {
    const onBack = jest.fn();
    render(
      <CustomStepper steps={steps} activeStep={2} onStepChange={jest.fn()} onNext={jest.fn()} onBack={onBack}>
        <div />
      </CustomStepper>
    );
    fireEvent.click(screen.getByTestId('btn-Back'));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it('shows Submit button text on last step', () => {
    render(
      <CustomStepper
        steps={steps}
        activeStep={3}
        onStepChange={jest.fn()}
        onNext={jest.fn()}
        onBack={jest.fn()}
        onSubmit={jest.fn()}
        submitButtonText='Submit'
      >
        <div />
      </CustomStepper>
    );
    expect(screen.getByTestId('btn-Submit')).toBeInTheDocument();
  });

  it('calls onSubmit when on last step and submit button clicked', () => {
    const onSubmit = jest.fn();
    render(
      <CustomStepper steps={steps} activeStep={3} onStepChange={jest.fn()} onNext={jest.fn()} onBack={jest.fn()} onSubmit={onSubmit}>
        <div />
      </CustomStepper>
    );
    fireEvent.click(screen.getByTestId('btn-Submit'));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it('calls onStepChange with correct step number when step label clicked', () => {
    const onStepChange = jest.fn();
    render(
      <CustomStepper steps={steps} activeStep={1} onStepChange={onStepChange} onNext={jest.fn()} onBack={jest.fn()}>
        <div />
      </CustomStepper>
    );
    fireEvent.click(screen.getByText('Step 2'));
    expect(onStepChange).toHaveBeenCalledWith(2);
  });

  it('uses custom back button text', () => {
    render(
      <CustomStepper steps={steps} activeStep={2} onStepChange={jest.fn()} onNext={jest.fn()} onBack={jest.fn()} backButtonText='Previous'>
        <div />
      </CustomStepper>
    );
    expect(screen.getByTestId('btn-Previous')).toBeInTheDocument();
  });
});
