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
    listRecommendationNamesapces: jest.fn(),
    getK8sRecommendation: jest.fn(),
    getK8sRecommendationSummary: jest.fn(),
    createRecommendationJob: jest.fn(),
  },
  RECOMMENDATION_STATUS: [
    { label: 'Open', value: 'Open' },
    { label: 'In Progress', value: 'InProgress' },
    { label: 'Closed', value: 'Closed' },
  ],
  RECOMMENDATION_SERVERITY: [
    { label: 'Critical', value: 'critical' },
    { label: 'High', value: 'high' },
  ],
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

const mockUseData = jest.fn();
jest.mock('@context/DataContext', () => ({
  useData: () => mockUseData(),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ assistantName: 'Nubi' }),
  getNubiIconUrl: () => '/nubi.svg',
}));

jest.mock('@lib/collections', () => ({
  unique: (arr) => Array.from(new Set(arr)),
}));

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { primary: '#000', secondary: '#666', secondaryDark: '#333', white: '#fff', lastSync: '#999' },
    background: { white: '#fff', red: '#f00' },
  },
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {}, nubi: {} },
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s) => s,
}));

jest.mock('src/utils/nubiPromptBuilder', () => ({
  buildNubiOptimizePrompt: ({ ruleName }) => `nubi-prompt-${ruleName}`,
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/widgets/SeverityIcon', () => ({
  __esModule: true,
  default: ({ severityType }) => <span data-testid={`severity-${severityType || 'none'}`}>sev</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{String(value || '—')}</span>,
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => <span title={typeof title === 'string' ? title : 'tooltip'}>{children}</span>,
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
    <button data-testid={`btn-${id || 'custom'}`} onClick={onClick} disabled={disabled}>
      {text || 'btn'}
    </button>
  ),
}));

jest.mock('@components1/common/NubiChatSidebar', () => ({
  __esModule: true,
  default: ({ isVisible, accountId, queryPrefix, context }) =>
    isVisible ? (
      <div data-testid='nubi-sidebar'>
        <div data-testid='nubi-account'>{accountId}</div>
        <div data-testid='nubi-query'>{queryPrefix}</div>
        <div data-testid='nubi-conv-id'>{context?.data?.conversationId}</div>
      </div>
    ) : null,
}));

jest.mock('@components1/optimise/SummaryWidget', () => ({
  __esModule: true,
  default: ({ title, value }) => <div data-testid={`summary-${title}`}>{value}</div>,
}));

jest.mock('@components1/tickets/TicketCreatePopupForm', () => ({
  __esModule: true,
  default: ({ open, ticketData, onSuccess, onFailure, onClose }) =>
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
        <button data-testid='ticket-close' onClick={onClose}>
          Close
        </button>
      </div>
    ) : null,
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

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn(), info: jest.fn() },
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
      {children}
    </div>
  ),
}));

jest.mock('@components1/k8s/common/KubernetesTable2', () => ({
  __esModule: true,
  default: ({ id, data, totalRows, loading, pageNumber }) => (
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
    </div>
  ),
}));

import KubernetesBestPractices from '@components1/recommendations/KubernetesBestPractices';

const recommendationApi = require('@api1/recommendation').default;
const { snackbar } = require('@components1/common/snackbarService');

const sampleRecommendations = [
  {
    id: 'r-1',
    rule_name: 'misconfigurations',
    severity: 'critical',
    updated_at: '2026-05-15T10:00:00Z',
    recommendation: [{ namespace: 'prod', kind: 'Deployment', name: 'web', message: 'no resource limits' }],
    ticket: undefined,
  },
  {
    id: 'r-2',
    rule_name: 'health_check',
    severity: 'high',
    updated_at: '2026-05-15T11:00:00Z',
    recommendation: { workload: { namespace: 'kube-system', kind: 'Deployment', name: 'kube-dns' }, messages: ['Pod restart loop'] },
    ticket: { url: 'https://t/1', ticket_id: 'T-1' },
  },
];

const mockRecResponse = (items = sampleRecommendations) => ({
  data: { recommendation: items, recommendation_aggregate: { aggregate: { count: items.length } } },
});

