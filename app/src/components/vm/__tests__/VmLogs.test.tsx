import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import VmLogs from '@components/vm/VmLogs';
import apiAccount from '@api1/account';
import observability from '@api1/observability';

const push = jest.fn();
jest.mock('next/router', () => ({ useRouter: () => ({ push }) }));
jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: jest.fn() } }));
jest.mock('@api1/observability', () => ({ __esModule: true, default: { fetchLogs: jest.fn() } }));
jest.mock('@lib/HttpService', () => ({
  unwrapGraphQL: (response: any, fallback: string) => {
    const errors = response?.data?.errors;
    if (errors?.length) throw new Error(errors[0]?.message || fallback);
    return response?.data?.data;
  },
}));

// The builder and the date picker have their own suites and heavy dependencies; the
// stubs expose just the callbacks this tab consumes.
jest.mock('@components/k8s/common/QueryModeSwitcher', () => ({
  __esModule: true,
  default: ({ onQueryChange, qLEditor, logProvider }: any) => (
    <div>
      <span data-testid='switcher'>{`${logProvider}:${qLEditor}`}</span>
      <button onClick={() => onQueryChange({ query: JSON.stringify([{ _binary: { host: { _eq: 'vm-1' } } }]) })}>pick-filter</button>
      <button onClick={() => onQueryChange({ query: '{job="nb-forager-test"}' })}>write-logql</button>
    </div>
  ),
}));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => <div /> }));
jest.mock('@shared/buttons/DownloadButton', () => ({ __esModule: true, default: () => <div /> }));
jest.mock('@components/cloudaccount/cloud-logs/CloudLogsTable', () => ({
  __esModule: true,
  default: ({ logs }: any) => (
    <ul>
      {logs.map((l: any, i: number) => (
        <li key={i}>{l.message}</li>
      ))}
    </ul>
  ),
}));

const provider = (over: object = {}) => ({
  data: { data: { observability_get_default_provider: { provider: 'loki', integration_source: 'user', available_providers: [], ...over } } },
});
const logsResponse = (logs: object[], query = '') => ({ data: { data: { logs_list: { logs, query } } } });

describe('VmLogs', () => {
  beforeEach(() => jest.clearAllMocks());

  it('offers to connect a log store when the account has none', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(provider({ provider: '' }));
    render(<VmLogs accountId='acc-1' />);
    await userEvent.setup().click(await screen.findByRole('button', { name: 'Connect a log store' }));
    expect(push).toHaveBeenCalledWith('/user-management?integration=observability#integrations');
    expect(apiAccount.getDefaultProvider).toHaveBeenCalledWith({ account_id: 'acc-1', provider_type: 'logs' });
  });

  it('shows the resolver error verbatim, such as several providers and no default', async () => {
    const message = 'observability: multiple logs providers are configured (vm-loki, vm-loki), but none is marked as default';
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue({ data: { errors: [{ message }] } });
    render(<VmLogs accountId='acc-1' />);
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Run Query' })).not.toBeInTheDocument();
  });

  it('shows a failure when the default provider cannot be resolved', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockRejectedValue(new Error('Network Error'));
    render(<VmLogs accountId='acc-1' />);
    expect(await screen.findByText('Network Error')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Run Query' })).not.toBeInTheDocument();
  });

  it('runs a builder filter against the default provider and lists the lines', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(provider());
    (observability.fetchLogs as jest.Mock).mockResolvedValue(
      logsResponse([{ timestamp: '2026-10-13T07:41:29Z', message: 'forager loki test line', severity: 'info', labels: {} }], '{host="vm-1"}')
    );
    const user = userEvent.setup();
    render(<VmLogs accountId='acc-1' />);

    expect(await screen.findByTestId('switcher')).toHaveTextContent('loki:build');
    await user.click(screen.getByText('pick-filter'));
    await user.click(screen.getByRole('button', { name: 'Run Query' }));

    expect(await screen.findByText('forager loki test line')).toBeInTheDocument();
    expect(screen.getByText('{host="vm-1"}')).toBeInTheDocument();
    const payload = (observability.fetchLogs as jest.Mock).mock.calls[0][0];
    expect(payload).toMatchObject({
      account_id: 'acc-1',
      log_provider: 'loki',
      log_provider_source: 'user',
      query: '',
      limit: 100,
      query_request: { where: { _and: [{ _binary: { host: { _eq: 'vm-1' } } }] } },
    });
  });

  it('does not query in Builder mode until a filter is chosen', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(provider());
    const user = userEvent.setup();
    render(<VmLogs accountId='acc-1' />);
    await user.click(await screen.findByRole('button', { name: 'Run Query' }));
    expect(await screen.findByText(/Select at least one label filter/)).toBeInTheDocument();
    expect(observability.fetchLogs).not.toHaveBeenCalled();
  });

  it('sends a Code-mode query as written', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(provider());
    (observability.fetchLogs as jest.Mock).mockResolvedValue(logsResponse([]));
    const user = userEvent.setup();
    render(<VmLogs accountId='acc-1' />);

    await screen.findByTestId('switcher');
    await user.click(screen.getByText('Code'));
    await user.click(screen.getByText('write-logql'));
    await user.click(screen.getByRole('button', { name: 'Run Query' }));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    const payload = (observability.fetchLogs as jest.Mock).mock.calls[0][0];
    expect(payload.query).toBe('{job="nb-forager-test"}');
    expect(payload.query_request).toBeUndefined();
    expect(await screen.findByText('No log entries')).toBeInTheDocument();
  });

  it('surfaces a failed query instead of an empty result', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(provider());
    (observability.fetchLogs as jest.Mock).mockResolvedValue({ data: { errors: [{ message: 'parse error at line 1' }] } });
    const user = userEvent.setup();
    render(<VmLogs accountId='acc-1' />);
    await screen.findByTestId('switcher');
    await user.click(screen.getByText('pick-filter'));
    await user.click(screen.getByRole('button', { name: 'Run Query' }));
    expect(await screen.findByText('parse error at line 1')).toBeInTheDocument();
    expect(screen.queryByText('No log entries')).not.toBeInTheDocument();
  });
});
