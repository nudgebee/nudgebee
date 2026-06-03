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
    pathname: '/triage',
    asPath: '/triage',
    route: '/triage',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('@lib/router', () => ({
  applyFiltersOnRouter: jest.fn(),
}));

jest.mock('@api1/triage', () => ({
  __esModule: true,
  default: {
    listThresholdSuggestions: jest.fn(),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

const mockUseKubernetesEventFilters = jest.fn();
jest.mock('@hooks/useKubernetesEventFilters', () => ({
  __esModule: true,
  default: (...args) => mockUseKubernetesEventFilters(...args),
}));

jest.mock('@assets', () => ({
  DataNotAvailable: '/no-data.svg',
}));

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { primary: '#000', secondary: '#666' },
    background: { white: '#fff' },
    border: { secondary: '#ddd' },
  },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }) => <span data-testid={`cloud-${cloud_provider}`}>cloud</span>,
}));

jest.mock('@components1/common/EmptyData', () => ({
  __esModule: true,
  default: ({ heading, id }) => <div data-testid={id || 'empty-data'}>{heading}</div>,
}));

jest.mock('@components1/common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, filterOptions = [] }) => (
    <div data-testid='box-layout'>
      <div data-testid='filters'>
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
                {f.label}: {(f.value || []).length}
              </button>
            );
          }
          return null;
        })}
      </div>
      {children}
    </div>
  ),
}));

