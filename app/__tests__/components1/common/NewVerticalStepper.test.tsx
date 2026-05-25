import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NewVerticalStepper from '@components1/common/NewVerticalStepper';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
      disabled: '#9CA3AF',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
      secondary: '#E5E7EB',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
      primaryLightest: '#DBEAFE',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children, title }: any) => <div title={String(title)}>{children}</div>,
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }: any) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

jest.mock('@assets', () => ({
  checklistIcon: 'checklist-icon.svg',
}));

const steps = [
  { id: 'step-1', title: 'First Step', description: 'Description of first step' },
  { id: 'step-2', title: 'Second Step', description: 'Description of second step' },
  { id: 'step-3', title: 'Third Step', description: '' },
];

describe('NewVerticalStepper', () => {
  it('renders without crashing', () => {
    const { container } = render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders the default title "Upgrade Steps"', () => {
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    expect(screen.getByText('Upgrade Steps')).toBeInTheDocument();
  });

  it('renders a custom title when provided', () => {
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} title='My Custom Steps' />);
    expect(screen.getByText('My Custom Steps')).toBeInTheDocument();
  });

  it('renders all step titles', () => {
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    expect(screen.getByText('First Step')).toBeInTheDocument();
    expect(screen.getByText('Second Step')).toBeInTheDocument();
    expect(screen.getByText('Third Step')).toBeInTheDocument();
  });

  it('renders step number circles for each step', () => {
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
  });

  it('calls onStepChange with correct step number and id when a step button is clicked', () => {
    const onStepChange = jest.fn();
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={onStepChange} />);
    fireEvent.click(screen.getByText('Second Step'));
    expect(onStepChange).toHaveBeenCalledWith(2, 'step-2');
  });

  it('renders a custom icon when provided', () => {
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} icon={<span data-testid='custom-icon'>Icon</span>} />);
    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  it('renders the checklist image when no icon is provided', () => {
    render(<NewVerticalStepper steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    const img = screen.getByAltText('checklist');
    expect(img).toBeInTheDocument();
  });
});
