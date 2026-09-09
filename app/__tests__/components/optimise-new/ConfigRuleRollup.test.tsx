import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import ConfigRuleRollup from '@components/optimise-new/ConfigRuleRollup';

const mockListRollup = jest.fn();
const mockGetDetails = jest.fn();

const lastFindingsProps: Record<string, any> = {};
jest.mock('@components/optimise-new/ConfigRuleFindings', () => ({
  __esModule: true,
  default: (props: any) => {
    // Cleared, not merged: a prop absent from a later render would otherwise
    // keep an earlier test's value and quietly assert nothing.
    Object.keys(lastFindingsProps).forEach((key) => delete lastFindingsProps[key]);
    Object.assign(lastFindingsProps, props);
    return <div data-testid='config-rule-findings'>{props.ruleName}</div>;
  },
}));

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listRecommendationRuleRollup: (...args: any[]) => mockListRollup(...args),
    getRecommendationDetails: (...args: any[]) => mockGetDetails(...args),
  },
}));

// One grouping row per (rule, severity, account) — the shape the aggregate returns.
const ROWS = [
  { rule_name: 'aws_lambda_tracing', severity: 'Low', account_id: 'acct-a', count: 1194 },
  { rule_name: 'aws_lambda_tracing', severity: 'Low', account_id: 'acct-b', count: 39 },
  { rule_name: 'aws_tags', severity: 'Low', account_id: 'acct-a', count: 969 },
  { rule_name: 'certificate_expiry', severity: 'Critical', account_id: 'acct-a', count: 23 },
];

