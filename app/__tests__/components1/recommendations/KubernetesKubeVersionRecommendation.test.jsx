import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getK8sRecommendation: jest.fn(),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/optimise/SummaryWidget', () => ({
  __esModule: true,
  default: ({ title, value }) => <div data-testid={`summary-${title}`}>{value}</div>,
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children }) => <div data-testid='box-layout'>{children}</div>,
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ id, tableData, totalRows, loading, pageNumber, onPageChange, headers }) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{headers.map((h) => h.name).join('|')}</div>
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
      <button data-testid='change-size' onClick={() => onPageChange(2, 25)}>
        Resize
      </button>
    </div>
  ),
}));

import KubernetesKubeVersionRecommendation from '@components1/recommendations/KubernetesKubeVersionRecommendation';

const recommendationApi = require('@api1/recommendation').default;

const sampleRecs = [
  {
    id: 'r-1',
    recommendation: {
      'kube-proxy': 'v1.24.0',
      target_k8s_version: 'v1.28.0',
      message: 'Upgrade needed for security patches). API removed in v1.28).',
    },
  },
  {
    id: 'r-2',
    recommendation: {
      'kube-proxy': 'v1.25.0',
      target_k8s_version: 'v1.29.0',
      message: 'Multiple deprecations',
    },
  },
];

const mockResponse = (items = sampleRecs, count) => ({
  data: { recommendation: items, recommendation_aggregate: { aggregate: { count: count ?? items.length } } },
});

describe('KubernetesKubeVersionRecommendation (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse());
  });

  it('does not fetch when accountId is missing', async () => {
    render(<KubernetesKubeVersionRecommendation accountId={undefined} />);
    await act(async () => {});
    expect(recommendationApi.getK8sRecommendation).not.toHaveBeenCalled();
  });

  it('fetches list and summary on mount', async () => {
    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);

    await waitFor(() => expect(recommendationApi.getK8sRecommendation.mock.calls.length).toBeGreaterThanOrEqual(2));
    // First call is summary (limit: 1)
    const summaryCall = recommendationApi.getK8sRecommendation.mock.calls.find((c) => c[0].limit === 1);
    expect(summaryCall[0]).toMatchObject({
      accountId: 'acc-1',
      ruleName: 'kube_proxy_version',
      category: 'InfraUpgrade',
      limit: 1,
      offset: 0,
    });
    // List call has status + fetchTicket
    const listCall = recommendationApi.getK8sRecommendation.mock.calls.find((c) => c[0].fetchTicket === true);
    expect(listCall[0]).toMatchObject({
      accountId: 'acc-1',
      ruleName: 'kube_proxy_version',
      category: 'InfraUpgrade',
      status: ['Open'],
      limit: 10,
      offset: 0,
      fetchTicket: true,
    });
  });

  it('renders SummaryWidget with total count from summary fetch', async () => {
    recommendationApi.getK8sRecommendation.mockImplementation((params) => {
      if (params.limit === 1) {
        return Promise.resolve(mockResponse([], 42));
      }
      return Promise.resolve(mockResponse());
    });

    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('summary-Total Recommendations')).toHaveTextContent('42'));
  });

  it('renders table headers Current Version | Min. K8s Version | Message', async () => {
    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Current Version|Min. K8s Version|Message'));
  });

  it('renders rows with current + target versions', async () => {
    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('v1.24.0')).toBeInTheDocument());
    expect(screen.getByText('v1.28.0')).toBeInTheDocument();
    expect(screen.getByText('v1.25.0')).toBeInTheDocument();
    expect(screen.getByText('v1.29.0')).toBeInTheDocument();
  });

  it('splits message on right-paren-dot separator and renders bullet items', async () => {
    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('• Upgrade needed for security patches')).toBeInTheDocument());
    expect(screen.getByText('• API removed in v1.28')).toBeInTheDocument();
  });

  it('renders dash for missing kube-proxy or target version', async () => {
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse([{ id: 'r-x', recommendation: { message: 'msg).' } }]));

    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getAllByText('-').length).toBeGreaterThanOrEqual(2));
  });

  it('paginates and updates offset on next page', async () => {
    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    const listCall = recommendationApi.getK8sRecommendation.mock.calls.find((c) => c[0].fetchTicket === true);
    expect(listCall[0].offset).toBe(10);
  });

  it('updates rowsPerPage when changePage receives different limit', async () => {
    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('change-size'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    const listCall = recommendationApi.getK8sRecommendation.mock.calls.find((c) => c[0].fetchTicket === true);
    expect(listCall[0].limit).toBe(25);
  });

  it('shows loading state during fetch and clears after', async () => {
    let resolveFn;
    recommendationApi.getK8sRecommendation.mockImplementation((params) => {
      if (params.limit === 1) return Promise.resolve(mockResponse([], 0));
      return new Promise((resolve) => {
        resolveFn = resolve;
      });
    });

    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty recommendations gracefully', async () => {
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse([], 0));

    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });

  it('handles summary fetch rejection without crashing', async () => {
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    recommendationApi.getK8sRecommendation.mockImplementation((params) => {
      if (params.limit === 1) return Promise.reject(new Error('summary boom'));
      return Promise.resolve(mockResponse());
    });

    render(<KubernetesKubeVersionRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('summary-Total Recommendations')).toHaveTextContent('0'));
    errorSpy.mockRestore();
  });
});
