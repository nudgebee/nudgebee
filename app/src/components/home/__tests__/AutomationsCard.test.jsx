import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { AutomationsCard } from '@pages/home';

jest.mock('next/router', () => ({ useRouter: () => ({ isReady: true, query: {}, pathname: '/home', push: jest.fn() }) }));

// #40934: Triggered (24h) counts runs, so it must open the runs list for the window it counted.
const FROM = '2026-10-11T09:00:00.000Z';
const TO = '2026-10-12T09:00:00.000Z';
const DATA = { totalCount: 5, actionedCount: 37, configuredCount: 2, windowStart: FROM, windowEnd: TO };

const renderCard = (workflowData = DATA) => {
  const onViewWorkflows = jest.fn();
  const onViewRuns = jest.fn();
  render(<AutomationsCard workflowData={workflowData} accountId='acc-1' onViewWorkflows={onViewWorkflows} onViewRuns={onViewRuns} />);
  return { onViewWorkflows, onViewRuns };
};

describe('Home AutomationsCard', () => {
  it('opens the runs list for the exact window Triggered (24h) counted', () => {
    const { onViewWorkflows, onViewRuns } = renderCard();

    fireEvent.click(document.getElementById('automations-triggered-stat'));

    expect(onViewRuns).toHaveBeenCalledWith({ accountId: 'acc-1', from: FROM, to: TO });
    expect(onViewWorkflows).not.toHaveBeenCalled();
  });

  it('does not make Triggered (24h) clickable when it is 0', () => {
    const { onViewRuns } = renderCard({ ...DATA, actionedCount: 0 });

    const stat = document.getElementById('automations-triggered-stat');
    expect(stat).not.toHaveAttribute('role', 'button');
    fireEvent.click(stat);
    expect(onViewRuns).not.toHaveBeenCalled();
  });

  it('keeps Configured and Event-based on the automations list', () => {
    const { onViewWorkflows } = renderCard();

    fireEvent.click(screen.getByText('Configured'));
    fireEvent.click(screen.getByText('Event-based'));

    expect(onViewWorkflows).toHaveBeenNthCalledWith(1, { accountId: 'acc-1', status: 'Active' });
    expect(onViewWorkflows).toHaveBeenNthCalledWith(2, { accountId: 'acc-1', status: 'Active', type: 'event' });
  });
});
