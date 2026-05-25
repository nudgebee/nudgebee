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

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listAllEventResolutions: jest.fn(),
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

jest.mock('src/utils/common', () => ({
  containsLink: (val) => typeof val === 'string' && /^https?:\/\//.test(val),
  snakeToTitleCase: (s) =>
    String(s || '')
      .toLowerCase()
      .replace(/_./g, (m) => ' ' + m[1].toUpperCase())
      .replace(/^./, (c) => c.toUpperCase()),
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000', secondary: '#666' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common', () => ({
  BoxLayout2: ({ children, filterOptions = [] }) => (
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
      {children}
    </div>
  ),
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ id, tableData, totalRows, loading, pageNumber, onPageChange }) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      {(tableData || []).map((row, i) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell, j) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component || cell.text}
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

jest.mock('@components1/common/CustomLink', () => ({
  __esModule: true,
  default: ({ href, children }) => (
    <a data-testid='custom-link' href={href}>
      {children}
    </a>
  ),
}));

jest.mock('@components1/common/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }) => <span>{cloud_provider}</span>,
}));

import EventResolutions from '@components1/troubleshoot/EventResolutions';

const apiRecommendations = require('@api1/recommendation').default;
const apiHome = require('@api1/home').default;
const { applyFiltersOnRouter } = require('@lib/router');

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod', cloud_provider: 'aws' },
  { id: 'acc-2', account_name: 'GCP Dev', cloud_provider: 'gcp' },
];

const sampleResolutions = [
  {
    id: 'r-1',
    type: 'PullRequest',
    type_reference_id: 'https://github.com/foo/bar/pull/123',
    status: 'Success',
    resolver_type: 'auto_pilot',
    resolver_user: { display_name: 'AutoBot' },
    updated_at: '2026-05-15T10:00:00Z',
    event: { subject_name: 'svc-a', subject_namespace: 'default', cloud_account_id: 'acc-1', priority: 'high' },
    data: {
      data: {
        web: { cpu: { oldRequest: 100, request: 200, oldLimit: 200, limit: 400 } },
      },
    },
  },
  {
    id: 'r-2',
    type: 'DeploymentChange',
    type_reference_id: 'deployment-456',
    status: 'Failed',
    status_message: 'kubectl apply failed',
    resolver_type: 'manual',
    resolver_user: null,
    updated_at: '2026-05-15T11:00:00Z',
    event: { subject_name: 'svc-b', priority: 'low' },
    data: { change_type: 'restart_pod', data: { restart: true, container_name: 'app' } },
  },
  {
    id: 'r-3',
    type: 'Ticket',
    type_reference_id: 'ticket-789',
    status: 'InProgress',
    resolver_type: 'system',
    updated_at: '2026-05-15T12:00:00Z',
    event: { subject_name: 'svc-c' },
    data: { data: { raisePR: true }, provider: 'github' },
  },
];

const mockResponse = (items = sampleResolutions) => ({
  data: {
    data: {
      event_resolution: items,
      event_resolution_aggregate: { aggregate: { count: items.length } },
    },
  },
});

describe('EventResolutions (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    apiHome.getCloudAccounts.mockResolvedValue(sampleAccounts);
    apiRecommendations.listAllEventResolutions.mockResolvedValue(mockResponse());
  });

  it('fetches accounts and resolutions on mount with default filters', async () => {
    render(<EventResolutions />);

    await waitFor(() => {
      expect(apiHome.getCloudAccounts).toHaveBeenCalledTimes(1);
      expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled();
    });
    const call = apiRecommendations.listAllEventResolutions.mock.calls[0][0];
    expect(call).toMatchObject({
      limit: 10,
      offset: 0,
      accountId: undefined,
      status: undefined,
      type: undefined,
      resolverType: undefined,
    });
  });

  it('renders resolution rows with subject + namespace + account name', async () => {
    render(<EventResolutions />);

    await waitFor(() => expect(screen.getByText('svc-a')).toBeInTheDocument());
    expect(screen.getByText('svc-b')).toBeInTheDocument();
    expect(screen.getByText('svc-c')).toBeInTheDocument();
    expect(screen.getByText('ns: default')).toBeInTheDocument();
    expect(screen.getByText('acc: AWS Prod')).toBeInTheDocument();
  });

  it('renders status badges with correct variants per status', async () => {
    render(<EventResolutions />);

    await waitFor(() => {
      expect(screen.getByTestId('label-green')).toHaveTextContent('Success');
    });
    expect(screen.getByTestId('label-red')).toHaveTextContent('Failed');
    expect(screen.getByTestId('label-yellow')).toHaveTextContent('InProgress');
    expect(screen.getByText('kubectl apply failed')).toBeInTheDocument();
  });

  it('renders type as link when type_reference_id is URL', async () => {
    render(<EventResolutions />);

    await waitFor(() => expect(screen.getAllByTestId('custom-link').length).toBeGreaterThan(0));
    const links = screen.getAllByTestId('custom-link');
    expect(links.some((l) => l.getAttribute('href') === 'https://github.com/foo/bar/pull/123')).toBe(true);
  });

  it('refetches with status filter on change', async () => {
    render(<EventResolutions />);
    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    apiRecommendations.listAllEventResolutions.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Success' } });

    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].status).toBe('Success');
  });

  it('refetches with type filter on change', async () => {
    render(<EventResolutions />);
    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    apiRecommendations.listAllEventResolutions.mockClear();

    fireEvent.change(screen.getByTestId('filter-Type'), { target: { value: 'PullRequest' } });

    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].type).toBe('PullRequest');
  });

  it('refetches with resolver filter on change', async () => {
    render(<EventResolutions />);
    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    apiRecommendations.listAllEventResolutions.mockClear();

    fireEvent.change(screen.getByTestId('filter-Resolver'), { target: { value: 'AutoPilot' } });

    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].resolverType).toBe('AutoPilot');
  });

  it('updates router and refetches when account multi-dropdown selected', async () => {
    render(<EventResolutions />);
    await waitFor(() => expect(screen.getByTestId('multi-Account')).toBeInTheDocument());
    apiRecommendations.listAllEventResolutions.mockClear();

    fireEvent.click(screen.getByTestId('multi-Account'));

    await waitFor(() => {
      expect(applyFiltersOnRouter).toHaveBeenCalledWith(expect.anything(), { accountId: 'acc-1' });
      expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled();
    });
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].accountId).toEqual(['acc-1']);
  });

  it('initializes account filter from router query', async () => {
    mockRouterQuery = { accountId: 'acc-1,acc-2' };
    render(<EventResolutions />);

    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].accountId).toEqual(['acc-1', 'acc-2']);
  });

  it('paginates and updates offset on next page', async () => {
    render(<EventResolutions />);
    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    apiRecommendations.listAllEventResolutions.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].offset).toBe(10);
  });

  it('caps limit at 100 when rowsPerPage exceeds 100', async () => {
    const apiUser = require('@api1/user').default;
    apiUser.getUserPreferencesTablePageSize.mockReturnValue(500);

    render(<EventResolutions />);

    await waitFor(() => expect(apiRecommendations.listAllEventResolutions).toHaveBeenCalled());
    expect(apiRecommendations.listAllEventResolutions.mock.calls[0][0].limit).toBe(100);
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    apiRecommendations.listAllEventResolutions.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<EventResolutions />);
    expect(screen.getByTestId('loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty list gracefully', async () => {
    apiRecommendations.listAllEventResolutions.mockResolvedValue(mockResponse([]));

    render(<EventResolutions />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
