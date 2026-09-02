import React from 'react';
import { render, screen } from '@testing-library/react';
import ExecutionSummaryCards from '@components/workflow/execution-dashboard/ExecutionSummaryCards';
import type { ExecutionAggregateResponse } from '@api1/workflow/types';

const aggregate: ExecutionAggregateResponse = {
  total: 412,
  succeeded: 380,
  failed: 28,
  running: 4,
  timed_out: 12,
  counts_are_approximate: true,
  top_failed: [],
  top_failed_is_approximate: false,
  retention_days: 10,
};

describe('ExecutionSummaryCards', () => {
  // Temporal documents CountWorkflow as approximate; a bare number would
  // overstate what the dashboard actually knows.
  it('prefixes counts with ≈ when the server reports them as approximate', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    expect(screen.getByText('≈ 28')).toBeInTheDocument();
    expect(screen.getByText('of ≈ 412 total · last 10 days')).toBeInTheDocument();
  });

  it('renders exact counts when the server does not flag them', () => {
    render(<ExecutionSummaryCards aggregate={{ ...aggregate, counts_are_approximate: false }} loading={false} retentionDays={10} />);
    expect(screen.getByText('28')).toBeInTheDocument();
    expect(screen.getByText('of 412 total · last 10 days')).toBeInTheDocument();
  });

  // Failures are the headline; success and timeouts are the context under it.
  it('leads with the failed count and breaks the window down beneath it', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    expect(screen.getByText('Failed executions')).toBeInTheDocument();
    expect(screen.getByTestId('execution-dashboard-share-succeeded')).toHaveTextContent('≈ 380 · 92.2%');
    expect(screen.getByTestId('execution-dashboard-share-timed-out')).toHaveTextContent('≈ 12 · 2.9%');
  });

  // Running is a point-in-time number and reads wrong beside retention-window
  // totals, so it is deliberately not rendered even though the API returns it.
  it('omits the running count', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    expect(screen.queryByText(/Running/)).not.toBeInTheDocument();
  });

  // The counts are unfiltered and static; making them look clickable would
  // imply they respond to the table's filters, which they do not.
  it('renders the summary as display-only, not as a filter control', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    expect(screen.getByTestId('execution-dashboard-summary')).not.toHaveAttribute('role', 'button');
    // The only button on the card is the failed count's info hint.
    expect(screen.getAllByRole('button')).toHaveLength(1);
    expect(screen.getByRole('button')).toHaveAttribute('aria-label', 'Info about Failed executions');
  });

  it('omits the window caption when the retention is unknown', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={0} />);
    expect(screen.queryByText(/last \d+ days/)).not.toBeInTheDocument();
    expect(screen.getByText('of ≈ 412 total')).toBeInTheDocument();
  });

  // Zeros are a real reading of an empty window, so they must never stand in
  // for "not loaded yet" — the card is fed the aggregate's own loading flag.
  it('renders a skeleton, not zeros, while the aggregate is in flight', () => {
    render(<ExecutionSummaryCards aggregate={null} loading={true} retentionDays={0} />);
    expect(screen.getByRole('status')).toHaveAttribute('aria-busy', 'true');
    expect(screen.queryByText('Failed executions')).not.toBeInTheDocument();
    expect(screen.queryByTestId('execution-dashboard-share-succeeded')).not.toBeInTheDocument();
  });

  // A percentage of zero executions is 0%, not NaN%.
  it('survives an empty window', () => {
    const empty: ExecutionAggregateResponse = { ...aggregate, total: 0, succeeded: 0, failed: 0, timed_out: 0 };
    render(<ExecutionSummaryCards aggregate={empty} loading={false} retentionDays={10} />);
    expect(screen.getByTestId('execution-dashboard-share-succeeded')).toHaveTextContent('≈ 0 · 0.0%');
  });
});
