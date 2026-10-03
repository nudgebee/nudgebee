// Guards the destructive half of the Coverage tab: unticking an account
// deactivates every node/edge it owns immediately (FilterRepository
// .softDeleteRemovedAccounts), and only the next hourly rebuild brings them back.
// These tests pin the warning to what the save ACTUALLY sends — the bug worth
// preventing is a banner that promises a removal the backend never makes.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KGCoverageTab from '../KGCoverageTab';
import apiKnowledgeGraph from '@api1/knowledge-graph';

jest.mock('@api1/knowledge-graph', () => ({
  __esModule: true,
  default: { getCloudAccounts: jest.fn(), getTenantFilter: jest.fn(), upsertTenantFilter: jest.fn() },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const ACCOUNTS = [
  { id: 'a1', account_name: 'Prod AWS', account_number: '111', cloud_provider: 'aws', created_at: '2026-02-01T00:00:00Z' },
  { id: 'a2', account_name: 'Staging GCP', account_number: '222', cloud_provider: 'gcp', created_at: '2026-01-01T00:00:00Z' },
];

// `account_ids: []` / `flow_sources: []` is the common real state — the hourly
// cron pre-creates the default row that way, and both sides read it as "all".
const mockLoad = (filter = { account_ids: [], flow_sources: [] }) => {
  apiKnowledgeGraph.getCloudAccounts.mockResolvedValue({ data: { data: { cloud_accounts: { rows: ACCOUNTS } } } });
  apiKnowledgeGraph.getTenantFilter.mockResolvedValue({ data: { data: { kg_get_tenant_filter: filter } } });
  apiKnowledgeGraph.upsertTenantFilter.mockResolvedValue({
    data: { data: { kg_upsert_tenant_filter: { removed_accounts: [], removed_flow_sources: [] } } },
  });
};

const renderTab = async () => {
  render(<KGCoverageTab open onClose={jest.fn()} onSaved={jest.fn()} />);
  await screen.findByRole('checkbox', { name: /Prod AWS/ });
};

beforeEach(() => {
  jest.clearAllMocks();
  mockLoad();
});

it('shows no warning until the selection actually removes something', async () => {
  await renderTab();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('warns which account loses its nodes, and confirms before saving', async () => {
  await renderTab();
  fireEvent.click(screen.getByRole('checkbox', { name: /Prod AWS/ }));

  const warning = screen.getByRole('alert');
  expect(warning).toHaveTextContent(/Saving will remove data from the graph/);
  expect(warning).toHaveTextContent(/Prod AWS/);
  expect(warning).not.toHaveTextContent(/Staging GCP/);
  expect(warning).toHaveTextContent(/next hourly rebuild/);

  // The destructive save must not go through on the first click.
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(apiKnowledgeGraph.upsertTenantFilter).not.toHaveBeenCalled();

  await screen.findByText('Remove coverage from the Knowledge Graph?');
  fireEvent.click(screen.getByRole('button', { name: 'Remove and save' }));
  await waitFor(() => expect(apiKnowledgeGraph.upsertTenantFilter).toHaveBeenCalledWith({ accountIds: ['a2'], flowSources: [] }));
});

it('abandons the removal when the confirm is cancelled', async () => {
  await renderTab();
  fireEvent.click(screen.getByRole('checkbox', { name: /Prod AWS/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await screen.findByText('Remove coverage from the Knowledge Graph?');

  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByText('Remove coverage from the Knowledge Graph?')).not.toBeInTheDocument());
  expect(apiKnowledgeGraph.upsertTenantFilter).not.toHaveBeenCalled();
});

it('warns about a removed flow source too, since the same Save drops its edges', async () => {
  await renderTab();
  fireEvent.click(screen.getByRole('checkbox', { name: /eBPF/ }));

  const warning = screen.getByRole('alert');
  expect(warning).toHaveTextContent(/eBPF/);
  expect(warning).toHaveTextContent(/lose every edge they created/);
});

it('saves straight through when coverage is only added', async () => {
  mockLoad({ account_ids: ['a1'], flow_sources: [] });
  await renderTab();
  fireEvent.click(screen.getByRole('checkbox', { name: /Staging GCP/ }));

  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(screen.queryByText('Remove coverage from the Knowledge Graph?')).not.toBeInTheDocument();
  // Full selection collapses to [] so accounts added later stay covered.
  await waitFor(() => expect(apiKnowledgeGraph.upsertTenantFilter).toHaveBeenCalledWith({ accountIds: [], flowSources: [] }));
});

it('does not claim a removal when every account is unticked, because empty means all', async () => {
  await renderTab();
  fireEvent.click(screen.getByRole('checkbox', { name: /Prod AWS/ }));
  fireEvent.click(screen.getByRole('checkbox', { name: /Staging GCP/ }));

  const warning = screen.getByRole('alert');
  expect(warning).toHaveTextContent(/saves as “all” rather than “none”/);
  expect(warning).not.toHaveTextContent(/Saving will remove data from the graph/);

  // No removals to confirm: the payload resolves back to full coverage.
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(screen.queryByText('Remove coverage from the Knowledge Graph?')).not.toBeInTheDocument();
  await waitFor(() => expect(apiKnowledgeGraph.upsertTenantFilter).toHaveBeenCalledWith({ accountIds: [], flowSources: [] }));
});

it('stays silent when the settings fail to load, instead of warning about an unpopulated form', async () => {
  apiKnowledgeGraph.getCloudAccounts.mockRejectedValue(new Error('boom'));
  apiKnowledgeGraph.getTenantFilter.mockRejectedValue(new Error('boom'));
  render(<KGCoverageTab open onClose={jest.fn()} onSaved={jest.fn()} />);

  // The load ended (no spinner) but nothing populated — the empty-selection
  // notice would be nonsense here, so it must not render.
  await waitFor(() => expect(screen.queryByText('Loading…')).not.toBeInTheDocument());
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('re-ticks everything after an all-unticked save, because that is what was stored', async () => {
  await renderTab();
  fireEvent.click(screen.getByRole('checkbox', { name: /Prod AWS/ }));
  fireEvent.click(screen.getByRole('checkbox', { name: /Staging GCP/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));

  await waitFor(() => expect(apiKnowledgeGraph.upsertTenantFilter).toHaveBeenCalled());
  // [] was stored as "all", so leaving the boxes unticked would show "nothing
  // on" over a graph that covers everything — the #32328 mismatch.
  await waitFor(() => expect(screen.getByRole('checkbox', { name: /Prod AWS/ })).toBeChecked());
  expect(screen.getByRole('checkbox', { name: /Staging GCP/ })).toBeChecked();
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
