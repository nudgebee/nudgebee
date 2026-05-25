import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: {
    fetchLogs: jest.fn(),
  },
}));

jest.mock('@components1/cloudaccount/cloud-logs/CloudLogsQueryPanel', () => ({
  __esModule: true,
  default: React.forwardRef((props: any, ref: any) => {
    React.useImperativeHandle(ref, () => ({
      setQuery: jest.fn(),
    }));
    return (
      <div data-testid='query-panel'>
        <div data-testid='qp-provider'>{props.provider}</div>
        <button
          data-testid='qp-emit-valid'
          onClick={() =>
            props.onChange({
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
            props.onChange({
              query: '',
              region: '',
            })
          }
        >
          Emit Empty
        </button>
      </div>
    );
  }),
}));

jest.mock('@components1/cloudaccount/cloud-logs/CloudLogsQueryHelp', () => ({
  __esModule: true,
  default: () => <div data-testid='query-help'>help</div>,
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, extraOptions = [], dateTimeRange, heading }: any) => (
    <div data-testid='box-layout'>
      <h2>{heading}</h2>
      <div data-testid='extras'>{extraOptions}</div>
      {dateTimeRange?.enabled && (
        <button
          data-testid='date-range-shortcut'
          onClick={() =>
            dateTimeRange.onChange({
              startTime: 1000,
              endTime: 2000,
              shortcutClickTime: 60_000,
            })
          }
        >
          1m
        </button>
      )}
      {dateTimeRange?.enabled && (
        <button
          data-testid='date-range-absolute'
          onClick={() =>
            dateTimeRange.onChange({
              startTime: 1000,
              endTime: 2000,
              shortcutClickTime: 0,
            })
          }
        >
          Absolute
        </button>
      )}
      {children}
    </div>
  ),
}));

jest.mock('@components1/common/CustomDropdown', () => ({
  __esModule: true,
  default: ({ label, value, options, onChange }: any) => (
    <select data-testid={`dropdown-${label}`} value={value || ''} onChange={(e) => onChange(e, e.target.value)}>
      {(options || []).map((opt: any) => {
        const v = typeof opt === 'string' ? opt : opt.value;
        const l = typeof opt === 'string' ? opt : opt.label;
        return (
          <option key={v} value={v}>
            {l}
          </option>
        );
      })}
    </select>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, disabled }: any) => (
    <button data-testid={`btn-${text}`} onClick={onClick} disabled={disabled}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value || '—'}</span>,
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ id, headers, tableData, showExpandable }: any) => (
    <div data-testid='custom-table' id={id}>
      <div data-testid='headers'>{headers.map((h: any) => h.name).join('|')}</div>
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

import CloudLogsViewer from '@components1/cloudaccount/cloud-logs/CloudLogsViewer';

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
  data: { data: { logs_query: logs } },
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

  it('shows initial info Alert when AWS and no logGroup selected', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByText(/Select a region and log group/)).toBeInTheDocument());
  });

  it('shows initial info Alert when Azure and no resourceId selected', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='Azure' />);
    await waitFor(() => expect(screen.getByText(/Select a Log Analytics Workspace/)).toBeInTheDocument());
  });

  it('shows error Alert when Run Query pressed without AWS log group', async () => {
    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-empty')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('qp-emit-empty'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText('Please select a log group')).toBeInTheDocument());
    expect(observability.fetchLogs).not.toHaveBeenCalled();
  });

  it('shows error Alert when Run Query pressed without Azure resourceId', async () => {
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
    // dynamic label keys present
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
    // shortcutClickTime=60_000 → start_time = now - 60_000, end_time = now
    expect(payload.end_time - payload.start_time).toBe(60_000);
  });

  it('shows error Alert when fetchLogs rejects', async () => {
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

  it('shows "No log entries found" Alert when empty result + provider params present', async () => {
    observability.fetchLogs.mockResolvedValue(mockLogsResponse([]));

    render(<CloudLogsViewer accountId='acc-1' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('qp-emit-valid')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('qp-emit-valid'));
    fireEvent.click(screen.getByTestId('btn-Run Query'));

    await waitFor(() => expect(screen.getByText(/No log entries found/)).toBeInTheDocument());
  });
});
