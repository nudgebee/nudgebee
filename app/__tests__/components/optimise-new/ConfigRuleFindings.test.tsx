import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import ConfigRuleFindings from '@components/optimise-new/ConfigRuleFindings';

const mockGet = jest.fn();

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getK8sRecommendation: (...args: any[]) => mockGet(...args),
  },
}));

// RowActions gates on write access; grant it so the actions render under test.
jest.mock('@lib/auth', () => ({
  __esModule: true,
  hasWriteAccess: () => true,
  hasPermission: () => true,
}));

const ROW_ACTIONS = {
  assistantName: 'Nubi',
  onAskNubi: jest.fn(),
  onResolve: jest.fn(),
  onCreateTicket: jest.fn(),
  onCopyCli: jest.fn(),
  onDismiss: jest.fn(),
};

const page = (count: number) => ({
  data: {
    recommendation: Array.from({ length: 5 }, (_, i) => ({
      id: `rec-${i}`,
      severity: 'High',
      resource_name: `resource-${i}`,
      account_id: 'acct-a',
      updated_at: '2026-08-20T00:00:00Z',
    })),
    recommendation_aggregate: { aggregate: { count } },
  },
});

describe('ConfigRuleFindings', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockResolvedValue(page(1233));
  });

  const renderView = (props: Partial<React.ComponentProps<typeof ConfigRuleFindings>> = {}) =>
    render(
      <ConfigRuleFindings ruleName='aws_lambda_tracing' accountId={['acct-a']} status={['Open']} onSelectRecommendation={jest.fn()} {...props} />
    );

  it('asks only for the check it is expanded under', async () => {
    renderView();

    await waitFor(() => expect(mockGet).toHaveBeenCalled());
    const query = mockGet.mock.calls[0][0];
    expect(query.category).toBe('Configuration');
    expect(query.ruleName).toEqual(['aws_lambda_tracing']);
    expect(query.offset).toBe(0);
  });

  it('pages through the findings rather than capping them', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('resource-0')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /page 3/i }));

    await waitFor(() => expect(mockGet).toHaveBeenCalledTimes(2));
    expect(mockGet.mock.calls[1][0].offset).toBe(10);
  });

  it('returns to the first page when the filters narrow', async () => {
    const { rerender } = renderView();

    await waitFor(() => expect(screen.getByText('resource-0')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /page 3/i }));
    await waitFor(() => expect(mockGet.mock.calls[1][0].offset).toBe(10));

    // Narrowing shrinks the result set — holding the old offset would land past
    // the end and read as "no findings".
    rerender(
      <ConfigRuleFindings
        ruleName='aws_lambda_tracing'
        accountId={['acct-a']}
        status={['Open']}
        severity={['Critical']}
        onSelectRecommendation={jest.fn()}
      />
    );

    await waitFor(() => {
      const latest = mockGet.mock.calls[mockGet.mock.calls.length - 1][0];
      expect(latest.offset).toBe(0);
      expect(latest.severity).toEqual(['Critical']);
    });
  });

  it('opens the individual finding that was clicked', async () => {
    const onSelectRecommendation = jest.fn();
    renderView({ onSelectRecommendation });

    await waitFor(() => expect(screen.getByText('resource-2')).toBeInTheDocument());
    fireEvent.click(screen.getByText('resource-2'));

    expect(onSelectRecommendation).toHaveBeenCalledWith(expect.objectContaining({ id: 'rec-2' }));
  });
});

describe('ConfigRuleFindings row actions', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGet.mockResolvedValue(page(6));
  });

  const renderWithActions = () =>
    render(
      <ConfigRuleFindings
        ruleName='aws_lambda_tracing'
        accountId={['acct-a']}
        status={['Open']}
        onSelectRecommendation={jest.fn()}
        rowActions={ROW_ACTIONS}
      />
    );

  it('carries the same quick actions the cost tab rows do', async () => {
    renderWithActions();

    await waitFor(() => expect(screen.getByText('resource-0')).toBeInTheDocument());
    // One Ask-Nubi button per row — the shared RowActions, not a local copy.
    expect(screen.getAllByRole('button', { name: 'Ask Nubi' })).toHaveLength(5);
    expect(screen.getAllByRole('button', { name: 'More actions' })).toHaveLength(5);
  });

  it('acts on the row without also opening the panel', async () => {
    const onSelectRecommendation = jest.fn();
    render(
      <ConfigRuleFindings
        ruleName='aws_lambda_tracing'
        accountId={['acct-a']}
        status={['Open']}
        onSelectRecommendation={onSelectRecommendation}
        rowActions={ROW_ACTIONS}
      />
    );

    await waitFor(() => expect(screen.getByText('resource-0')).toBeInTheDocument());
    fireEvent.click(screen.getAllByRole('button', { name: 'Ask Nubi' })[0]);

    expect(ROW_ACTIONS.onAskNubi).toHaveBeenCalledWith(expect.objectContaining({ id: 'rec-0' }));
    // RowActions stops propagation, so the action is not also a way into the row.
    expect(onSelectRecommendation).not.toHaveBeenCalled();
  });

  it('renders no action buttons when no handlers are supplied', async () => {
    render(<ConfigRuleFindings ruleName='aws_lambda_tracing' accountId={['acct-a']} status={['Open']} onSelectRecommendation={jest.fn()} />);

    await waitFor(() => expect(screen.getByText('resource-0')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Ask Nubi' })).not.toBeInTheDocument();
  });
});
