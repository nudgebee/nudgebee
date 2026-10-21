import { fireEvent, render, screen } from '@testing-library/react';
import PlanCard from '../PlanCard';

const plan = {
  steps: [
    { id: 's1', phase: 'Scope', title: 'Find pod and window', kind: 'task', status: 'done', finding: 'api-7f, 03:41-03:43' },
    { id: 'h1', phase: 'Verify', title: 'OOMKill', kind: 'hypothesis', status: 'refuted', finding: 'exit 130, not 137' },
    { id: 'h2', phase: 'Verify', title: 'Spot preemption', kind: 'hypothesis', status: 'testing', prior: 'high' },
  ],
  toolCallsByStep: {
    h2: [
      { id: 't1', tool: 'kubectl_execute', thought: 'read node labels' },
      { id: 't2', tool: 'kubectl_execute', thought: 'read node events' },
    ],
  },
};

describe('PlanCard', () => {
  it('renders nothing without steps', () => {
    const { container } = render(<PlanCard plan={{ steps: [] }} live />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows phases, steps, findings and hypothesis results while live', () => {
    render(<PlanCard plan={plan} live />);
    expect(screen.getByTestId('nubi-plan-card')).toBeInTheDocument();
    expect(screen.getByText('Now: Spot preemption')).toBeInTheDocument();
    expect(screen.getByText('Scope')).toBeInTheDocument();
    expect(screen.getByText('exit 130, not 137')).toBeInTheDocument();
    expect(screen.getByText('Refuted')).toBeInTheDocument();
    expect(screen.getByText('Likely: high')).toBeInTheDocument();
  });

  it('lists the calls a step served on demand', () => {
    render(<PlanCard plan={plan} live />);
    expect(screen.queryByText(/read node events/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('plan-step-calls-h2'));
    expect(screen.getByText(/read node events/)).toBeInTheDocument();
  });

  it('counts sub-agent work and opens the step in the drawer when a handler is given', () => {
    const onOpenStep = jest.fn();
    render(<PlanCard plan={{ ...plan, callCountByStep: { h2: 7 } }} live={false} onOpenStep={onOpenStep} />);
    fireEvent.click(screen.getByText('Plan'));
    fireEvent.click(screen.getByTestId('plan-step-calls-h2'));
    expect(screen.getByTestId('plan-step-calls-h2')).toHaveTextContent('7 calls');
    expect(onOpenStep).toHaveBeenCalledWith('h2');
  });

  it('shows untested steps as not started and keeps the running icon for the active one', () => {
    const planWithOpen = { steps: [...plan.steps, { id: 'h3', phase: 'Verify', title: 'DNS', kind: 'hypothesis', status: 'open', prior: 'low' }] };
    render(<PlanCard plan={planWithOpen} live />);
    expect(screen.getAllByTestId('plan-step-not-started')).toHaveLength(1);
    expect(screen.queryByText('Likely: high')).toBeInTheDocument();
    expect(screen.queryByText(/Likely: (medium)/)).not.toBeInTheDocument();
  });

  it('says it is still working when every step is closed but the turn runs, and lists outside-plan work', () => {
    const closedPlan = {
      steps: [{ id: 'h1', title: 'OOMKill', kind: 'hypothesis', status: 'refuted', finding: 'exit 0', prior: 'high' }],
      toolCallsByStep: { _outside: [{ id: 'x1', tool: 'logs', thought: 'check kubelet logs' }] },
      callCountByStep: { _outside: 3 },
    };
    render(<PlanCard plan={closedPlan} live />);
    expect(screen.getByText('All steps closed · still working')).toBeInTheDocument();
    expect(screen.getByText('Outside the plan')).toBeInTheDocument();
    expect(screen.getByTestId('plan-step-calls-_outside')).toHaveTextContent('3 calls');
    expect(screen.queryByText('Likely: high')).not.toBeInTheDocument();
  });

  it('summarizes progress once the turn has answered', () => {
    render(<PlanCard plan={plan} live={false} />);
    expect(screen.getByText('2 of 3 closed')).toBeInTheDocument();
  });
});