describe('ConfigRuleRollup', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListRollup.mockResolvedValue(ROWS);
    mockGetDetails.mockReturnValue(null);
  });

  const renderView = (props: Partial<React.ComponentProps<typeof ConfigRuleRollup>> = {}) =>
    render(<ConfigRuleRollup accountId={['acct-a', 'acct-b']} status={['Open']} onSelectRecommendation={jest.fn()} {...props} />);

  it('collapses per-resource findings into one row per check', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('aws_lambda_tracing')).toBeInTheDocument());
    // Four grouping rows spanning three checks — the two Lambda rows fold into one.
    expect(screen.getAllByText('aws_lambda_tracing')).toHaveLength(1);
    expect(screen.getByText('aws_tags')).toBeInTheDocument();
    expect(screen.getByText('certificate_expiry')).toBeInTheDocument();
  });

  it('shows a check total summed across accounts, not the per-account split', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('1,233')).toBeInTheDocument());
    expect(screen.queryByText('1,194')).not.toBeInTheDocument();
  });

  it('scopes the query to the category and the accounts in view', async () => {
    renderView();

    await waitFor(() => expect(mockListRollup).toHaveBeenCalled());
    expect(mockListRollup).toHaveBeenCalledWith({
      accountId: ['acct-a', 'acct-b'],
      category: 'Configuration',
      status: ['Open'],
    });
  });

  it('prefers the catalog title over the raw rule name', async () => {
    mockGetDetails.mockImplementation((_category: string, ruleName: string) =>
      ruleName === 'aws_lambda_tracing' ? { title: 'Tracing Enabled for Lambda Functions' } : null
    );
    renderView();

    await waitFor(() => expect(screen.getByText('Tracing Enabled for Lambda Functions')).toBeInTheDocument());
    // The raw name stays as the secondary line, so the rule is still searchable.
    expect(screen.getByText('aws_lambda_tracing')).toBeInTheDocument();
  });

  it('expands a check in place rather than navigating away', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('aws_tags')).toBeInTheDocument());
    // The group is not itself a destination — nothing is open until it expands.
    expect(screen.queryByTestId('config-rule-findings')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('aws_tags'));

    await waitFor(() => expect(screen.getByTestId('config-rule-findings')).toBeInTheDocument());
    expect(lastFindingsProps.ruleName).toBe('aws_tags');
  });

  it('hands the expanded findings the scope the list is filtered to', async () => {
    const onSelectRecommendation = jest.fn();
    renderView({ severity: ['Low'], onSelectRecommendation });

    await waitFor(() => expect(screen.getByText('aws_tags')).toBeInTheDocument());
    fireEvent.click(screen.getByText('aws_tags'));

    await waitFor(() => expect(screen.getByTestId('config-rule-findings')).toBeInTheDocument());
    expect(lastFindingsProps.accountId).toEqual(['acct-a', 'acct-b']);
    expect(lastFindingsProps.status).toEqual(['Open']);
    expect(lastFindingsProps.severity).toEqual(['Low']);
    // The individual finding, not the group, is what opens the panel.
    expect(lastFindingsProps.onSelectRecommendation).toBe(onSelectRecommendation);
  });

  it('narrows to the selected severity without re-querying', async () => {
    renderView({ severity: ['Critical'] });

    await waitFor(() => expect(screen.getByText('certificate_expiry')).toBeInTheDocument());
    expect(screen.queryByText('aws_lambda_tracing')).not.toBeInTheDocument();
    expect(mockListRollup).toHaveBeenCalledTimes(1);
  });

  it('keeps a multi-band check visible under each band it has findings in', async () => {
    mockListRollup.mockResolvedValue([
      { rule_name: 'misconfigurations', severity: 'Critical', account_id: 'acct-a', count: 2 },
      { rule_name: 'misconfigurations', severity: 'Medium', account_id: 'acct-a', count: 159 },
    ]);
    // The row shows Critical, but filtering to Medium must not hide a check that
    // has 159 Medium findings.
    renderView({ severity: ['Medium'] });

    await waitFor(() => expect(screen.getByText('misconfigurations')).toBeInTheDocument());
  });

  it('counts only the findings in the selected bands', async () => {
    mockListRollup.mockResolvedValue([
      { rule_name: 'misconfigurations', severity: 'Critical', account_id: 'acct-a', count: 2 },
      { rule_name: 'misconfigurations', severity: 'Medium', account_id: 'acct-a', count: 159 },
    ]);
    renderView({ severity: ['Critical'] });

    // 2 Critical findings — not the 161 the check has in total.
    await waitFor(() => expect(screen.getByText('2')).toBeInTheDocument());
    expect(screen.queryByText('161')).not.toBeInTheDocument();
  });

  it('orders by the count it is showing, not the unfiltered total', async () => {
    mockListRollup.mockResolvedValue([
      { rule_name: 'big_overall', severity: 'Critical', account_id: 'acct-a', count: 2 },
      { rule_name: 'big_overall', severity: 'Medium', account_id: 'acct-a', count: 900 },
      { rule_name: 'big_in_band', severity: 'Critical', account_id: 'acct-a', count: 20 },
    ]);
    renderView({ severity: ['Critical'] });

    await waitFor(() => expect(screen.getByText('big_in_band')).toBeInTheDocument());
    const rows = screen.getAllByText(/big_overall|big_in_band/).map((el) => el.textContent);
    expect(rows[0]).toBe('big_in_band');
  });

  it('names the accounts a check fires in rather than counting them', async () => {
    renderView({
      accounts: {
        'acct-a': { name: 'production-eu', cloud_provider: 'AWS' },
        'acct-b': { name: 'sandbox', cloud_provider: 'AWS' },
      },
    });

    await waitFor(() => expect(screen.getByText('production-eu, sandbox')).toBeInTheDocument());
    // The two single-account checks name their account as well, rather than showing "1".
    expect(screen.getAllByText('production-eu')).toHaveLength(2);
  });

  it('falls back to the account id rather than dropping an account it cannot name', async () => {
    renderView({ accounts: { 'acct-a': { name: 'production-eu', cloud_provider: 'AWS' } } });

    await waitFor(() => expect(screen.getByText('acct-b, production-eu')).toBeInTheDocument());
  });

  it('leads with the worst check, however loud the lower bands are', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('certificate_expiry')).toBeInTheDocument());
    const order = screen.getAllByText(/aws_lambda_tracing|aws_tags|certificate_expiry/).map((el) => el.textContent);
    // 23 Critical findings lead 1,233 Low ones.
    expect(order[0]).toBe('certificate_expiry');
  });

  it('badges a filtered row with the worst band in the selection, not the worst it reaches', async () => {
    mockListRollup.mockResolvedValue([
      { rule_name: 'misconfigurations', severity: 'Critical', account_id: 'acct-a', count: 2 },
      { rule_name: 'misconfigurations', severity: 'Medium', account_id: 'acct-a', count: 159 },
    ]);
    renderView({ severity: ['Medium'] });

    // The count says 159, so the badge beside it has to say Medium — the two
    // would otherwise describe different sets of findings on one row.
    await waitFor(() => expect(screen.getByText('159')).toBeInTheDocument());
    expect(screen.getByLabelText('Medium')).toBeInTheDocument();
    expect(screen.queryByLabelText('Critical')).not.toBeInTheDocument();
  });

  it('renders an empty state rather than failing when the query errors', async () => {
    mockListRollup.mockRejectedValue(new Error('boom'));
    const consoleError = jest.spyOn(console, 'error').mockImplementation(() => {});
    renderView();

    await waitFor(() => expect(screen.queryByText('aws_tags')).not.toBeInTheDocument());
    consoleError.mockRestore();
  });

  it('does not query when no account is in scope', async () => {
    renderView({ accountId: [] });

    await waitFor(() => expect(mockListRollup).not.toHaveBeenCalled());
  });
});
