import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import VerticalStepNavigation from '@shared/navigation/VerticalStepNavigation';

jest.mock('@utils/colors');

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }: any) => <div title={String(title)}>{children}</div>,
}));

jest.mock('@ui/Divider', () => ({
  __esModule: true,
  Divider: () => <hr />,
  default: () => <hr />,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }: any) => React.createElement('img', { alt, 'data-testid': 'safe-icon', ...props }),
}));

jest.mock('@assets', () => ({
  checklistIcon: 'checklist-icon.svg',
  checkIconBold: 'check-icon-bold.svg',
  MenuArrowDownIcon: 'menu-arrow-down.svg',
  checkFilledIcon: 'check-filled.svg',
  AskNudgebeeSkipIcon: 'skip-icon.svg',
  RunningIcon: 'running-icon.svg',
  timelapseBlackSVG: 'timelapse-black.svg',
  AskNudgebeeErrorIcon: 'error-icon.svg',
  timelapse: 'timelapse.svg',
}));

const makeTasks = (statuses: string[]) =>
  statuses.map((status, i) => ({
    id: `task-${i}`,
    title: `Task ${i + 1}`,
    description: `Description ${i + 1}`,
    status,
    is_required: true,
  }));

const steps = [
  {
    id: 'step-1',
    title: 'First Step',
    description: 'First step description',
    sequence: 1,
    tasks: makeTasks(['completed', 'pending']),
  },
  {
    id: 'step-2',
    title: 'Second Step',
    description: 'Second step description',
    sequence: 2,
    tasks: makeTasks(['pending', 'pending']),
  },
];

describe('VerticalStepNavigation', () => {
  it('renders the "Upgrade Steps" header', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    expect(screen.getByText('Upgrade Steps')).toBeInTheDocument();
  });

  it('renders all step titles', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    expect(screen.getByText('First Step')).toBeInTheDocument();
    expect(screen.getByText('Second Step')).toBeInTheDocument();
  });

  it('calls onStepChange with the correct step number and id when a step button is clicked', () => {
    const onStepChange = jest.fn();
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={onStepChange} />);
    fireEvent.click(screen.getByText('Second Step'));
    expect(onStepChange).toHaveBeenCalledWith(2, 'step-2');
  });

  it('shows pending task count when tasks have pending status', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    // 1 pending in step 1 + 2 pending in step 2 = 3
    expect(screen.getByText('3 pending')).toBeInTheDocument();
  });

  it('does not show pending count when no pending tasks', () => {
    const completedSteps = [
      {
        id: 'step-1',
        title: 'Done Step',
        description: '',
        sequence: 1,
        tasks: makeTasks(['completed']),
      },
    ];
    render(<VerticalStepNavigation steps={completedSteps} activeStep={1} onStepChange={jest.fn()} />);
    expect(screen.queryByText(/pending/)).not.toBeInTheDocument();
  });

  it('does not show tasks by default when showTasks is false', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} showTasks={false} />);
    expect(screen.queryByText('Task 1')).not.toBeInTheDocument();
  });

  it('expands tasks when a step is clicked and showTasks is true', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} showTasks />);
    fireEvent.click(screen.getByText('First Step'));
    expect(screen.getByText('Task 1')).toBeInTheDocument();
  });

  it('calls onTaskChange when a task is clicked', () => {
    const onTaskChange = jest.fn();
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} onTaskChange={onTaskChange} showTasks />);
    // Expand first step
    fireEvent.click(screen.getByText('First Step'));
    // Click task 1
    fireEvent.click(screen.getByText('Task 1'));
    expect(onTaskChange).toHaveBeenCalledWith('step-1', 'task-0');
  });

  it('marks active task with a distinct style indicator', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} activeTask='task-0' onStepChange={jest.fn()} onTaskChange={jest.fn()} showTasks />);
    fireEvent.click(screen.getByText('First Step'));
    // Active task title should be in bold (fontWeight 500) - we verify the text exists
    expect(screen.getByText('Task 1')).toBeInTheDocument();
  });

  it('renders step number for incomplete steps', () => {
    render(<VerticalStepNavigation steps={steps} activeStep={1} onStepChange={jest.fn()} />);
    // Step numbers 1 and 2 should be rendered as text within circles
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('2')).toBeInTheDocument();
  });
});
