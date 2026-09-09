import React from 'react';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import '@testing-library/jest-dom';
import ResolutionDetailPanel from '@components/optimise-new/ResolutionDetailPanel';

const mockGetRecommendation = jest.fn();

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: { getK8sRecommendation: (...args: any[]) => mockGetRecommendation(...args) },
}));

jest.mock('@components/cloudaccount/CommandExecutionHistory', () => ({
  __esModule: true,
  default: () => <div data-testid='command-history' />,
}));

const RESOLUTION = {
  id: 'res-1',
  recommendation_id: 'rec-1',
  account_id: 'acct-a',
  status: 'Failed',
  status_message: 'Failed to execute code agent: llm: max retry attempts reached',
  resolver_type: 'AutoOptimize',
  resolver_display_name: 'nudgebee',
  type: 'PullRequest',
  type_reference_id: 'https://github.com/nudgebee/example/pull/1',
  created_at: '2026-08-21T10:00:00Z',
  updated_at: '2026-08-21T10:15:00Z',
  data: { data: { 'workflow-server': { cpu: { request: '81m' }, memory: { request: 402258739 } } } },
  recommendation: {
    recommendation: {},
    rule_name: 'pod_right_sizing',
    severity: 'Critical',
    estimated_savings: 16.2,
    status: 'InProgress',
    cloud_resourse: { name: 'workflow-server', meta: {} },
  },
};

const FULL_RECOMMENDATION = {
  id: 'rec-1',
  status: 'InProgress',
  recommendation: {
    'workflow-server': [
      { resource: 'cpu', allocated: { request: 0.3 }, recommended: { request: 0.081 } },
      { resource: 'memory', allocated: { request: 1073741824 }, recommended: { request: 402258739 } },
    ],
  },
};

const renderPanel = (props: Partial<React.ComponentProps<typeof ResolutionDetailPanel>> = {}) =>
  render(
    <ResolutionDetailPanel
      open
      onClose={jest.fn()}
      resolution={RESOLUTION}
      accounts={{ 'acct-a': { name: 'k8s-dev', cloud_provider: 'K8S' } }}
      onRetry={jest.fn()}
      retrying={false}
      {...props}
    />
  );

describe('ResolutionDetailPanel', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetRecommendation.mockResolvedValue({ data: { recommendation: [FULL_RECOMMENDATION] } });
  });

  it('leads with the full failure reason rather than the truncation the table shows', async () => {
    renderPanel();

    await waitFor(() => expect(screen.getByText('This resolution failed')).toBeInTheDocument());
    expect(screen.getByText('Failed to execute code agent: llm: max retry attempts reached')).toBeInTheDocument();
  });

  it('reads the applied spec as a change, in the units the rest of the product uses', async () => {
    renderPanel();

    // The resolution failed, so nothing changed — the heading must not claim it did.
    await waitFor(() => expect(screen.getByText('What it would have changed')).toBeInTheDocument());
    expect(screen.queryByText('What changed')).not.toBeInTheDocument();
    // Raw bytes on the row; 384 Mi against the recommendation's 1.0 Gi before.
    await waitFor(() => expect(screen.getByText('384 Mi')).toBeInTheDocument());
    expect(screen.getByText('1.0 Gi')).toBeInTheDocument();
    expect(screen.getByText('81m')).toBeInTheDocument();
    expect(screen.getByText('300m')).toBeInTheDocument();
    expect(screen.queryByText('402258739')).not.toBeInTheDocument();
  });

  it('still shows the change when the recommendation behind it cannot be loaded', async () => {
    mockGetRecommendation.mockRejectedValue(new Error('boom'));
    renderPanel();

    // No before-state, but the applied value is still readable — a failed lookup
    // degrades the section rather than the panel.
    await waitFor(() => expect(screen.getByText('384 Mi')).toBeInTheDocument());
    expect(screen.queryByText('1.0 Gi')).not.toBeInTheDocument();
  });

  it('names the disagreement when a failure left the recommendation in progress', async () => {
    renderPanel();

    await waitFor(() => expect(screen.getByText('still marked in progress despite the failure')).toBeInTheDocument());
  });

  it('says a success has not closed its recommendation yet', async () => {
    renderPanel({ resolution: { ...RESOLUTION, status: 'Success', status_message: '' } });

    await waitFor(() => expect(screen.getByText('applied, but not yet closed')).toBeInTheDocument());
  });

  it('stays quiet when the two statuses agree', async () => {
    mockGetRecommendation.mockResolvedValue({ data: { recommendation: [{ ...FULL_RECOMMENDATION, status: 'Closed' }] } });
    renderPanel({ resolution: { ...RESOLUTION, status: 'Success', status_message: '' } });

    await waitFor(() => expect(screen.getByText('Closed')).toBeInTheDocument());
    expect(screen.queryByText(/applied, but not yet closed|despite the failure/)).not.toBeInTheDocument();
  });

  it('puts Retry in the footer action bar, where the recommendation panel puts its actions', async () => {
    const onRetry = jest.fn();
    renderPanel({ onRetry });

    const bar = await screen.findByTestId('resolution-action-bar');
    fireEvent.click(within(bar).getByText('Retry'));

    expect(onRetry).toHaveBeenCalledWith('res-1', 'acct-a');
  });

  it('offers no retry on a resolution that did not fail', async () => {
    renderPanel({ resolution: { ...RESOLUTION, status: 'Success', status_message: '' } });

    await waitFor(() => expect(screen.getByText('What changed')).toBeInTheDocument());
    expect(screen.queryByTestId('resolution-action-bar')).not.toBeInTheDocument();
  });

  it('offers a History tab only for the resolution type that has one', async () => {
    renderPanel();
    await waitFor(() => expect(screen.getByRole('tab', { name: /Details/ })).toBeInTheDocument());
    expect(screen.queryByRole('tab', { name: /History/ })).not.toBeInTheDocument();
  });

  it('says a success actually changed something', async () => {
    renderPanel({ resolution: { ...RESOLUTION, status: 'Success', status_message: '' } });

    await waitFor(() => expect(screen.getByText('What changed')).toBeInTheDocument());
  });

  it('reports how far each value moved', async () => {
    renderPanel();

    // 300m → 81m is a 73% cut; the size of the change is the point of it.
    await waitFor(() => expect(screen.getByText('-73%')).toBeInTheDocument());
  });

  it('shows command history on its own tab for a CLI execution', async () => {
    renderPanel({ resolution: { ...RESOLUTION, type_reference_id: 'cli_execution' } });

    const historyTab = await screen.findByRole('tab', { name: /History/ });
    // The tab exists but is not the landing view — the outcome still leads.
    expect(screen.queryByTestId('command-history')).not.toBeInTheDocument();

    fireEvent.click(historyTab);
    await waitFor(() => expect(screen.getByTestId('command-history')).toBeInTheDocument());
  });

  it('renders nothing when no resolution is selected', () => {
    const { container } = renderPanel({ resolution: null, open: false });
    expect(container).toBeEmptyDOMElement();
  });
});
