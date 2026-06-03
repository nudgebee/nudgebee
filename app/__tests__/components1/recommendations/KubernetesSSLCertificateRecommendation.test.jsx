import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getK8sRecommendation: jest.fn(),
    getK8sRecommendationSummary: jest.fn(),
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

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@assets/sidebar-icon/tickets-icon.svg', () => '/tickets.svg', { virtual: true });

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{String(value || '—')}</span>,
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
      <button data-testid='change-size' onClick={() => onPageChange(2, 25)}>
        Resize
      </button>
    </div>
  ),
}));

import KubernetesSSLCertificateRecommendation from '@components1/recommendations/KubernetesSSLCertificateRecommendation';

const recommendationApi = require('@api1/recommendation').default;
const { snackbar } = require('@components1/common/snackbarService');

const sampleRecs = [
  {
    id: 'r-1',
    updated_at: '2026-05-15T10:00:00Z',
    recommendation: {
      namespace: 'prod',
      name: 'web-cert',
      days_until_expiry: 30,
      expiry_date: '2026-06-15T10:00:00Z',
    },
    ticket: undefined,
  },
  {
    id: 'r-2',
    updated_at: '2026-05-15T11:00:00Z',
    recommendation: {
      namespace: 'staging',
      name: 'api-cert',
      days_until_expiry: -5,
      expiry_date: '2026-05-10T10:00:00Z', // already expired
    },
    ticket: { url: 'https://t/1', ticket_id: 'T-1' },
  },
  {
    id: 'r-3',
    updated_at: '2026-05-15T12:00:00Z',
    recommendation: {
      namespace: 'qa',
      name: 'mid-cert',
      days_until_expiry: 10,
      expiry_date: '2026-05-25T10:00:00Z',
    },
    ticket: undefined,
  },
];

const mockResponse = (items = sampleRecs) => ({
  data: { recommendation: items, recommendation_aggregate: { aggregate: { count: items.length } } },
});

describe('KubernetesSSLCertificateRecommendation (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse());
    recommendationApi.getK8sRecommendationSummary.mockResolvedValue({
      data: { recommendation_aggregate: { aggregate: { count: 3 } } },
    });
    recommendationApi.createRecommendationJob.mockResolvedValue({});
  });

  it('does not fetch when kubernetes.id missing', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{}} />);
    await act(async () => {});
    expect(recommendationApi.getK8sRecommendation).not.toHaveBeenCalled();
    expect(recommendationApi.getK8sRecommendationSummary).not.toHaveBeenCalled();
  });

  it('fetches list + summary on mount with default Open status', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => {
      expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled();
      expect(recommendationApi.getK8sRecommendationSummary).toHaveBeenCalled();
    });
    const call = recommendationApi.getK8sRecommendation.mock.calls[0][0];
    expect(call).toMatchObject({
      accountId: 'acc-1',
      category: 'Configuration',
      ruleName: 'certificate_expiry',
      status: ['Open'],
      limit: 10,
      offset: 0,
      fetchTicket: true,
    });
  });

  it('renders summary widget with cert count', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('summary-Total Certificates')).toBeInTheDocument());
    expect(screen.getByTestId('summary-Total Certificates')).toHaveTextContent('3');
  });

  it('renders rows sorted by days_until_expiry ascending', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);

    // r-2 (-5) before r-3 (10) before r-1 (30)
    await waitFor(() => expect(screen.getByTestId('row-0')).toBeInTheDocument());
    const row0 = screen.getByTestId('row-0').textContent;
    const row1 = screen.getByTestId('row-1').textContent;
    const row2 = screen.getByTestId('row-2').textContent;
    expect(row0).toMatch(/api-cert/); // expired first
    expect(row1).toMatch(/mid-cert/);
    expect(row2).toMatch(/web-cert/);
  });

  it('renders existing ticket link when rec has ticket', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('custom-link')).toBeInTheDocument());
    expect(screen.getByTestId('custom-link')).toHaveAttribute('href', 'https://t/1');
    expect(screen.getByTestId('custom-link')).toHaveTextContent('T-1');
  });

  it('disables Create Ticket menu when ticket already exists', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Create Ticket-r-1')).not.toBeDisabled();
    expect(screen.getByTestId('menu-Create Ticket-r-2')).toBeDisabled();
    expect(screen.getByTestId('menu-Create Ticket-r-3')).not.toBeDisabled();
  });

  it('opens ticket modal on Create Ticket menu click with cert description', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    expect(screen.getByTestId('ticket-modal')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-subject')).toHaveTextContent('K8s SSL Certificate Upgrade Recommendation');
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Certificate Namespace\*\*: prod/);
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Certificate Name\*\*: web-cert/);
    expect(screen.getByTestId('ticket-desc').textContent).toMatch(/\*\*Certificate Expire In\*\*: 30 days/);
  });

  it('refetches list after ticket success (but not summary)', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('ticket-success'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
  });

  it('snackbar error on ticket failure', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('menu-Create Ticket-r-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Create Ticket-r-1'));

    fireEvent.click(screen.getByTestId('ticket-failure'));

    expect(snackbar.error).toHaveBeenCalledWith(expect.stringContaining('Bad'));
  });

  it('refetches list + summary on status filter change', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();
    recommendationApi.getK8sRecommendationSummary.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Closed' } });

    await waitFor(() => {
      expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled();
      expect(recommendationApi.getK8sRecommendationSummary).toHaveBeenCalled();
    });
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].status).toEqual(['Closed']);
    expect(recommendationApi.getK8sRecommendationSummary.mock.calls[0][0].status).toEqual(['Closed']);
  });

  it('paginates and updates offset on next page', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(recommendationApi.getK8sRecommendation.mock.calls[0][0].offset).toBe(10);
  });

  it('updates rowsPerPage when changePage called with different limit', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    recommendationApi.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('change-size'));

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    const lastCall = recommendationApi.getK8sRecommendation.mock.calls.at(-1)[0];
    expect(lastCall.limit).toBe(25);
  });

  it('triggers Generate scan job on extra button click', async () => {
    const alertSpy = jest.spyOn(window, 'alert').mockImplementation(() => {});

    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('btn-triggerRecommendation')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('btn-triggerRecommendation'));

    await waitFor(() => expect(recommendationApi.createRecommendationJob).toHaveBeenCalledWith('acc-1', 'certificate_scanner'));
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

    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty list gracefully', async () => {
    recommendationApi.getK8sRecommendation.mockResolvedValue(mockResponse([]));

    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(recommendationApi.getK8sRecommendation).toHaveBeenCalled());
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });

  it('uses heading prop when provided, defaults to Cluster Upgrade when undefined', async () => {
    const { rerender } = render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} heading='SSL Certs' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('SSL Certs'));

    rerender(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Cluster Upgrade'));
  });

  it('renders RecommendationJobDetails with certificate_scanner job name', async () => {
    render(<KubernetesSSLCertificateRecommendation kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('job-details')).toHaveTextContent('certificate_scanner'));
  });
});
