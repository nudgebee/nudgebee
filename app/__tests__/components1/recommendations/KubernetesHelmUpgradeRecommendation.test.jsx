import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getK8sRecommendation: jest.fn(),
    createRecommendationJob: jest.fn(),
  },
  RECOMMENDATION_STATUS: [
    { label: 'Open', value: 'Open' },
    { label: 'In Progress', value: 'InProgress' },
    { label: 'Closed', value: 'Closed' },
  ],
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('@lib/formatter', () => ({
  titleCase: (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : ''),
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@assets/sidebar-icon/tickets-icon.svg', () => '/tickets.svg', { virtual: true });

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{String(value || '—')}</span>,
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text }) => <span data-testid='label'>{text}</span>,
}));

jest.mock('@components1/common/CustomLink', () => ({
  __esModule: true,
  default: ({ href, children }) => (
    <a data-testid='custom-link' href={href}>
      {children}
    </a>
  ),
}));

jest.mock('@components1/common/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }) => (
    <div data-testid='three-dots'>
      {(menuItems || []).map((mi) => (
        <button key={mi.id} data-testid={`menu-${mi.label}-${data.id}`} onClick={() => onMenuClick(mi, data)} disabled={mi.disabled}>
          {mi.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ id, text, onClick }) => (
    <button data-testid={`btn-${id || text}`} onClick={onClick}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/k8s/common/ClusterNameWithRegion', () => ({
  __esModule: true,
  default: ({ name, region }) => (
    <span data-testid='cluster-name'>
      {name}
      {region}
    </span>
  ),
}));

jest.mock('@components1/k8s/common/RecommendationJobDetails', () => ({
  __esModule: true,
  default: ({ jobName }) => <div data-testid='job-details'>{jobName}</div>,
}));

jest.mock('@components1/tickets/TicketCreatePopupForm', () => ({
  __esModule: true,
  default: ({ open, ticketData, onSuccess, onFailure }) =>
    open ? (
      <div data-testid='ticket-modal'>
        <div data-testid='ticket-subject'>{ticketData.subject}</div>
        <div data-testid='ticket-desc'>{ticketData.description}</div>
        <button data-testid='ticket-success' onClick={onSuccess}>
          Success
        </button>
        <button data-testid='ticket-failure' onClick={() => onFailure('Bad')}>
          Failure
        </button>
      </div>
    ) : null,
}));

jest.mock('@components1/optimise/SummaryWidget', () => ({
  __esModule: true,
  default: ({ title, value }) => <div data-testid={`summary-${title}`}>{value}</div>,
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, heading, filterOptions = [], extraOptions = [], onRefresh }) => (
    <div data-testid='box-layout'>
      <h2 data-testid='box-heading'>{heading}</h2>
      <div data-testid='extras'>{extraOptions}</div>
      {filterOptions.map((f, i) => (
        <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={(e) => f.onSelect(e)}>
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
      ))}
      {onRefresh?.enabled && (
        <button data-testid='refresh-btn' onClick={onRefresh.onClick}>
          Refresh
        </button>
      )}
      {children}
    </div>
  ),
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

import KubernetesHelmUpgradeRecommendation from '@components1/recommendations/KubernetesHelmUpgradeRecommendation';

const recommendationApi = require('@api1/recommendation').default;
const { snackbar } = require('@components1/common/snackbarService');

const sampleRecs = [
  {
    id: 'r-1',
    severity: 'high',
    updated_at: '2026-05-15T10:00:00Z',
    recommendation: {
      chartName: 'nginx',
      release: 'web-release',
      namespace: 'default',
      outdated: true,
      deprecated: false,
      overridden: true,
      Installed: { version: '1.0.0', date: '2024-01-01T00:00:00Z' },
      Latest: { version: '1.5.0', date: '2026-04-01T00:00:00Z' },
    },
    ticket: undefined,
  },
  {
    id: 'r-2',
    severity: 'low',
    updated_at: '2026-05-15T11:00:00Z',
    recommendation: {
      chartName: 'redis',
      release: 'cache',
      namespace: 'cache-ns',
      outdated: false,
      deprecated: true,
      overridden: false,
      Installed: { version: '6.0.0', date: '2024-01-01T00:00:00Z' },
      Latest: { version: '6.0.0', date: '0001-01-01T00:00:00Z' },
    },
    ticket: { url: 'https://t/1', ticket_id: 'T-1' },
  },
];

const mockResponse = (items = sampleRecs) => ({
  data: { recommendation: items, recommendation_aggregate: { aggregate: { count: items.length } } },
});

describe('KubernetesHelmUpgradeRecommendation (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse());
    recommendationApi.createRecommendationJob.mockResolvedValue({});
  });

  it('does not fetch when accountId missing', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId={undefined} />);
    await act(async () => {});
    expect(recommendationApi.getK8sRecommendation).not.toHaveBeenCalled();
  });

  it('fetches recommendations on mount with default Open status', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    const call = recommendationApi.getK8sRecommendation.mock.calls[0][0];
    expect(call).toMatchObject({
      accountId: 'acc-1',
      ruleName: 'helm_chart_upgrade',
      category: 'InfraUpgrade',
      status: ['Open'],
      limit: 10,
      offset: 0,
      fetchTicket: true,
    });
  });

  it('renders summary widget with total count', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('summary-Total Recommendations')).toBeInTheDocument());
    expect(screen.getByTestId('summary-Total Recommendations')).toHaveTextContent('2');
  });

  it('renders rows with chart names + release + versions', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('nginx')).toBeInTheDocument());
    expect(screen.getByText('redis')).toBeInTheDocument();
    expect(screen.getByText('web-release')).toBeInTheDocument();
    expect(screen.getByText('1.0.0')).toBeInTheDocument();
    expect(screen.getByText('1.5.0')).toBeInTheDocument();
  });

  it('shows truthy issue flags (Outdated, Overridden) as text', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('Outdated, Overridden')).toBeInTheDocument());
    expect(screen.getByText('Deprecated')).toBeInTheDocument();
  });

  it('renders existing ticket link when rec has ticket', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getAllByTestId('custom-link').length).toBeGreaterThan(0));
    const links = screen.getAllByTestId('custom-link');
    expect(links.some((l) => l.getAttribute('href') === 'https://t/1')).toBe(true);
  });

  it('disables Create Ticket menu when ticket already exists', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Create Ticket-r-1')).not.toBeDisabled();
    expect(screen.getByTestId('menu-Create Ticket-r-2')).toBeDisabled();
  });

  it('opens ticket modal on Create Ticket menu click with description', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    expect(screen.getByTestId('ticket-modal')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-subject')).toHaveTextContent('K8s Cluster Version Upgrade Issue');
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Chart Name\*\*: nginx/);
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Latest Version\*\*: 1\.5\.0/);
  });

  it('refetches list after ticket success', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('ticket-success'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
  });

  it('snackbar error on ticket failure', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    fireEvent.click(screen.getByTestId('ticket-failure'));

    expect(snackbar.error).toHaveBeenCalledWith(expect.stringContaining('Bad'));
  });

  it('refetches with status filter on change', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Closed' } });

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].status).toEqual(['Closed']);
  });

  it('paginates and updates offset on next page', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].offset).toBe(10);
  });

  it('triggers refresh on refresh button click', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('refresh-btn'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
  });

  it('triggers Generate scan job on extra button click', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('btn-triggerRecommendation')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('btn-triggerRecommendation'));

    await waitFor(() => expect(recommendationApi.createRecommendationJob).toHaveBeenCalledWith('acc-1', 'helm_chart_upgrade'));
    expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('Scan Triggered'));
    alertSpy.mockRestore();
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    recommendationApi.getK8sRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty list gracefully', async () => {
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse([]));

    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });

  it('uses heading prop when provided', async () => {
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' heading='Custom' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Custom'));
  });

  it('renders empty heading when heading prop not provided (destructure default kicks in)', async () => {
    // Note: destructure default `heading = ''` makes the `heading === undefined` fallback unreachable
    render(<KubernetesHelmUpgradeRecommendation accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toBeEmptyDOMElement());
  });
});
