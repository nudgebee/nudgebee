import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NewVerticalStepper from '@shared/navigation/NewVerticalStepper';

jest.mock('@utils/colors');

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }: any) => <div title={String(title)}>{children}</div>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }: any) => React.createElement('img', { alt, 'data-testid': 'safe-icon', ...props }),
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
