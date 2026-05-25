import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockRouterReplace = jest.fn();
const mockRouterPush = jest.fn();
let mockRouterQuery = {};

jest.mock('next/router', () => ({
  useRouter: () => ({
    push: mockRouterPush,
    replace: mockRouterReplace,
    pathname: '/troubleshoot',
    query: mockRouterQuery,
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
  colors: {
    primary: '#3B82F6',
    text: { primary: '#000', secondary: '#666', white: '#fff', tertiary: '#999', disabled: '#ccc' },
    background: { white: '#fff', primaryLightest: '#eef', input: '#f9f9f9' },
    border: { secondary: '#ddd', primary: '#3B82F6', secondaryLight: '#eee' },
    button: { primary: '#3B82F6', primaryText: '#fff', secondary: '#fff', secondaryText: '#333', secondaryBorder: '#ddd' },
  },
}));

jest.mock('@components1/common', () => ({
  BoxLayout2: ({ children, filterOptions = [], dateTimeRange }) => (
    <div data-testid='box-layout'>
      <div data-testid='filters'>
        {filterOptions.map((f, i) => {
          if (f.type === 'search') {
            return (
              <input
                key={i}
                data-testid={`filter-search-${f.label}`}
                value={f.value || ''}
                onChange={(e) => f.onSelect(e)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && f.onEnter) f.onEnter();
                }}
              />
            );
          }
          if (f.type === 'dropdown') {
            return (
              <select key={i} data-testid={`filter-dropdown-${f.label}`} value={f.value || ''} onChange={(e) => f.onSelect(e)}>
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
                data-testid={`filter-multidropdown-${f.label}`}
                onClick={() => {
                  const first = (f.options || [])[0];
                  if (first) f.onSelect({}, [first]);
                }}
              >
                {f.label}: {(f.value || []).map((v) => v.label).join(',')}
              </button>
            );
          }
          return null;
        })}
      </div>
      {dateTimeRange?.enabled && (
        <button
          data-testid='date-range-trigger'
          onClick={() =>
            dateTimeRange.onChange({
              startTime: 1000,
              endTime: 2000,
              shortcutClickTime: 1,
            })
          }
        >
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
  default: ({ id, tableData, headers, totalRows, loading, onPageChange, pageNumber }) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='table-loading'>Loading...</div>}
      <div data-testid='table-total'>{totalRows}</div>
      <div data-testid='table-page'>{pageNumber}</div>
      <table>
        <thead>
          <tr>
            {headers.map((h) => (
              <th key={h.name}>{h.name}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {(tableData || []).map((row, i) => (
            <tr key={i} data-testid={`row-${i}`}>
              {row.map((cell, j) => (
                <td key={j}>{cell.component || cell.data}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <button data-testid='next-page' onClick={() => onPageChange(2, 10)}>
        Next
      </button>
    </div>
  ),
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text, variant }) => <span data-testid={`label-${variant}`}>{text}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{value}</span>,
}));

jest.mock('@components1/common/InvestigateButton', () => ({
  __esModule: true,
  default: ({ text, onClick }) => (
    <button data-testid='investigate-btn' onClick={onClick}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }) => <span data-testid={`cloud-${cloud_provider}`}>cloud</span>,
}));

jest.mock('@assets', () => ({
  UserIcon: '/user-icon.svg',
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s) =>
    String(s || '')
      .toLowerCase()
      .replace(/_./g, (m) => ' ' + m[1].toUpperCase())
      .replace(/^./, (c) => c.toUpperCase()),
}));

import ManualInvestigated from '@components1/troubleshoot/ManualInvestigated';

const apiAskNudgebee = require('@api1/ask-nudgebee').default;
const apiHome = require('@api1/home').default;
const apiUser = require('@api1/user').default;
const { applyFiltersOnRouter } = require('@lib/router');

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod', cloud_provider: 'aws' },
  { id: 'acc-2', account_name: 'GCP Dev', cloud_provider: 'gcp' },
];

const sampleConversations = [
  {
    id: 'conv-1',
    title: 'Investigate pod crash',
    account_id: 'acc-1',
    session_id: 'sess-1',
    status: 'COMPLETED',
    updated_at: '2026-05-15T10:00:00Z',
    user: { display_name: 'Alice' },
  },
  {
    id: 'conv-2',
    title: 'Cost analysis',
    account_id: 'acc-2',
    session_id: 'sess-2',
    status: 'IN_PROGRESS',
    updated_at: '2026-05-15T11:00:00Z',
    user: { display_name: 'Bob' },
  },
];

const mockListResponse = (items = sampleConversations) => ({
  data: {
    data: {
      llm_conversations: items,
      llm_conversations_aggregate: { aggregate: { count: items.length } },
    },
  },
});

describe('ManualInvestigated (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    apiHome.getCloudAccounts.mockResolvedValue(sampleAccounts);
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue(mockListResponse());
    apiUser.getUserPreferencesTablePageSize.mockReturnValue(10);
  });

  it('loads cloud accounts and conversation list on mount', async () => {
    render(<ManualInvestigated />);

    await waitFor(() => {
      expect(apiHome.getCloudAccounts).toHaveBeenCalledTimes(1);
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });

    const firstCall = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(firstCall.source).toBe('UserInvestigation');
    expect(firstCall.limit).toBe(10);
    expect(firstCall.offset).toBe(0);
    expect(firstCall.status).toBe('');
    expect(firstCall.account_id).toBeUndefined();
  });

  it('renders rows from API response with user, title, and status badge', async () => {
    render(<ManualInvestigated />);

    await waitFor(() => {
      expect(screen.getByText('Alice')).toBeInTheDocument();
    });
    expect(screen.getByText('Bob')).toBeInTheDocument();
    expect(screen.getByText('Investigate pod crash')).toBeInTheDocument();
    expect(screen.getByText('Cost analysis')).toBeInTheDocument();
    expect(screen.getByTestId('label-green')).toHaveTextContent('Completed');
    expect(screen.getByTestId('label-yellow')).toHaveTextContent('In Progress');
  });

  it('displays total row count from aggregate', async () => {
    render(<ManualInvestigated />);
    await waitFor(() => {
      expect(screen.getByTestId('table-total')).toHaveTextContent('2');
    });
  });

  it('shows loading state while fetching', async () => {
    let resolveFn;
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<ManualInvestigated />);
    expect(screen.getByTestId('table-loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn(mockListResponse());
    });

    await waitFor(() => {
      expect(screen.queryByTestId('table-loading')).not.toBeInTheDocument();
    });
  });

  it('initializes selectedAccountId from router query and includes in API call', async () => {
    mockRouterQuery = { accountId: 'acc-1,acc-2' };
    render(<ManualInvestigated />);

    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });

    const call = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(call.account_id).toEqual(['acc-1', 'acc-2']);
  });

  it('refetches with new status when status dropdown changes', async () => {
    render(<ManualInvestigated />);
    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.change(screen.getByTestId('filter-dropdown-By Status'), {
      target: { value: 'FAILED' },
    });

    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    const call = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(call.status).toBe('FAILED');
  });

  it('updates router and refetches when account multi-dropdown changes', async () => {
    render(<ManualInvestigated />);
    await waitFor(() => {
      expect(screen.getByTestId('filter-multidropdown-Account')).toBeInTheDocument();
    });
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.click(screen.getByTestId('filter-multidropdown-Account'));

    await waitFor(() => {
      expect(applyFiltersOnRouter).toHaveBeenCalledWith(expect.anything(), {
        accountId: 'acc-1',
      });
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
  });

  it('refetches with new date range and resets page', async () => {
    render(<ManualInvestigated />);
    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.click(screen.getByTestId('date-range-trigger'));

    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    const call = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(call.startUpdatedAt).toBe(new Date(1000).toISOString());
    expect(call.endUpdatedAt).toBe(new Date(2000).toISOString());
  });

  it('updates page and rowsPerPage when pagination changes', async () => {
    render(<ManualInvestigated />);
    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => {
      expect(apiAskNudgebee.llmConversationHistoryForInvestigation).toHaveBeenCalled();
    });
    const call = apiAskNudgebee.llmConversationHistoryForInvestigation.mock.calls[0][0];
    expect(call.offset).toBe(10);
    expect(call.limit).toBe(10);
  });

  it('opens conversation in new tab when investigate button clicked', async () => {
    const openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
    render(<ManualInvestigated />);

    await waitFor(() => {
      expect(screen.getAllByTestId('investigate-btn')).toHaveLength(2);
    });

    fireEvent.click(screen.getAllByTestId('investigate-btn')[0]);

    expect(openSpy).toHaveBeenCalledWith('/ask-nudgebee?accountId=acc-1&session_id=sess-1', '_blank', 'noopener,noreferrer');
    openSpy.mockRestore();
  });

  it('handles empty conversation list gracefully', async () => {
    apiAskNudgebee.llmConversationHistoryForInvestigation.mockResolvedValue(mockListResponse([]));

    render(<ManualInvestigated />);

    await waitFor(() => {
      expect(screen.getByTestId('table-total')).toHaveTextContent('0');
    });
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
