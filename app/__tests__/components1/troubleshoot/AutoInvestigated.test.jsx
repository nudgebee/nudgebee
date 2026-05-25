import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockRouterReplace = jest.fn();
let mockRouterQuery = {};
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: mockRouterReplace,
    query: mockRouterQuery,
    pathname: '/troubleshoot',
    asPath: '/troubleshoot',
    route: '/troubleshoot',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('@lib/router', () => ({
  applyFiltersOnRouter: jest.fn(),
}));

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    llmConversationHistoryForInvestigation: jest.fn(),
  },
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sEvents: jest.fn(),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getCloudAccounts: jest.fn(),
  },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' }, border: { secondary: '#ddd' } },
}));

jest.mock('@components1/common', () => ({
  BoxLayout2: ({ children, filterOptions = [], dateTimeRange }) => (
    <div data-testid='box-layout'>
      {filterOptions.map((f, i) => {
        if (f.type === 'dropdown') {
          return (
            <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={f.onSelect}>
              <option value=''>--</option>
              {(f.options || []).map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          );
        }
        if (f.type === 'multi-dropdown') {
          return (
            <button
              key={i}
              data-testid={`multi-${f.label}`}
              onClick={() => {
                const first = (f.options || [])[0];
                if (first) f.onSelect({}, [first]);
              }}
            >
              {f.label}
            </button>
          );
        }
        return null;
      })}
      {dateTimeRange?.enabled && (
        <button data-testid='date-range-trigger' onClick={() => dateTimeRange.onChange({ startTime: 1000, endTime: 2000, shortcutClickTime: 1 })}>
          Set Date
        </button>
      )}
      {children}
    </div>
  ),
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ id, tableData, totalRows, loading, onPageChange, pageNumber, emptyStateText }) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='table-loading'>loading</div>}
      <div data-testid='table-total'>{totalRows}</div>
      <div data-testid='table-page'>{pageNumber}</div>
      {emptyStateText && <div data-testid='empty-msg'>{emptyStateText}</div>}
      {(tableData || []).map((row, i) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell, j) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2, 10)}>
        Next
      </button>
    </div>
  ),
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text, variant }) => <span data-testid={`label-${variant || 'plain'}`}>{text}</span>,
}));

jest.mock('@components1/common/widgets/SeverityIcon', () => ({
  __esModule: true,
  default: ({ severityType }) => <span data-testid={`severity-${severityType || 'none'}`}>sev</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{value}</span>,
}));

jest.mock('@components1/common/InvestigateButton', () => ({
  __esModule: true,
  default: ({ url }) => (
    <a data-testid='investigate-link' href={url}>
      Investigate
    </a>
  ),
}));

jest.mock('@components1/common/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }) => <span data-testid={`cloud-${cloud_provider}`}>cloud</span>,
}));

jest.mock('@components1/k8s/common/ClusterNameWithRegion', () => ({
  __esModule: true,
  default: ({ name }) => <span data-testid='cluster-name'>{name}</span>,
}));

import AutoInvestigated from '@components1/troubleshoot/AutoInvestigated';

const apiAskNudgebee = require('@api1/ask-nudgebee').default;
const k8sApi = require('@api1/kubernetes').default;
const apiHome = require('@api1/home').default;
const { applyFiltersOnRouter } = require('@lib/router');

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod', cloud_provider: 'aws' },
  { id: 'acc-2', account_name: 'GCP Dev', cloud_provider: 'gcp' },
];

const sampleConversations = [
  { id: 'conv-1', title: 'Investigate 11111111-1111-1111-1111-111111111111 issue' },
  { id: 'conv-2', title: 'Pod restart 22222222-2222-2222-2222-222222222222' },
];

const sampleEvents = [
  {
    id: '11111111-1111-1111-1111-111111111111',
    account_id: 'acc-1',
    subject_name: 'pod-a',
    subject_namespace: 'default',
    title: 'PodCrashLoopBackOff event detected',
    priority: 'high',
    urgency: 'P1',
    status: 'FIRING',
    updated_at: '2026-05-15T10:00:00Z',
  },
  {
    id: '22222222-2222-2222-2222-222222222222',
    account_id: 'acc-2',
    subject_name: 'pod-b',
    subject_namespace: 'kube-system',
    title: 'Memory pressure event',
    priority: 'low',
    urgency: 'P3',
    status: 'CLOSED',
    updated_at: '2026-05-15T11:00:00Z',
  },
];

const mockConvResponse = (items = sampleConversations) => ({
  data: { data: { llm_conversations: items, llm_conversations_aggregate: { aggregate: { count: items.length } } } },
});