describe('KubernetesBestPractices (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockHasWriteAccess.mockReturnValue(true);
    mockUseData.mockReturnValue({
      selectedCluster: {
        agent: {
          connection_status: {
            schedule_jobs: [{ runnable_params: { action_func_name: 'popeye_scan' }, state: { last_exec_time_sec: 1731600000 } }],
          },
        },
      },
    });
    recommendationApi.listRecommendationNamesapces.mockResolvedValue([
      { label: 'prod', value: 'prod' },
      { label: 'kube-system', value: 'kube-system' },
    ]);
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockRecResponse());
    recommendationApi.getK8sRecommendationSummary.mockResolvedValue({
      data: { recommendation_aggregate: { aggregate: { count: 42 } } },
    });
    recommendationApi.createRecommendationJob.mockResolvedValue({});
  });

  it('does not fetch when kubernetes.id is missing', async () => {
    render(<KubernetesBestPractices kubernetes={{}} />);
    await act(async () => {});
    expect(recommendationApi.getK8sRecommendation).not.toHaveBeenCalled();
    expect(recommendationApi.getK8sRecommendationSummary).not.toHaveBeenCalled();
  });

  it('fetches list + namespaces + summary on mount with default filters', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => {
      expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled();
      expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled();
      expect(recommendationApi.getK8sRecommendationSummary).toHaveBeenCalled();
    });

    const call = recommendationApi.getK8sRecommendation.mock.calls[0][0];
    expect(call).toMatchObject({
      accountId: 'acc-1',
      category: 'Configuration',
      ruleName: '',
      severity: '',
      status: ['Open'],
      resourceNamespace: '',
      limit: 10,
      offset: 0,
      fetchTicket: true,
    });
  });

  it('renders total recommendations summary widget', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('summary-Total Recommendations')).toBeInTheDocument());
    expect(screen.getByTestId('summary-Total Recommendations')).toHaveTextContent('42');
  });

  it('renders heading from props or default', async () => {
    const { rerender } = render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Best Practices'));

    rerender(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} heading='Custom Title' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Custom Title'));
  });

  it('renders rows with rule_name → label mapping and severity', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getAllByText('Misconfiguration').length).toBeGreaterThan(0));
    expect(screen.getAllByText('Health Check').length).toBeGreaterThan(0);
    expect(screen.getByTestId('severity-critical')).toBeInTheDocument();
    expect(screen.getByTestId('severity-high')).toBeInTheDocument();
  });

  it('renders existing ticket link when rec has ticket', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('ticket-link')).toBeInTheDocument());
    expect(screen.getByTestId('ticket-link')).toHaveTextContent('T-1');
  });

  it('Create Ticket menu disabled when ticket already exists', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-2')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Create Ticket-r-2')).toBeDisabled();
    expect(screen.getByTestId('menu-Create Ticket-r-1')).not.toBeDisabled();
  });

  it('opens ticket modal with description on Create Ticket menu click', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    expect(screen.getByTestId('ticket-modal')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Name\*\*: Misconfiguration/);
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Severity\*\*: critical/);
  });

  it('refetches list after ticket success', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('ticket-success'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
  });

  it('shows snackbar error on ticket failure', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    fireEvent.click(screen.getByTestId('ticket-failure'));

    expect(snackbar.error).toHaveBeenCalledWith(expect.stringContaining('Bad'));
  });

  it('opens Nubi sidebar with prompt + account + conv id when Nubi icon clicked', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('bp-ask-nubi-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('bp-ask-nubi-r-1'));

    expect(screen.getByTestId('nubi-sidebar')).toBeInTheDocument();
    expect(screen.getByTestId('nubi-account')).toHaveTextContent('acc-1');
    expect(screen.getByTestId('nubi-conv-id')).toHaveTextContent('recom_r-1');
    expect(screen.getByTestId('nubi-query').textContent).toMatch(/^nubi-prompt-Misconfiguration$/);
  });

  it('refetches with severity filter on change', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Severity'), { target: { value: 'critical' } });

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].severity).toBe('critical');
  });

  it('refetches with status filter on change', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Closed' } });

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].status).toEqual(['Closed']);
  });

  it('refetches with ruleName + namespace filters and refreshes namespace list', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.listRecommendationNamesapces.mockClear();
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Rule Name'), { target: { value: 'misconfigurations' } });

    await waitFor(() => {
      expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled();
      expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled();
    });
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].ruleName).toBe('misconfigurations');
  });

  it('triggers refresh job on Sync icon click', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});

    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('btn-triggerRecommendation')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('btn-triggerRecommendation'));

    await waitFor(() => expect(recommendationApi.createRecommendationJob).toHaveBeenCalledWith('acc-1', 'popeye_scan'));
    expect(alertSpy).toHaveBeenCalledWith(expect.stringContaining('Scan Triggered'));
    alertSpy.mockRestore();
  });

  it('disables refresh button without write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('btn-triggerRecommendation')).toBeInTheDocument());
    expect(screen.getByTestId('btn-triggerRecommendation')).toBeDisabled();
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    recommendationApi.getK8sRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockRecResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty list gracefully', async () => {
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockRecResponse([]));

    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });

  it('handles namespaces fetch rejection without crashing', async () => {
    recommendationApi.listRecommendationNamesapces.mockRejectedValue(new Error('boom'));

    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(screen.getByTestId('filter-Namespace')).toBeInTheDocument();
  });
});
