import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import MostFailedAutomations from '@components/workflow/execution-dashboard/MostFailedAutomations';
import type { FailedAutomationCount } from '@api1/workflow/types';

const entries: FailedAutomationCount[] = [
  { workflow_id: 'wf-1', workflow_name: 'ImagePullBackOffHandler', failure_count: 768 },
  { workflow_id: 'wf-2', workflow_name: 'p1-incident-escalation', failure_count: 204 },
];

describe('MostFailedAutomations', () => {
  it('renders one row per entry with its failure count', () => {
    render(<MostFailedAutomations entries={entries} approximate={false} totalFailures={1000} onSelectAutomation={jest.fn()} />);
    expect(screen.getByTestId('execution-dashboard-most-failed-wf-1')).toBeInTheDocument();
    expect(screen.getByTestId('execution-dashboard-most-failed-wf-2')).toBeInTheDocument();
    expect(screen.getByText('768')).toBeInTheDocument();
  });

  // The badge answers "would fixing this one automation matter?", so it is the
  // row's share of every failure in the window — not of the rows shown.
  it('shows each row as a share of all failures in the window', () => {
    render(<MostFailedAutomations entries={entries} approximate={false} totalFailures={1000} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText('76.8%')).toBeInTheDocument();
    expect(screen.getByText('20.4%')).toBeInTheDocument();
  });

  // Rounding a tiny-but-nonzero share to "0.0%" would read as "never fails".
  it('floors a nonzero share rather than rounding it to zero', () => {
    const rare: FailedAutomationCount[] = [{ workflow_id: 'wf-9', workflow_name: 'rare-failure', failure_count: 3 }];
    render(<MostFailedAutomations entries={rare} approximate={false} totalFailures={100000} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText('<0.1%')).toBeInTheDocument();
  });

  // Clicking a row filters the table and nothing else — the summary beside it
  // is static by design.
  it('reports the clicked automation', () => {
    const onSelectAutomation = jest.fn();
    render(<MostFailedAutomations entries={entries} approximate={false} totalFailures={1000} onSelectAutomation={onSelectAutomation} />);
    fireEvent.click(screen.getByTestId('execution-dashboard-most-failed-wf-2'));
    expect(onSelectAutomation).toHaveBeenCalledWith('wf-2');
  });

  it('is reachable by keyboard', () => {
    const onSelectAutomation = jest.fn();
    render(<MostFailedAutomations entries={entries} approximate={false} totalFailures={1000} onSelectAutomation={onSelectAutomation} />);
    fireEvent.keyDown(screen.getByTestId('execution-dashboard-most-failed-wf-1'), { key: 'Enter' });
    expect(onSelectAutomation).toHaveBeenCalledWith('wf-1');
  });

  it('says so when the ranking only covers the scanned prefix', () => {
    render(<MostFailedAutomations entries={entries} approximate totalFailures={1000} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText(/ranked over the first 1,000 failures/)).toBeInTheDocument();
  });

  it('drops the caveat when the ranking is exact', () => {
    render(<MostFailedAutomations entries={entries} approximate={false} totalFailures={1000} onSelectAutomation={jest.fn()} />);
    expect(screen.queryByText(/ranked over the first/)).not.toBeInTheDocument();
  });

  // An empty panel is worse than no panel.
  it('renders nothing when there are no failures', () => {
    const { container } = render(<MostFailedAutomations entries={[]} approximate={false} totalFailures={0} onSelectAutomation={jest.fn()} />);
    expect(container).toBeEmptyDOMElement();
  });

  // Without a total from the aggregate, shares fall back to the ranked set
  // rather than dividing by zero.
  it('falls back to the ranked entries when the window total is unknown', () => {
    render(<MostFailedAutomations entries={entries} approximate={false} totalFailures={0} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText('79.0%')).toBeInTheDocument();
  });

  it('truncates a long automation name', () => {
    const long: FailedAutomationCount[] = [{ workflow_id: 'wf-3', workflow_name: 'github-infra-ci-job-failure-investigation', failure_count: 3 }];
    render(<MostFailedAutomations entries={long} approximate={false} totalFailures={100} onSelectAutomation={jest.fn()} />);
    expect(screen.getByText('github-infra-ci-job-failure-…')).toBeInTheDocument();
  });
});