describe('AutoInvestigated (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    apiHome.getCloudAccounts.mockResolvedValue(sampleAccounts);
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue(mockConvResponse());
    k8sApi.getK8sEvents.mockResolvedValue({ data: { events: sampleEvents } });
  });

  it('fetches accounts and conversations on mount with default filters', async () => {
    render(<AutoInvestigated />);

    await waitFor(() => {
      expect(apiHome.getCloudAccounts).toHaveBeenCalledTimes(1);
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    const call = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(call).toMatchObject({
      source: 'Investigation',
      account_id: undefined,
      limit: 10,
      offset: 0,
      extractEventIdsFromTitle: true,
      event_status: undefined,
    });
  });

  it('extracts event UUIDs from conversation titles and fetches K8s events', async () => {
    render(<AutoInvestigated />);

    await waitFor(() => expect(k8sApi.getK8sEvents).toHaveBeenCalled());
    const [limit, offset, opts] = k8sApi.getK8sEvents.mock.calls[0];
    expect(limit).toBe(10);
    expect(offset).toBe(0);
    expect(opts.eventIds).toEqual(['11111111-1111-1111-1111-111111111111', '22222222-2222-2222-2222-222222222222']);
    expect(opts.timeFilter).toBe(false);
  });

  it('renders event rows from k8s events response', async () => {
    render(<AutoInvestigated />);

    await waitFor(() => expect(screen.getByText('pod-a')).toBeInTheDocument());
    expect(screen.getByText('pod-b')).toBeInTheDocument();
    expect(screen.getByText('PodCrashLoopBackOff event detected')).toBeInTheDocument();
    expect(screen.getByTestId('label-red')).toHaveTextContent('Open');
    expect(screen.getByTestId('label-grey')).toHaveTextContent('Closed');
  });

  it('refetches with event_status when status dropdown changes', async () => {
    render(<AutoInvestigated />);
    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'FIRING' } });

    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    expect(apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0].event_status).toBe('FIRING');
  });

  it('updates router and refetches when account multi-dropdown selected', async () => {
    render(<AutoInvestigated />);
    await waitFor(() => expect(screen.getByTestId('multi-Account')).toBeInTheDocument());
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.click(screen.getByTestId('multi-Account'));

    await waitFor(() => {
      expect(applyFiltersOnRouter).toHaveBeenCalledWith(expect.anything(), { accountId: 'acc-1' });
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    expect(apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0].account_id).toEqual(['acc-1']);
  });

  it('initializes account filter from router query', async () => {
    mockRouterQuery = { accountId: 'acc-1,acc-2' };
    render(<AutoInvestigated />);

    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    expect(apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0].account_id).toEqual(['acc-1', 'acc-2']);
  });

  it('refetches with new date range and resets page', async () => {
    render(<AutoInvestigated />);
    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.click(screen.getByTestId('date-range-trigger'));

    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    const call = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(call.startUpdatedAt).toBe(new Date(1000).toISOString());
    expect(call.endUpdatedAt).toBe(new Date(2000).toISOString());
  });

  it('paginates and updates offset on next page', async () => {
    render(<AutoInvestigated />);
    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled());
    expect(apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0].offset).toBe(10);
  });

  it('shows empty message when conversations have no extractable UUIDs', async () => {
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue(mockConvResponse([{ id: 'x', title: 'no uuid here' }]));

    render(<AutoInvestigated />);

    await waitFor(() => expect(screen.getByTestId('empty-msg')).toBeInTheDocument());
    expect(screen.getByTestId('empty-msg')).toHaveTextContent(/Could not match any events/);
    expect(k8sApi.getK8sEvents).not.toHaveBeenCalled();
  });

  it('handles empty conversation list without calling getK8sEvents', async () => {
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue(mockConvResponse([]));

    render(<AutoInvestigated />);

    await waitFor(() => expect(screen.getByTestId('table-total')).toHaveTextContent('0'));
    expect(k8sApi.getK8sEvents).not.toHaveBeenCalled();
  });

  it('shows loading state during conv fetch', async () => {
    let resolveFn;
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<AutoInvestigated />);
    expect(screen.getByTestId('table-loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn(mockConvResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('table-loading')).not.toBeInTheDocument());
  });

  it('renders investigate link with correct URL per row', async () => {
    render(<AutoInvestigated />);

    await waitFor(() => expect(screen.getAllByTestId('investigate-link')).toHaveLength(2));
    const links = screen.getAllByTestId('investigate-link');
    expect(links[0]).toHaveAttribute('href', '/investigate?id=11111111-1111-1111-1111-111111111111&accountId=acc-1&autoInvestigate=true');
    expect(links[1]).toHaveAttribute('href', '/investigate?id=22222222-2222-2222-2222-222222222222&accountId=acc-2&autoInvestigate=true');
  });
});
