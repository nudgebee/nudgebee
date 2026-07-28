import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import MostFailedAutomations from '@components/workflow/execution-dashboard/MostFailedAutomations';
import type { FailedAutomationCount } from '@api1/workflow/types';

const entries: FailedAutomationCount[] = [
  { workflow_id: 'wf-1', workflow_name: 'ImagePullBackOffHandler', failure_count: 768 },
  { workflow_id: 'wf-2', workflow_name: 'p1-incident-escalation', failure_count: 204 },
];

describe('MostFailedAutomations', () => {
  it('renders one chip per entry', () => {
    render(<MostFailedAutomations entries={entries} approximate={false} retentionDays={10} onSelectAutomation={jest.fn()} />);
    expect(screen.getByTestId('execution-dashboard-most-failed-wf-1')).toBeInTheDocument();
    expect(screen.getByTestId('execution-dashboard-most-failed-wf-2')).toBeInTheDocument();
    expect(screen.getByText('768')).toBeInTheDocument();
  });

  // Clicking a chip filters the table and nothing else — the summary above it
  // is static by design.
  it('reports the clicked automation', () => {
    const onSelectAutomation = jest.fn();
    render(<MostFailedAutomations entries={entries} approximate={false} retentionDays={10} onSelectAutomation={onSelectAutomation} />);
    fireEvent.click(screen.getByTestId('execution-dashboard-most-failed-wf-2'));
    expect(onSelectAutomation).toHaveBeenCalledWith('wf-2');
  });

  it('captions the window it covers', () => {
    render(<MostFailedAutomations entries={entries} approximate={false} retentionDays={10} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText('Most failed · last 10 days')).toBeInTheDocument();
  });

  it('says so when the ranking only covers the scanned prefix', () => {
    render(<MostFailedAutomations entries={entries} approximate retentionDays={10} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText(/ranked over the first 1000 failures/)).toBeInTheDocument();
  });

  // An empty strip is worse than no strip.
  it('renders nothing when there are no failures', () => {
    const { container } = render(<MostFailedAutomations entries={[]} approximate={false} retentionDays={10} onSelectAutomation={jest.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('truncates a long automation name', () => {
    const long: FailedAutomationCount[] = [{ workflow_id: 'wf-3', workflow_name: 'github-infra-ci-job-failure-investigation', failure_count: 3 }];
    render(<MostFailedAutomations entries={long} approximate={false} retentionDays={10} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText('github-infra-ci-job-fail…')).toBeInTheDocument();
  });
});
