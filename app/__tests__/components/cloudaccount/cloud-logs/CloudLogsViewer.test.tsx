import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: {
    fetchLogs: jest.fn(),
  },
}));

// Source consumes `useCloudLogsQueryPanel` (a hook) instead of the old QueryPanel component.
// We mock the hook to: (a) stash the `onChange` callback into JSX buttons so tests can
// simulate user picking valid / empty params, and (b) expose `provider` via testid.
jest.mock('@components/cloudaccount/cloud-logs/CloudLogsQueryPanel', () => ({
  __esModule: true,
  useCloudLogsQueryPanel: ({ provider, onChange }: any) => ({
    filters: (
      <>
        <div data-testid='qp-provider'>{provider}</div>
        <button
          data-testid='qp-emit-valid'
          onClick={() =>
            onChange({
              query: 'fields @timestamp',
              region: 'us-east-1',
              logGroup: '/aws/lambda/x',
              resourceId: 'workspace-1',
            })
          }
        >
          Emit Valid
        </button>
        <button
          data-testid='qp-emit-empty'
          onClick={() =>
            onChange({
              query: '',
              region: '',
            })
          }
        >
          Emit Empty
        </button>
      </>
    ),
    textarea: <div data-testid='query-panel'>panel</div>,
    regionHint: '',
    setQuery: jest.fn(),
  }),
}));

jest.mock('@components/cloudaccount/cloud-logs/CloudLogsQueryHelp', () => ({
  __esModule: true,
  default: () => <div data-testid='query-help'>help</div>,
}));

jest.mock('@utils/colors');

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value || '—'}</span>,
}));

jest.mock('@shared/buttons/DownloadButton', () => ({
  __esModule: true,
  default: ({ onClick }: any) => (
    <button data-testid='download-btn' onClick={onClick}>
      DL
    </button>
  ),
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: ({ onChange }: any) => (
    <>
      <button
        data-testid='date-range-shortcut'
        onClick={() => onChange({ selection: { startTime: 1000, endTime: 2000, shortcutClickTime: 60_000 } })}
      >
        1m
      </button>
      <button data-testid='date-range-absolute' onClick={() => onChange({ selection: { startTime: 1000, endTime: 2000, shortcutClickTime: 0 } })}>
        Absolute
      </button>
    </>
  ),
}));

jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick, disabled }: any) => (
    <button data-testid={`btn-${typeof children === 'string' ? children : 'icon'}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Banner', () => ({
  __esModule: true,
  Banner: ({ message }: any) => <div data-testid='banner'>{message}</div>,
}));

jest.mock('@ui/EmptyState', () => ({
  __esModule: true,
  EmptyState: ({ title, description }: any) => (
    <div data-testid='empty-state'>
      <div>{title}</div>
      <div>{description}</div>
    </div>
  ),
}));

jest.mock('@ui/Chip', () => ({
  __esModule: true,
  Chip: ({ children }: any) => <span data-testid='chip'>{children}</span>,
}));

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout: any = ({ children, id }: any) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }: any) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }: any) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect }: any) => {
    const currentValue = typeof value === 'object' && value !== null ? value.value : value;
    return (
      <select
        data-testid={`dropdown-${label}`}
        value={currentValue || ''}
        onChange={(e) => onSelect?.(e, { value: e.target.value, label: e.target.value })}
      >
        {(options || []).map((opt: any, idx: number) => {
          const v = typeof opt === 'string' ? opt : opt.value;
          const l = typeof opt === 'string' ? opt : opt.label;
          return (
            <option key={(v || '_') + '-' + idx} value={v}>
              {l}
            </option>
          );
        })}
      </select>
    );
  },
}));

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ id, headers, tableData, showExpandable }: any) => (
    <div data-testid='custom-table' id={id}>
      <div data-testid='headers'>{(headers || []).map((h: any) => h.name).join('|')}</div>
      <div data-testid='expandable-enabled'>{String(!!showExpandable)}</div>
      {(tableData || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.text}
            </span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

import CloudLogsViewer from '@components/cloudaccount/cloud-logs/CloudLogsViewer';

const observability = require('@api1/observability').default;

const sampleLogsWithMessages = [
  { timestamp: '2026-05-15T10:00:00Z', message: 'Starting up', severity: 'info', labels: { region: 'us-east-1' } },
  { timestamp: '2026-05-15T10:01:00Z', message: 'Error processing', severity: 'error', labels: { region: 'us-east-1', pod: 'web-0' } },
];

const sampleLogsLabelsOnly = [
  { timestamp: '2026-05-15T10:00:00Z', message: '', severity: 'info', labels: { region: 'us-east-1', pod: 'web-0', container: 'app' } },
  { timestamp: '2026-05-15T10:01:00Z', message: '', severity: 'warning', labels: { region: 'us-east-1', pod: 'web-1', container: 'app' } },
];

const mockLogsResponse = (logs: any[] = sampleLogsWithMessages) => ({
  data: { data: { logs_list: logs } },
});

describe('CloudLogsViewer (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    observability.fetchLogs.mockResolvedValue(mockLogsResponse());
  });

  it('does not fetch on mount (params not yet emitted)', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await act(async () => {});
    expect(observability.fetchLogs).not.toHaveBeenCalled();
  });

  it('shows initial empty state when AWS and no logGroup selected', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByText(/Select a region and log group/)).toBeInTheDocument());
  });

  it('shows initial empty state when Azure and no resourceId selected', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='Azure' />);
    await waitFor(() => expect(screen.getByText(/Select a Log Analytics Workspace/)).toBeInTheDocument());
  });

  it('shows error Banner when Run Query pressed without AWS log group', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-empty')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-empty'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Please select a log group')).toBeInTheDocument());
    expect(observability.fetchLogs).not.toHaveBeenCalled();
  });

  it('shows error Banner when Run Query pressed without Azure resourceId', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='Azure' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-empty')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-empty'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Please select a Log Analytics Workspace')).toBeInTheDocument());
    expect(observability.fetchLogs).not.toHaveBeenCalled();
  });

  it('fetches with AWS payload including log_group when Run Query pressed', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    const payload = observability.fetchLogs.mock.calls[0][0];
    expect(payload).toMatchObject({
      account_id: 'acc-1',
      log_provider: 'aws_cloudwatch',
      log_provider_source: 'user',
      query: 'fields @timestamp',
      limit: 100,
      request: { region: 'us-east-1', log_group: '/aws/lambda/x' },
    });
    expect(payload.request.resource_id).toBeUndefined();
  });

  it('fetches with Azure payload including resource_id + azure_sql service_name', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='Azure' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    const payload = observability.fetchLogs.mock.calls[0][0];
    expect(payload.request).toMatchObject({
      region: 'us-east-1',
      resource_id: 'workspace-1',
      service_name: 'azure_sql',
    });
    expect(payload.request.log_group).toBeUndefined();
  });

  it('fetches with GCP payload including cloud sql service_name', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='GCP' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    const payload = observability.fetchLogs.mock.calls[0][0];
    expect(payload.request).toMatchObject({ region: 'us-east-1', service_name: 'cloud sql' });
    expect(payload.request.log_group).toBeUndefined();
    expect(payload.request.resource_id).toBeUndefined();
  });

  it('renders Timestamp + Message columns when logs have messages', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByTestId('custom-table')).toBeInTheDocument());
    expect(screen.getByTestId('headers')).toHaveTextContent('Timestamp|Message');
    expect(screen.getAllByTestId(/^row-/)).toHaveLength(2);
  });

  it('renders dynamic label columns when logs have only labels (no messages)', async () => {
    observability.fetchLogs.mockResolvedValue(mockLogsResponse(sampleLogsLabelsOnly));

    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByTestId('custom-table')).toBeInTheDocument());
    const headers = screen.getByTestId('headers').textContent;
    expect(headers).toMatch(/Timestamp/);
    expect(headers).toMatch(/region/);
    expect(headers).toMatch(/pod/);
    expect(headers).toMatch(/container/);
    expect(headers).not.toMatch(/Message/);
  });

  it('changes log limit via dropdown', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('dropdown-Limit'), { target: { value: '500' } });
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    expect(observability.fetchLogs.mock.calls[0][0].limit).toBe(500);
  });

  it('auto-fetches when date range changes after params have been emitted', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    observability.fetchLogs.mockClear();

    fireEvent.click(screen.getByTestId('date-range-absolute'));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    const payload = observability.fetchLogs.mock.calls[0][0];
    expect(payload.start_time).toBe(1000);
    expect(payload.end_time).toBe(2000);
  });

  it('does not auto-fetch on date change before AWS log group is set', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-empty')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-empty'));
    observability.fetchLogs.mockClear();

    fireEvent.click(screen.getByTestId('date-range-absolute'));

    await act(async () => {});
    expect(observability.fetchLogs).not.toHaveBeenCalled();
  });

  it('uses now-shortcut delta when shortcut date is selected', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    observability.fetchLogs.mockClear();

    fireEvent.click(screen.getByTestId('date-range-shortcut'));

    await waitFor(() => expect(observability.fetchLogs).toHaveBeenCalled());
    const payload = observability.fetchLogs.mock.calls[0][0];
    expect(payload.end_time - payload.start_time).toBe(60_000);
  });

  it('shows error Banner when fetchLogs rejects', async () => {
    observability.fetchLogs.mockRejectedValue({
      response: { data: { errors: [{ message: 'Quota exceeded' }] } },
    });

    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Quota exceeded')).toBeInTheDocument());
  });

  it('shows generic error message when no structured error in rejection', async () => {
    observability.fetchLogs.mockRejectedValue(new Error('Network down'));

    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Network down')).toBeInTheDocument());
  });

  it('shows "No log entries" empty state when empty result + provider params present', async () => {
    observability.fetchLogs.mockResolvedValue(mockLogsResponse([]));

    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText(/No log entries found/)).toBeInTheDocument());
  });
});