jest.mock('@components1/k8s/common/KubernetesTable2', () => ({
  __esModule: true,
  default: ({ id, data, headers, totalRows, loading, pageNumber, onPageChange }) => (
    <div data-testid='k8s-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{headers.map((h) => h.name).join('|')}</div>
      {(data || []).map((row, i) => (
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
      <button data-testid='change-size' onClick={() => onPageChange(2, 25)}>
        Resize
      </button>
    </div>
  ),
}));

jest.mock('./../../../src/components1/triage/ThresholdEvidence', () => ({
  __esModule: true,
  default: () => <div>evidence</div>,
  RecentEventsTab: () => <div>recent</div>,
}));

import ThresholdSuggestionsManager from '@components1/triage/ThresholdSuggestionsManager';

const apiTriage = require('@api1/triage').default;
const { applyFiltersOnRouter } = require('@lib/router');

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod', label: 'AWS Prod', cloud_provider: 'aws' },
  { id: 'acc-2', account_name: 'GCP Dev', label: 'GCP Dev', cloud_provider: 'gcp' },
];

const sampleSuggestions = [
  {
    alert_name: 'CPU High',
    source: 'AWS_CloudWatch_Alarm',
    cloud_account_id: 'acc-1',
    confidence: 'high',
    current_threshold: 80,
    suggested_threshold: 90,
    operator: 'GreaterThanThreshold',
    estimated_reduction: 75,
    recommendation_type: 'tune_threshold',
    metric_stats: { recommendation_type: 'tune_threshold', risk_level: 'safe' },
  },
  {
    alert_name: 'Pod Restart',
    source: 'prometheus',
    cloud_account_id: 'acc-2',
    confidence: 'low',
    current_threshold: 5,
    suggested_threshold: 5,
    operator: 'gt',
    estimated_reduction: 100,
    recommendation_type: 'disable',
    metric_stats: { recommendation_type: 'disable', risk_level: 'review' },
  },
];

describe('ThresholdSuggestionsManager (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    mockUseKubernetesEventFilters.mockReturnValue({ accounts: sampleAccounts });
    apiTriage.listThresholdSuggestions.mockResolvedValue({
      suggestions: sampleSuggestions,
      total: sampleSuggestions.length,
    });
  });

  it('fetches suggestions on mount with default filters', async () => {
    render(<ThresholdSuggestionsManager />);

    await waitFor(() => {
      expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled();
    });
    const call = apiTriage.listThresholdSuggestions.mock.calls[0][0];
    expect(call).toMatchObject({
      cloud_account_id: undefined,
      cloud_account_ids: undefined,
      source: undefined,
      confidence: undefined,
      limit: 10,
      offset: 0,
    });
  });

  it('renders Account column header in multi-account view', async () => {
    render(<ThresholdSuggestionsManager />);

    await waitFor(() => {
      expect(screen.getByTestId('headers')).toHaveTextContent('Account|Alert|Recommendation|Threshold|Confidence|Noise Reduction');
    });
  });

  it('hides Account column header when accountId prop is provided', async () => {
    render(<ThresholdSuggestionsManager accountId='acc-1' />);

    await waitFor(() => {
      expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled();
    });
    const call = apiTriage.listThresholdSuggestions.mock.calls[0][0];
    expect(call.cloud_account_id).toBe('acc-1');
    expect(screen.getByTestId('headers')).toHaveTextContent('Alert|Recommendation|Threshold|Confidence|Noise Reduction');
    expect(screen.getByTestId('headers')).not.toHaveTextContent('Account|');
  });

  it('renders suggestion rows with formatted threshold and noise reduction', async () => {
    render(<ThresholdSuggestionsManager />);

    await waitFor(() => {
      expect(screen.getByText('CPU High')).toBeInTheDocument();
    });
    expect(screen.getByText('Pod Restart')).toBeInTheDocument();
    expect(screen.getByText('75%')).toBeInTheDocument();
    expect(screen.getByText('Suppresses all')).toBeInTheDocument();
    expect(screen.getAllByText('AWS CloudWatch').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Prometheus').length).toBeGreaterThan(0);
  });

  it('refetches with source filter on dropdown change', async () => {
    render(<ThresholdSuggestionsManager />);
    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    apiTriage.listThresholdSuggestions.mockClear();

    fireEvent.change(screen.getByTestId('filter-Source'), {
      target: { value: 'prometheus' },
    });

    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    expect(apiTriage.listThresholdSuggestions.mock.calls[0][0].source).toBe('prometheus');
  });

  it('refetches with confidence filter on dropdown change', async () => {
    render(<ThresholdSuggestionsManager />);
    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    apiTriage.listThresholdSuggestions.mockClear();

    fireEvent.change(screen.getByTestId('filter-Confidence'), {
      target: { value: 'medium' },
    });

    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    expect(apiTriage.listThresholdSuggestions.mock.calls[0][0].confidence).toBe('medium');
  });

  it('updates router and fetches with cloud_account_ids when account filter changes', async () => {
    render(<ThresholdSuggestionsManager />);
    await waitFor(() => expect(screen.getByTestId('multi-Account')).toBeInTheDocument());
    apiTriage.listThresholdSuggestions.mockClear();

    fireEvent.click(screen.getByTestId('multi-Account'));

    await waitFor(() => {
      expect(applyFiltersOnRouter).toHaveBeenCalledWith(expect.anything(), { accountId: 'acc-1' });
    });
    await waitFor(() => {
      expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled();
      expect(apiTriage.listThresholdSuggestions.mock.calls[0][0].cloud_account_ids).toEqual(['acc-1']);
    });
  });

  it('initializes account filter from router query', async () => {
    mockRouterQuery = { accountId: 'acc-1,acc-2' };
    render(<ThresholdSuggestionsManager />);

    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    const call = apiTriage.listThresholdSuggestions.mock.calls[0][0];
    expect(call.cloud_account_ids).toEqual(['acc-1', 'acc-2']);
  });

  it('paginates and adjusts offset when next page clicked', async () => {
    render(<ThresholdSuggestionsManager />);
    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    apiTriage.listThresholdSuggestions.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    expect(apiTriage.listThresholdSuggestions.mock.calls[0][0].offset).toBe(10);
  });

  it('resets to page 0 when rowsPerPage changes', async () => {
    render(<ThresholdSuggestionsManager />);
    await waitFor(() => expect(apiTriage.listThresholdSuggestions).toHaveBeenCalled());
    apiTriage.listThresholdSuggestions.mockClear();

    fireEvent.click(screen.getByTestId('change-size'));

    await waitFor(() => {
      const last = apiTriage.listThresholdSuggestions.mock.calls.at(-1)[0];
      expect(last.limit).toBe(25);
      expect(last.offset).toBe(0);
    });
  });

  it('shows empty state when no suggestions', async () => {
    apiTriage.listThresholdSuggestions.mockResolvedValue({ suggestions: [], total: 0 });

    render(<ThresholdSuggestionsManager />);

    await waitFor(() => {
      expect(screen.getByTestId('threshold-suggestions-empty')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('k8s-table')).not.toBeInTheDocument();
  });

  it('handles API rejection gracefully (no crash, loading clears)', async () => {
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    apiTriage.listThresholdSuggestions.mockRejectedValue(new Error('network'));

    render(<ThresholdSuggestionsManager />);

    await waitFor(() => {
      expect(screen.queryByTestId('loading')).not.toBeInTheDocument();
    });
    expect(screen.getByTestId('threshold-suggestions-empty')).toBeInTheDocument();
    errorSpy.mockRestore();
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    apiTriage.listThresholdSuggestions.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<ThresholdSuggestionsManager />);
    expect(screen.getByTestId('loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn({ suggestions: sampleSuggestions, total: 2 });
    });

    await waitFor(() => {
      expect(screen.queryByTestId('loading')).not.toBeInTheDocument();
    });
  });
});
