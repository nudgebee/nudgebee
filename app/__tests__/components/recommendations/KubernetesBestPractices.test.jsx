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
  // Source treats these as plain strings and wraps each into { label, value }.
  // (See KubernetesBestPractices.jsx where it maps RECOMMENDATION_STATUS / SERVERITY
  //  through `(s) => ({ label: s, value: s })`.) Passing objects would double-wrap
  //  and React would try to render the inner object as a child — crash.
  RECOMMENDATION_STATUS: ['Open', 'InProgress', 'Closed'],
  RECOMMENDATION_SERVERITY: ['critical', 'high'],
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

jest.mock('@utils/colors');

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {}, nubi: {} },
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s) => s,
  latestUpdatedAt: (rows) => rows?.[0]?.updated_at ?? null,
}));

jest.mock('src/utils/nubiPromptBuilder', () => ({
  buildNubiOptimizePrompt: ({ ruleName }) => `nubi-prompt-${ruleName}`,
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{String(value || '—')}</span>,
}));

// Severity column — DS SeverityIcon with `level` prop. Test asserts `severity-{level}`.
jest.mock('@ui/SeverityIcon', () => ({
  __esModule: true,
  SeverityIcon: ({ level }) => <span data-testid={`severity-${level}`}>sev</span>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => <span title={typeof title === 'string' ? title : 'tooltip'}>{children}</span>,
}));

jest.mock('@shared/links/TicketLink', () => ({
  __esModule: true,
  default: ({ ticketID }) => <span data-testid='ticket-link'>{ticketID}</span>,
}));

// Per-row DropdownMenu. Item id format from source: `bp-action-ticket-{rowId}`.
jest.mock('@ui/DropdownMenu', () => ({
  __esModule: true,
  DropdownMenu: ({ items, trigger }) => (
    <div data-testid='three-dots'>
      {trigger}
      {(items || []).map((it) => {
        const m = String(it.id || '').match(/^bp-action-ticket-(.+)$/);
        const rowId = m ? m[1] : it.id;
        return (
          <button key={it.id} data-testid={`menu-${it.label}-${rowId}`} onClick={() => it.onSelect?.()} disabled={it.disabled}>
            {it.label}
          </button>
        );
      })}
    </div>
  ),
}));

// DS Button — uses `id` verbatim as testid when present (so source's id='bp-ask-nubi-r-1'
// becomes `btn-bp-ask-nubi-r-1`, and id='triggerRecommendation' becomes `btn-triggerRecommendation`).
jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick, disabled, id, ['aria-label']: ariaLabel }) => (
    <button data-testid={`btn-${id || (typeof children === 'string' ? children : ariaLabel || 'icon')}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Toast', () => ({
  __esModule: true,
  toast: { success: jest.fn(), error: jest.fn(), info: jest.fn() },
}));

jest.mock('@shared/layout/NubiChatSidebar', () => ({
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

jest.mock('@ui/Stat', () => ({
  __esModule: true,
  Stat: ({ label, value }) => <div data-testid={`summary-${label}`}>{value}</div>,
}));

jest.mock('@ui/WidgetCard', () => ({
  __esModule: true,
  default: ({ children }) => <div data-testid='widget-card'>{children}</div>,
}));

jest.mock('@components/tickets/TicketCreatePopupForm', () => ({
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

jest.mock('@components/k8s/common/ClusterNameWithRegion', () => ({
  __esModule: true,
  default: ({ name, region }) => (
    <span data-testid='cluster-name'>
      {name}
      {region}
    </span>
  ),
}));

// ScanRefreshButton is a separate component now. Simulate its public behaviour:
// disabled when no write access; clicking fires createRecommendationJob + alert.
jest.mock('@components/recommendations/ScanRefreshButton', () => ({
  __esModule: true,
  ScanRefreshButton: ({ accountId, jobName }) => {
    const { hasWriteAccess } = require('@lib/auth');
    const api = require('@api1/recommendation').default;
    const disabled = !hasWriteAccess(accountId);
    return (
      <button
        data-testid='btn-triggerRecommendation'
        disabled={disabled}
        onClick={() => {
          api.createRecommendationJob(accountId, jobName);
          window.alert('Scan Triggered');
        }}
      >
        Sync
      </button>
    );
  },
}));

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout = ({ children, id }) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions, title }) => (
    <div data-testid='toolbar'>
      <h2 data-testid='box-heading'>{title || ''}</h2>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect }) => {
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
  default: ({ id, tableData, totalRows, loading, pageNumber }) => (
    <div data-testid='k8s-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      {(tableData || []).map((row, i) => (
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

import KubernetesBestPractices from '@components/recommendations/KubernetesBestPractices';

const recommendationApi = require('@api1/recommendation').default;
const { toast: snackbar } = require('@ui/Toast');

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
    // Source treats namespaces as strings (see `namespaceOptions = namespaceFilter.map((n) => ({ label: n, value: n }))`).
    recommendationApi.listRecommendationNamesapces.mockResolvedValue(['prod', 'kube-system']);
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

  it('renders total recommendations Stat', async () => {
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

  it('Create-ticket menu has different label when ticket exists and is disabled', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    // r-1 (no ticket) → label 'Create ticket'
    await waitFor(() => expect(screen.getByTestId('menu-Create ticket-r-1')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Create ticket-r-1')).not.toBeDisabled();

    // r-2 (has ticket T-1) → label 'Ticket: T-1', disabled
    expect(screen.getByTestId('menu-Ticket: T-1-r-2')).toBeInTheDocument();
    expect(screen.getByTestId('menu-Ticket: T-1-r-2')).toBeDisabled();
  });

  it('opens ticket modal with description on Create ticket menu click', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('menu-Create ticket-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Create ticket-r-1'));

    expect(screen.getByTestId('ticket-modal')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Name\*\*: Misconfiguration/);
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Severity\*\*: critical/);
  });

  it('refetches list after ticket success', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create ticket-r-1'));
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('ticket-success'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
  });

  it('shows snackbar error on ticket failure', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create ticket-r-1'));

    fireEvent.click(screen.getByTestId('ticket-failure'));

    expect(snackbar.error).toHaveBeenCalledWith(expect.stringContaining('Bad'));
  });

  it('opens Nubi sidebar with prompt + account + conv id when Nubi icon clicked', async () => {
    render(<KubernetesBestPractices kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('btn-bp-ask-nubi-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('btn-bp-ask-nubi-r-1'));

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
