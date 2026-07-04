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
  toSeverityLevel: (s) => String(s || '').toLowerCase(),
}));

jest.mock('@utils/colors');

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{value}</span>,
}));

// DS Label takes `tone` (success/critical/warning/neutral) — map back to the
// legacy variant color for testid stability so existing label-{color} queries pass.
jest.mock('@ui/Label', () => {
  const TONE_TO_COLOR = { success: 'green', critical: 'red', warning: 'yellow', neutral: 'grey', info: 'blue' };
  return {
    Label: ({ text, children, tone, variant }) => {
      const color = variant || TONE_TO_COLOR[tone] || 'plain';
      return <span data-testid={`label-${color}`}>{children || text}</span>;
    },
  };
});

jest.mock('@ui/SeverityIcon', () => {
  const SeverityIcon = ({ level }) => <span data-testid={`severity-${level || 'none'}`}>sev</span>;
  return { __esModule: true, default: SeverityIcon, SeverityIcon };
});

jest.mock('@ui/Link', () => ({
  Link: ({ href, children }) => (
    <a data-testid='custom-link' href={href}>
      {children}
    </a>
  ),
}));

// Source passes `before` and `after` as objects like `{ value: 100 }`.
// Render the inner `.value` to avoid React "object as child" errors.
jest.mock('@ui/Comparison', () => ({
  Comparison: ({ before, after, label }) => (
    <div data-testid='comparison'>
      <span>{label}: </span>
      <span>{before?.value ?? String(before)}</span>→<span>{after?.value ?? String(after)}</span>
    </div>
  ),
  ComparisonGroup: ({ children }) => <div data-testid='comparison-group'>{children}</div>,
}));

jest.mock('@ui/Button', () => ({
  Button: React.forwardRef(({ children, onClick, id, disabled, ...rest }, ref) => (
    <button ref={ref} data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled} {...rest}>
      {children}
    </button>
  )),
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@shared/icons/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }) => <span>{cloud_provider}</span>,
}));

// Source line 17: `import ListingLayout from '@ui/ListingLayout';` — uses default
// import, while other files use named. Expose BOTH default and named to keep
// this mock reusable across files.
jest.mock('@ui/ListingLayout', () => {
  const ListingLayout = ({ children, id }) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, default: ListingLayout, ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect, multiple }) => {
    if (multiple) {
      return (
        <button data-testid={`multi-${label}`} onClick={() => onSelect?.(null, (options || []).slice(0, 1))}>
          {label}
        </button>
      );
    }
    const currentValue = typeof value === 'object' && value !== null ? value.value : value;
    return (
      <select
        data-testid={`filter-${label}`}
        value={currentValue || ''}
        onChange={(e) => onSelect?.({ target: { value: e.target.value } }, { value: e.target.value, label: e.target.value })}
      >
        <option value=''>--</option>
        {(options || []).map((opt, idx) => {
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

import EventResolutions from '@components/troubleshoot/EventResolutions';

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

    await waitFor(() => expect(screen.getByTestId('label-green')).toHaveTextContent('Success'));
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
