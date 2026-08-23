import React from 'react';
import { render, screen } from '@testing-library/react';
import ExecutionSummaryCards from '@components/workflow/execution-dashboard/ExecutionSummaryCards';
import type { ExecutionAggregateResponse } from '@api1/workflow/types';

const aggregate: ExecutionAggregateResponse = {
  total: 412,
  succeeded: 380,
  failed: 28,
  running: 4,
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
    expect(screen.getByText('≈ 412')).toBeInTheDocument();
    expect(screen.getByText('≈ 28')).toBeInTheDocument();
  });

  it('renders exact counts when the server does not flag them', () => {
    render(<ExecutionSummaryCards aggregate={{ ...aggregate, counts_are_approximate: false }} loading={false} retentionDays={10} />);
    expect(screen.getByText('412')).toBeInTheDocument();
  });

  // Running is a point-in-time number and reads wrong beside retention-window
  // totals, so it is deliberately not rendered even though the API returns it.
  it('renders exactly three cards and omits Running', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    expect(screen.getByTestId('execution-dashboard-stat-total')).toBeInTheDocument();
    expect(screen.getByTestId('execution-dashboard-stat-succeeded')).toBeInTheDocument();
    expect(screen.getByTestId('execution-dashboard-stat-failed')).toBeInTheDocument();
    expect(screen.queryByTestId('execution-dashboard-stat-running')).not.toBeInTheDocument();
  });

  // The counts are unfiltered and static; making them look clickable would
  // imply they respond to the table's filters, which they do not.
  it('renders the cards as display-only, not as filter controls', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    ['total', 'succeeded', 'failed'].forEach((key) => {
      const card = screen.getByTestId(`execution-dashboard-stat-${key}`);
      expect(card).not.toHaveAttribute('role', 'button');
      expect(card).not.toHaveAttribute('aria-pressed');
    });
    // The only button on the row is the Failed card's info hint.
    expect(screen.getAllByRole('button')).toHaveLength(1);
    expect(screen.getByRole('button')).toHaveAttribute('aria-label', 'Info about Failed');
  });

  // This caption is what stops the card/table mismatch reading as a bug.
  it('captions each card with the retention window', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={10} />);
    expect(screen.getAllByText('last 10 days')).toHaveLength(3);
  });

  it('omits the caption when the retention is unknown', () => {
    render(<ExecutionSummaryCards aggregate={aggregate} loading={false} retentionDays={0} />);
    expect(screen.queryByText(/last \d+ days/)).not.toBeInTheDocument();
  });
});
