import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getK8sRecommendation: jest.fn(),
    getRecommendationDetails: jest.fn(),
  },
}));

let nextPageVal = 0;
let pageSizeVal = 10;
const mockSetPage = jest.fn((p: number) => {
  nextPageVal = p;
});
const mockChangePage = jest.fn((p: number) => {
  nextPageVal = p - 1;
});
jest.mock('@hooks/usePagination', () => ({
  usePagination: () => ({
    page: nextPageVal,
    rowsPerPage: pageSizeVal,
    setPage: mockSetPage,
    changePage: mockChangePage,
  }),
}));

const mockUseCurrencySymbol = jest.fn();
jest.mock('@hooks/useCurrencySymbol', () => ({
  useCurrencySymbol: (...args: any[]) => mockUseCurrencySymbol(...args),
}));

jest.mock('@ui/Toast', () => ({
  __esModule: true,
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s: string) => (s || '').replace(/_/g, ' '),
  toSeverityLevel: (s: string) => String(s || '').toLowerCase(),
}));

jest.mock('@utils/common', () => ({
  snakeToTitleCase: (s: string) => (s || '').replace(/_/g, ' '),
  toSeverityLevel: (s: string) => String(s || '').toLowerCase(),
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@utils/colors');

jest.mock('@assets', () => ({
  AutoPilotGreyIcon: '/autopilot.svg',
  TicketsIcon: '/tickets.svg',
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{String(value || '—')}</span>,
}));

jest.mock('@ui/SeverityIcon', () => ({
  __esModule: true,
  SeverityIcon: ({ level }: any) => <span data-testid={`severity-${level}`}>sev</span>,
}));

jest.mock('@shared/format/Currency', () => ({
  __esModule: true,
  default: ({ value, prefix, suffix }: any) => (
    <span data-testid='currency'>
      {prefix}
      {value}
      {suffix}
    </span>
  ),
}));

jest.mock('@shared/buttons/DownloadButton', () => ({
  __esModule: true,
  default: ({ onClick }: any) => (
    <button data-testid='download-btn' onClick={onClick}>
      DL
    </button>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: any) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick, disabled }: any) => (
    <button data-testid={`btn-${typeof children === 'string' ? children : 'icon'}`} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/DropdownMenu', () => ({
  __esModule: true,
  DropdownMenu: ({ items, trigger }: any) => (
    <div data-testid='three-dots'>
      {trigger}
      {(items || []).map((it: any) => (
        <button key={it.id} data-testid={`menu-${it.label}`} onClick={() => it.onSelect?.()} disabled={it.disabled}>
          {it.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components/cloudaccount/common', () => ({
  getTicketDescription: (data: any) => `desc:${data?.rule_name || '-'}`,
  DataBlock: ({ title, data }: any) => <div data-testid={`datablock-${title}`}>{data}</div>,
  CustomText: ({ text1, text2 }: any) => (
    <span>
      {text1}
      {text2 ? ` ${text2}` : ''}
    </span>
  ),
}));

jest.mock('@components/tickets/TicketCreatePopupForm', () => ({
  __esModule: true,
  default: ({ open, ticketData, onSuccess, onFailure, handleClose }: any) =>
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
        <button data-testid='ticket-close' onClick={handleClose}>
          Close
        </button>
      </div>
    ) : null,
}));

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout: any = ({ children, id }: any) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions, title }: any) => (
    <div data-testid='toolbar'>
      <h2 data-testid='box-heading'>{title || ''}</h2>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }: any) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect }: any) => {
    const currentValue = typeof value === 'object' && value !== null ? value.value : value;
    return (
      <select
        data-testid={`filter-${label}`}
        value={currentValue || ''}
        onChange={(e) => onSelect?.({ target: { value: e.target.value } }, { value: e.target.value, label: e.target.value })}
      >
        {(options || []).map((opt: any, idx: number) => {
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

jest.mock('@components/cloudaccount/CloudAccountTable', () => ({
  __esModule: true,
  default: ({ id, data, totalRows, loading, pageNumber, onPageChange }: any) => (
    <div data-testid='cloud-account-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      {(data || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2)}>
        Next
      </button>
    </div>
  ),
}));

import ServiceRecommendations from '@components/cloudaccount/ServiceRecommendations';

const apiRecommendations = require('@api1/recommendation').default;
const { toast: snackbar } = require('@ui/Toast');

const sampleRecs = [
  {
    id: 'r-1',
    severity: 'critical',
    category: 'RightSizing',
    rule_name: 'oversized_instance',
    updated_at: '2026-05-15T10:00:00Z',
    resource_name: 'i-1234',
    resource_cloud_service: 'ec2',
    estimated_savings: 120.5,
    recommendation: { reason: 'CPU < 5% over 30 days' },
  },
  {
    id: 'r-2',
    severity: 'high',
    category: 'Security',
    rule_name: 'open_security_group',
    updated_at: '2026-05-15T11:00:00Z',
    account_object_id: 'arn:aws:rds:us-east-1:123:db:my-db',
    estimated_savings: 0,
    recommendation: { description: 'Open SG to 0.0.0.0/0' },
  },
];

const mockResponse = (items = sampleRecs, count?: number) => ({
  data: {
    recommendation: items,
    recommendation_aggregate: { aggregate: { count: count ?? items.length } },
  },
});

describe('ServiceRecommendations (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    nextPageVal = 0;
    pageSizeVal = 10;
    mockUseCurrencySymbol.mockReturnValue('$');
    apiRecommendations.getK8sRecommendation.mockResolvedValue(mockResponse());
    apiRecommendations.getRecommendationDetails.mockImplementation((category: string, rule: string) =>
      rule === 'oversized_instance' ? { title: 'Oversized Instance' } : null
    );
  });

  it('does not fetch when accountId missing', async () => {
    render(<ServiceRecommendations accountId='' serviceName='ec2' provider='AWS' />);
    await act(async () => {});
    expect(apiRecommendations.getK8sRecommendation).not.toHaveBeenCalled();
  });

  it('does not fetch when serviceName missing', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='' provider='AWS' />);
    await act(async () => {});
    expect(apiRecommendations.getK8sRecommendation).not.toHaveBeenCalled();
  });

  it('fetches recommendations on mount with all categories array', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(apiRecommendations.getK8sRecommendation).toHaveBeenCalled());
    const call = apiRecommendations.getK8sRecommendation.mock.calls[0][0];
    expect(call).toMatchObject({
      accountId: 'acc-1',
      ruleName: '',
      severity: '',
      serviceName: 'ec2',
      limit: 10,
      offset: 0,
    });
    expect(call.category).toEqual(['RightSizing', 'Configuration', 'Security', 'InfraUpgrade']);
  });

  it('renders rows with severity + category + resource info', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.getByTestId('severity-critical')).toBeInTheDocument());
    expect(screen.getByTestId('severity-high')).toBeInTheDocument();
    expect(screen.getAllByText('Right Sizing').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Security').length).toBeGreaterThan(0);
    expect(screen.getByText('i-1234')).toBeInTheDocument();
  });

  it('renders rule title from getRecommendationDetails when available', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.getByText('Oversized Instance')).toBeInTheDocument());
    expect(screen.getByText('open security group')).toBeInTheDocument();
  });

  it('extracts service+resource from account_object_id when fields missing', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.getByText('my-db')).toBeInTheDocument());
    expect(screen.getByText('Svc: rds')).toBeInTheDocument();
  });

  it('renders Currency with hook prefix and /yr suffix', async () => {
    mockUseCurrencySymbol.mockReturnValue('€');
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.getAllByTestId('currency').length).toBeGreaterThan(0));
    const cur = screen.getAllByTestId('currency')[0].textContent;
    expect(cur).toMatch(/^€/);
    expect(cur).toMatch(/\/yr$/);
  });

  it('renders recommendation reason or description', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.getByText('CPU < 5% over 30 days')).toBeInTheDocument());
    expect(screen.getByText('Open SG to 0.0.0.0/0')).toBeInTheDocument();
  });

  it('Resolve menu item is disabled', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Resolve').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('menu-Resolve')[0]).toBeDisabled();
  });

  it('Create Ticket menu opens ticket modal with subject', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    expect(screen.getByTestId('ticket-modal')).toBeInTheDocument();
    expect(screen.getByTestId('ticket-subject')).toHaveTextContent('Cloud Optimization - oversized_instance');
    expect(screen.getByTestId('ticket-desc')).toHaveTextContent('desc:oversized_instance');
  });

  it('closes ticket modal on success without refetch', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);
    apiRecommendations.getK8sRecommendation.mockClear();

    fireEvent.click(screen.getByTestId('ticket-success'));

    expect(screen.queryByTestId('ticket-modal')).not.toBeInTheDocument();
    expect(apiRecommendations.getK8sRecommendation).not.toHaveBeenCalled();
  });

  it('snackbar error on ticket failure', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    fireEvent.click(screen.getByTestId('ticket-failure'));

    expect(snackbar.error).toHaveBeenCalledWith('Bad');
  });

  it('refetches with single category when filter changes', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(apiRecommendations.getK8sRecommendation).toHaveBeenCalled());
    apiRecommendations.getK8sRecommendation.mockClear();

    fireEvent.change(screen.getByTestId('filter-Category'), { target: { value: 'Security' } });

    await waitFor(() => expect(apiRecommendations.getK8sRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.getK8sRecommendation.mock.calls[0][0].category).toBe('Security');
  });

  it('calls changePage on next page click (pagination wired to hook)', async () => {
    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(apiRecommendations.getK8sRecommendation).toHaveBeenCalled());

    fireEvent.click(screen.getByTestId('next-page'));

    expect(mockChangePage).toHaveBeenCalledWith(2);
  });

  it('shows loading state during fetch', async () => {
    let resolveFn: any;
    apiRecommendations.getK8sRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('clears recommendations and count on API rejection', async () => {
    apiRecommendations.getK8sRecommendation.mockRejectedValueOnce(new Error('boom'));

    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
    expect(screen.getByTestId('total')).toHaveTextContent('0');
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });

  it('handles empty recommendations list gracefully', async () => {
    apiRecommendations.getK8sRecommendation.mockResolvedValue(mockResponse([], 0));

    render(<ServiceRecommendations accountId='acc-1' serviceName='ec2' provider='AWS' />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });

  it('passes accountId through to useCurrencySymbol hook', async () => {
    render(<ServiceRecommendations accountId='acc-42' serviceName='ec2' provider='AWS' />);
    expect(mockUseCurrencySymbol).toHaveBeenCalledWith('acc-42');
    await waitFor(() => expect(apiRecommendations.getK8sRecommendation).toHaveBeenCalled());
  });
});
