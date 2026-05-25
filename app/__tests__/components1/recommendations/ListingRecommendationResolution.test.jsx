import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getDistinctResolverTypes: jest.fn(),
    getRecommendationResolution: jest.fn(),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

const mockUseCurrencySymbol = jest.fn();
jest.mock('@hooks/useCurrencySymbol', () => ({
  __esModule: true,
  default: (...args) => mockUseCurrencySymbol(...args),
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000', secondary: '#666' }, background: { white: '#fff' } },
}));

jest.mock('src/utils/common', () => ({
  containsLink: (val) => typeof val === 'string' && /^https?:\/\//.test(val),
  snakeToTitleCase: (s) => s,
}));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children }) => (
    <a data-testid='ext-link' href={href}>
      {children}
    </a>
  ),
}));

jest.mock('@components1/common', () => ({
  BoxLayout2: ({ children, filterOptions = [] }) => (
    <div data-testid='box-layout'>
      {filterOptions.map((f, i) =>
        f.type === 'dropdown' ? (
          <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={f.onSelect}>
            <option value=''>--</option>
            {(f.options || []).map((opt, idx) => {
              const v = typeof opt === 'string' ? opt : opt.value;
              const l = typeof opt === 'string' ? opt : opt.label;
              return (
                <option key={(v || '_') + '-' + idx} value={v}>
                  {l}
                </option>
              );
            })}
          </select>
        ) : null
      )}
      {children}
    </div>
  ),
}));

jest.mock('@components1/common/format/Currency', () => ({
  __esModule: true,
  default: ({ value, prefix }) => (
    <span data-testid='currency'>
      {prefix}
      {value}
    </span>
  ),
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{value || '—'}</span>,
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text }) => <span data-testid='label'>{text}</span>,
}));

jest.mock('@components1/k8s/common/KubernetesTable2', () => ({
  __esModule: true,
  default: ({ id, data, totalRows, loading, pageNumber, onPageChange }) => (
    <div data-testid='k8s-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      {(data || []).map((row, i) => (
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

import ListingRecommendationResolution from '@components1/recommendations/ListingRecommendationResolution';

const apiRecommendations = require('@api1/recommendation').default;

const sampleResolutions = [
  {
    type: 'KubernetesRightsize',
    type_reference_id: 'https://github.com/foo/bar/pull/1',
    status: 'Success',
    resolver_type: 'AutoPilot',
    resolver_display_name: 'AutoBot',
    updated_at: '2026-05-15T10:00:00Z',
    status_message: 'Done',
    recommendation: {
      rule_name: 'rightsize_cpu',
      severity: 'high',
      estimated_savings: 12.5,
      recommendation: { metadata: { name: 'web', namespace: 'prod' } },
      cloud_resourse: { meta: { namespace: 'prod', controller: 'web' } },
    },
    data: { provider_config: { name: 'GH' } },
  },
  {
    type: 'PvcResize',
    type_reference_id: 'pvc-456',
    status: 'InProgress',
    resolver_type: 'Manual',
    updated_at: '2026-05-15T11:00:00Z',
    status_message: 'Pending',
    recommendation: {
      rule_name: 'pvc_grow',
      severity: 'medium',
      estimated_savings: 0,
      recommendation: { spec: { claimRef: { name: 'data', namespace: 'apps' } } },
      cloud_resourse: { meta: { config: { labels: { 'app.kubernetes.io/name': 'api' } } } },
    },
    data: null,
  },
];

const mockResponse = (items = sampleResolutions, count) => ({
  data: {
    data: {
      recommendation_resolution: items,
      recommendation_resolution_aggregate: { aggregate: { count: count ?? items.length } },
    },
  },
});

const mockDistinctResponse = (types) => ({
  data: { data: { recommendation_resolution: types.map((t) => ({ type: t, resolver_type: t })) } },
});

describe('ListingRecommendationResolution (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockUseCurrencySymbol.mockReturnValue('$');
    apiRecommendations.getDistinctResolverTypes.mockImplementation((field) => {
      if (field === 'type') return Promise.resolve(mockDistinctResponse(['KubernetesRightsize', 'PvcResize']));
      return Promise.resolve(mockDistinctResponse(['AutoPilot', 'Manual']));
    });
    apiRecommendations.getRecommendationResolution.mockResolvedValue(mockResponse());
  });

  it('fetches distinct type + resolver filters on mount', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => {
      expect(apiRecommendations.getDistinctResolverTypes).toHaveBeenCalledWith('type');
      expect(apiRecommendations.getDistinctResolverTypes).toHaveBeenCalledWith('resolver_type');
    });
  });

  it('fetches resolution list on mount with default filters (status=InProgress)', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    const call = apiRecommendations.getRecommendationResolution.mock.calls[0][0];
    expect(call).toMatchObject({
      limit: 10,
      offset: 0,
      accountId: 'acc-1',
      status: 'InProgress',
      type: '',
      resolverType: '',
    });
  });

  it('does not fetch list until currencySymbol resolves', async () => {
    mockUseCurrencySymbol.mockReturnValue(undefined);
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await act(async () => {});
    expect(apiRecommendations.getRecommendationResolution).not.toHaveBeenCalled();
  });

  it('renders resolution rows with status labels', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => expect(screen.getAllByTestId('label').length).toBeGreaterThan(0));
    const labels = screen.getAllByTestId('label').map((el) => el.textContent);
    expect(labels).toEqual(expect.arrayContaining(['Success', 'In Progress']));
  });

  it('renders external Link for type when reference is URL', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => expect(screen.getAllByTestId('ext-link').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('ext-link')[0]).toHaveAttribute('href', 'https://github.com/foo/bar/pull/1');
  });

  it('renders Currency with currencySymbol prefix', async () => {
    mockUseCurrencySymbol.mockReturnValue('€');
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => expect(screen.getAllByTestId('currency').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('currency')[0].textContent).toMatch(/^€/);
  });

  it('populates status dropdown with In Progress option label', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('filter-Status')).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'In Progress' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Success' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Failed' })).toBeInTheDocument();
  });

  it('refetches with status when status filter changes', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);
    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    apiRecommendations.getRecommendationResolution.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Success' } });

    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    expect(apiRecommendations.getRecommendationResolution.mock.calls[0][0].status).toBe('Success');
  });

  it('refetches with type when recommendation filter changes', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);
    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    apiRecommendations.getRecommendationResolution.mockClear();

    fireEvent.change(screen.getByTestId('filter-Recommendation'), {
      target: { value: 'KubernetesRightsize' },
    });

    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    expect(apiRecommendations.getRecommendationResolution.mock.calls[0][0].type).toBe('KubernetesRightsize');
  });

  it('refetches with resolverType when resolver filter changes', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);
    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    apiRecommendations.getRecommendationResolution.mockClear();

    fireEvent.change(screen.getByTestId('filter-Resolver'), { target: { value: 'AutoPilot' } });

    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    expect(apiRecommendations.getRecommendationResolution.mock.calls[0][0].resolverType).toBe('AutoPilot');
  });

  it('paginates and updates offset on next page', async () => {
    render(<ListingRecommendationResolution accountId='acc-1' />);
    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    apiRecommendations.getRecommendationResolution.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiRecommendations.getRecommendationResolution).toHaveBeenCalled());
    expect(apiRecommendations.getRecommendationResolution.mock.calls[0][0].offset).toBe(10);
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    apiRecommendations.getRecommendationResolution.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<ListingRecommendationResolution accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty list gracefully', async () => {
    apiRecommendations.getRecommendationResolution.mockResolvedValue(mockResponse([], 0));

    render(<ListingRecommendationResolution accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
