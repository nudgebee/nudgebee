import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getK8sRecommendation: jest.fn(),
    createRecommendationJob: jest.fn(),
  },
  RECOMMENDATION_STATUS: [
    { label: 'Open', value: 'Open' },
    { label: 'Closed', value: 'Closed' },
  ],
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@assets/sidebar-icon/tickets-icon.svg', () => '/tickets.svg', { virtual: true });

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { primary: '#000', secondary: '#666', tertiary: '#999', infoDark: '#005' },
    background: { white: '#fff', shimmerHighlight: '#f9f9f9', infoGraphic: '#eef' },
    border: { secondary: '#ddd', secondaryLightest: '#eee', warning: '#fa0' },
    error: '#f00',
    info: '#00f',
  },
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
  default: ({ text }) => <span data-testid={`label-${text}`}>{text}</span>,
}));

jest.mock('@components1/common/CustomTicketLink', () => ({
  __esModule: true,
  default: ({ ticketID }) => <a data-testid='ticket-link'>{ticketID}</a>,
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
  default: ({ id, text, onClick, disabled }) => (
    <button data-testid={`btn-${id || text}`} onClick={onClick} disabled={disabled}>
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
  default: ({ children, heading, filterOptions = [], extraOptions = [] }) => (
    <div data-testid='box-layout'>
      <h2 data-testid='box-heading'>{heading}</h2>
      <div data-testid='extras'>{extraOptions}</div>
      {filterOptions.map((f, i) => (
        <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={(e) => f.onSelect(e)}>
          <option value=''>--</option>
          {(f.options || []).map((opt, idx) => {
            const v = opt.value;
            const l = opt.label;
            return (
              <option key={(v || '_') + '-' + idx} value={v}>
                {l}
              </option>
            );
          })}
        </select>
      ))}
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

import KubernetesClusterUpgradeRecommendation from '@components1/recommendations/KubernetesClusterUpgradeRecommendation';

const recommendationApi = require('@api1/recommendation').default;
const { snackbar } = require('@components1/common/snackbarService');

const sampleRecs = [
  {
    id: 'r-1',
    severity: 'Medium',
    updated_at: '2026-05-15T10:00:00Z',
    recommendation: {
      kind: 'PodSecurityPolicy',
      group: 'policy',
      version: 'v1beta1',
      deprecated_version: 'v1.25',
      deleted_version: 'v1.28',
      replacement: { kind: 'PodSecurity', group: 'core', version: 'v1' },
      deleted_items: [{ namespace: 'prod', objectname: 'psp-1' }],
      deprecated_items: [],
    },
    ticket: undefined,
  },
  {
    id: 'r-2',
    severity: 'Low',
    updated_at: '2026-05-15T11:00:00Z',
    recommendation: {
      kind: 'Ingress',
      group: 'extensions',
      version: 'v1beta1',
      deprecated_version: 'v1.28',
      deleted_version: 'v1.30',
      replacement: { kind: 'Ingress', group: 'networking.k8s.io', version: 'v1' },
      deleted_items: [],
      deprecated_items: [{ namespace: 'default', objectname: 'ingress-a' }],
    },
    ticket: { url: 'https://t/1', ticket_id: 'T-1' },
  },
  {
    id: 'r-3',
    severity: 'Info',
    updated_at: '2026-05-15T12:00:00Z',
    recommendation: {
      kind: 'HorizontalPodAutoscaler',
      group: 'autoscaling',
      version: 'v2beta1',
      deprecated_version: 'v1.40', // future, irrelevant
      deleted_version: 'v1.45',
      replacement: {},
      deleted_items: [],
      deprecated_items: [],
    },
    ticket: undefined,
  },
];

const mockResponse = (items = sampleRecs) => ({
  data: { recommendation: items, recommendation_aggregate: { aggregate: { count: items.length } } },
});

describe('KubernetesClusterUpgradeRecommendation (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockHasWriteAccess.mockReturnValue(true);
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse());
    recommendationApi.createRecommendationJob.mockResolvedValue({});
  });

  it('does not fetch when kubernetes.id missing', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{}} />);
    await act(async () => {});
    expect(recommendationApi.getK8sRecommendation).not.toHaveBeenCalled();
  });

  it('fetches list + summary on mount with correct rule + category', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);

    await waitFor(() => expect(recommendationApi.getK8sRecommendation.mock.calls.length).toBeGreaterThanOrEqual(2));
    // Two calls: list (fetchTicket=true) + summary
    const calls = recommendationApi.getK8sRecommendation.mock.calls;
    const listCall = calls.find((c) => c[0].fetchTicket === true);
    expect(listCall[0]).toMatchObject({
      accountId: 'acc-1',
      category: 'InfraUpgrade',
      ruleName: 'k8s_api_deprecated',
      status: ['Open'],
      limit: 1000,
      offset: 0,
    });
    const summaryCall = calls.find((c) => c[0].fetchTicket === undefined);
    expect(summaryCall[0]).toMatchObject({
      accountId: 'acc-1',
      category: 'InfraUpgrade',
      ruleName: 'k8s_api_deprecated',
      limit: 1000,
      offset: 0,
    });
  });

  it('renders summary widget with total count', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('summary-Total Recommendations')).toBeInTheDocument());
    expect(screen.getByTestId('summary-Total Recommendations')).toHaveTextContent('3');
  });

  it('hides summary widget when disableInfographics prop is set', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} disableInfographics />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(screen.queryByTestId('summary-Total Recommendations')).not.toBeInTheDocument();
  });

  it('filters relevant recs: targetVersion match for deleted_version or deprecated_version', async () => {
    // target v1.28: r-1 (deleted=v1.28 → match), r-2 (deprecated=v1.28 → match), r-3 (deprecated=v1.40 future → skip)
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);

    await waitFor(() => expect(screen.getByText('PodSecurityPolicy')).toBeInTheDocument());
    // 'Ingress' appears as both deprecated kind + replacement kind for r-2
    expect(screen.getAllByText('Ingress').length).toBeGreaterThan(0);
    // r-3 deprecated_version=v1.40 (future), not high severity → excluded
    expect(screen.queryByText('HorizontalPodAutoscaler')).not.toBeInTheDocument();
  });

  it('shows all recs when no targetVersion provided (fallback)', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByText('PodSecurityPolicy')).toBeInTheDocument());
    expect(screen.getAllByText('Ingress').length).toBeGreaterThan(0);
    expect(screen.getByText('HorizontalPodAutoscaler')).toBeInTheDocument();
  });

  it('overrides severity to High when deleted_items present', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    // r-1 has deleted_items → severity = High (overrides 'Medium')
    await waitFor(() => expect(screen.getByTestId('label-High')).toBeInTheDocument());
  });

  it('overrides severity to Medium when only deprecated_items present', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    // r-2 has deprecated_items only → severity = Medium (overrides 'Low')
    await waitFor(() => expect(screen.getByTestId('label-Medium')).toBeInTheDocument());
  });

  it('shows Create Ticket menu only when impacted objects exist', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);

    // r-1 and r-2 have impacted items → menu shown
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Create Ticket-r-2')).toBeInTheDocument();
  });

  it('disables Create Ticket menu when ticket already exists', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Create Ticket-r-1')).not.toBeDisabled();
    // r-2 has ticket → disabled
    expect(screen.getByTestId('menu-Create Ticket-r-2')).toBeDisabled();
  });

  it('opens ticket modal with description on menu click', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    expect(screen.getByTestId('ticket-modal')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-subject')).toHaveTextContent('K8s Cluster Version Upgrade Issue');
    const desc = screen.getByTestId('ticket-desc').textContent;
    expect(desc).toMatch(/\*\*Deprecated Api\*\*: PodSecurityPolicy/);
    expect(desc).toMatch(/\*\*Impacted Objects\*\*: prod\/psp-1/);
    expect(desc).toMatch(/\*\*Fixed Api Kind\*\*: PodSecurity/);
  });

  it('refetches after ticket success', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('ticket-success'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
  });

  it('snackbar error on ticket failure', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    fireEvent.click(screen.getByTestId('ticket-failure'));

    expect(snackbar.error).toHaveBeenCalledWith(expect.stringContaining('Bad'));
  });

  it('refetches when status filter changes', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Closed' } });

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    const listCall = recommendationApi.getK8sRecommendation.mock.calls.find((c) => c[0].fetchTicket === true);
    expect(listCall[0].status).toEqual(['Closed']);
  });

  it('disables Generate button without write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('btn-triggerRecommendation')).toBeInTheDocument());
    expect(screen.getByTestId('btn-triggerRecommendation')).toBeDisabled();
  });

  it('triggers Generate scan job (k8s_version_upgrade) when clicked with write access', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});

    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('btn-triggerRecommendation')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('btn-triggerRecommendation'));

    await waitFor(() => expect(recommendationApi.createRecommendationJob).toHaveBeenCalledWith('acc-1', 'k8s_version_upgrade'));
    expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('Scan Triggered'));
    alertSpy.mockRestore();
  });

  it('uses heading prop when provided, defaults to Cluster Upgrade when undefined', async () => {
    const { rerender } = render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} heading='Custom' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Custom'));

    rerender(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} heading={undefined} />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Cluster Upgrade'));
  });

  it('renders RecommendationJobDetails with k8s_version_upgrade jobName', async () => {
    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('job-details')).toHaveTextContent('k8s_version_upgrade'));
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    recommendationApi.getK8sRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty recommendations gracefully', async () => {
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse([]));

    render(<KubernetesClusterUpgradeRecommendation kubernetes={{ id: 'acc-1', version: 'v1.28' }} />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
  });
});
