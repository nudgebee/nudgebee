import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Stepper } from '@ui/Stepper';

const steps = [
  { id: 's1', label: 'Create account', state: 'done' as const },
  { id: 's2', label: 'Configure settings', state: 'current' as const },
  { id: 's3', label: 'Deploy', state: 'upcoming' as const },
];

describe('Stepper', () => {
  it('renders all step labels', () => {
    render(<Stepper steps={steps} current={1} />);
    expect(screen.getByText('Create account')).toBeInTheDocument();
    expect(screen.getByText('Configure settings')).toBeInTheDocument();
    expect(screen.getByText('Deploy')).toBeInTheDocument();
  });

  it('renders vertical orientation', () => {
    const { container } = render(<Stepper steps={steps} current={0} orientation='vertical' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders horizontal orientation', () => {
    const { container } = render(<Stepper steps={steps} current={0} orientation='horizontal' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('calls onStepClick with id and index when clickable-done step is clicked', () => {
    const onStepClick = jest.fn();
    render(<Stepper steps={steps} current={1} interactivity='clickable-done' onStepClick={onStepClick} />);
    fireEvent.click(screen.getByText('Create account'));
    expect(onStepClick).toHaveBeenCalledWith('s1', 0);
  });

  it('does not call onStepClick in static mode', () => {
    const onStepClick = jest.fn();
    render(<Stepper steps={steps} current={1} interactivity='static' onStepClick={onStepClick} />);
    fireEvent.click(screen.getByText('Create account'));
    expect(onStepClick).not.toHaveBeenCalled();
  });

  it('renders sub text for step', () => {
    const withSub = [{ id: 'a', label: 'Step A', sub: 'Substep detail', state: 'current' as const }];
    render(<Stepper steps={withSub} current={0} />);
    expect(screen.getByText('Substep detail')).toBeInTheDocument();
  });

  it('renders meta text for step', () => {
    const withMeta = [{ id: 'b', label: 'Step B', meta: '2 min', state: 'upcoming' as const }];
    render(<Stepper steps={withMeta} current={0} />);
    expect(screen.getByText('2 min')).toBeInTheDocument();
  });

  it('calls onStepClick for all-clickable mode on any step', () => {
    const onStepClick = jest.fn();
    render(<Stepper steps={steps} current={0} interactivity='all-clickable' onStepClick={onStepClick} />);
    fireEvent.click(screen.getByText('Deploy'));
    expect(onStepClick).toHaveBeenCalledWith('s3', 2);
  });
});
