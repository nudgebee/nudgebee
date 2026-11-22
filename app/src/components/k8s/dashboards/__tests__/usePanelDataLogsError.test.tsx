import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { usePanelData } from '../usePanelData';
import type { AccountOption, Panel } from '@api1/dashboards';

/*
 * A logs panel that could not load said which account failed and dropped why, so
 * a provider that needs a default, or an index, read as a broken panel.
 */

const fetchLogs = jest.fn();

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: jest.fn(), fetchLogs: (...args: unknown[]) => fetchLogs(...args) },
}));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: {},
  isCommandDatasource: () => false,
}));

jest.mock('../panelAccounts', () => ({
  resolvePanelAccounts: () => [{ value: 'acc-1', label: 'Account 1' }],
  panelQueryAccounts: () => ({ accounts: [{ value: 'acc-1', label: 'Account 1' }] }),
}));

const accounts = [{ value: 'acc-1', label: 'Account 1' }] as AccountOption[];

const panel = {
  id: 1,
  title: 'Logs',
  type: 'table',
  datasource: 'logs',
  account_ids: ['acc-1'],
  grid_pos: { x: 0, y: 0, w: 6, h: 8 },
  targets: [{ ref_id: 'A', expr: '{app="x"}' }],
} as Panel;

const render = () => renderHook(() => usePanelData({ panel, accounts, startTime: 0, endTime: 1000, variables: {}, immediate: true } as never));

afterEach(() => {
  cleanup();
  fetchLogs.mockReset();
});

describe('a logs panel that fails', () => {
  it("shows the provider's reason from the gateway", async () => {
    fetchLogs.mockResolvedValue({ data: { errors: [{ message: 'none is marked as default' }] } });
    const { result } = render();
    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect(result.current.error).toMatchObject({ message: 'Could not load logs for Account 1.', detail: 'none is marked as default' });
  });

  it('shows the reason a rejected request gave', async () => {
    fetchLogs.mockRejectedValue(new Error('gateway timeout'));
    const { result } = render();
    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect(result.current.error).toMatchObject({ detail: 'gateway timeout' });
  });

  it('adds no detail when there is no reason to show', async () => {
    fetchLogs.mockRejectedValue(new Error(''));
    const { result } = render();
    await waitFor(() => expect(result.current.error).not.toBeNull());
    expect(result.current.error).not.toHaveProperty('detail');
  });
});
