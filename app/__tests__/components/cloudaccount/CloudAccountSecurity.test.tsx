import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    listEvents: jest.fn(),
  },
}));

const mockUseCloudFilter = jest.fn();
jest.mock('@hooks/useCloudFilters', () => ({
  useCloudFilter: (...args: any[]) => mockUseCloudFilter(...args),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  getBrandingAsset: () => '/helpbee.svg',
  useTenantBranding: () => ({}),
}));

jest.mock('@assets', () => ({
  TicketsIcon: '/tickets.svg',
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@utils/colors');

jest.mock('@utils/common', () => ({
  toSeverityLevel: (s: string) => String(s || '').toLowerCase(),
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value}</span>,
}));

jest.mock('@ui/SeverityIcon', () => ({
  __esModule: true,
  SeverityIcon: ({ level }: any) => <span data-testid={`severity-${level}`}>sev</span>,
}));

jest.mock('@components/k8s/common/ClusterNameWithRegion', () => ({
  __esModule: true,
  default: ({ name }: any) => <span data-testid='cluster-name'>{name}</span>,
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

jest.mock('@components/helpbee', () => ({
  __esModule: true,
  default: ({ isModalVisible, onClose }: any) =>
    isModalVisible ? (
      <div data-testid='helpbee-modal'>
        <button data-testid='helpbee-close' onClick={onClose}>
          close
        </button>
      </div>
    ) : null,
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

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout: any = ({ children, id }: any) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }: any) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }: any) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect }: any) => (
    <select data-testid={`filter-${label}`} value={value || ''} onChange={onSelect}>
      <option value=''>--</option>
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
  ),
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

import CloudAccountSecurity from '@components/cloudaccount/CloudAccountSecurity';

const apiCloudAccount = require('@api1/cloud-account').default;

const sampleEvents = [
  {
    title: 'UnauthorizedAccess detected',
    subject_name: 'i-1234',
    subject_namespace: 'prod',
    aggregation_key: 'IAM:Login',
    principal: 'arn:aws:iam::123:user/alice',
    priority: 'high',
    starts_at: '2026-05-15T10:00:00Z',
    evidences: '[{"type":"json","data":"{\\"key\\":\\"value\\"}"}]',
  },
  {
    title: 'RootAccountUsage',
    subject_name: 'i-5678',
    subject_namespace: null,
    aggregation_key: 'IAM:Root',
    principal: 'root',
    priority: 'critical',
    starts_at: '2026-05-15T11:00:00Z',
    evidences: '[]',
  },
];

const mockResponse = (events = sampleEvents, count?: number) => ({
  data: {
    events,
    events_aggregate: { aggregate: { count: count ?? events.length } },
  },
});

describe('CloudAccountSecurity (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockUseCloudFilter.mockReturnValue({
      serviceNamesFilter: ['ec2'],
      severityFilterType: ['High', 'Critical'],
    });
    apiCloudAccount.listEvents.mockResolvedValue(mockResponse());
  });

  it('does not fetch when accountId is missing', async () => {
    render(<CloudAccountSecurity accountId={undefined} serviceName={undefined} />);

    await new Promise((r) => setTimeout(r, 50));
    expect(apiCloudAccount.listEvents).not.toHaveBeenCalled();
  });

  it('fetches events on mount with accountId + serviceName + default pagination', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    const call = apiCloudAccount.listEvents.mock.calls[0];
    expect(call[0]).toEqual({ accountId: 'acc-1', subjectNamespace: 'ec2' });
    expect(call[1]).toBe(10);
    expect(call[2]).toBe(0);
  });

  it('renders event rows from response', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByText('UnauthorizedAccess detected')).toBeInTheDocument());
    expect(screen.getByText('RootAccountUsage')).toBeInTheDocument();
    expect(screen.getByText('i-1234')).toBeInTheDocument();
    expect(screen.getByText('ns: prod')).toBeInTheDocument();
    expect(screen.getByTestId('severity-high')).toBeInTheDocument();
    expect(screen.getByTestId('severity-critical')).toBeInTheDocument();
  });

  it('populates severity dropdown from useCloudFilter', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByTestId('filter-Severity')).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'High' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Critical' })).toBeInTheDocument();
  });

  it('refetches with reset page on severity filter change', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    apiCloudAccount.listEvents.mockClear();

    fireEvent.change(screen.getByTestId('filter-Severity'), { target: { value: 'High' } });

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    expect(apiCloudAccount.listEvents.mock.calls[0][2]).toBe(0);
  });

  it('paginates and updates offset on next page', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    apiCloudAccount.listEvents.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    expect(apiCloudAccount.listEvents.mock.calls[0][2]).toBe(10);
  });

  it('opens HelpBee modal when menu HelpBee clicked', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();

    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    expect(screen.getByTestId('helpbee-modal')).toBeInTheDocument();
  });

  it('closes HelpBee modal when onClose called', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    fireEvent.click(screen.getByTestId('helpbee-close'));

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('does not open HelpBee for Create Ticket menu (id=0)', async () => {
    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('shows loading during fetch and clears after', async () => {
    let resolveFn: any;
    apiCloudAccount.listEvents.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);
    expect(screen.getByTestId('loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles API rejection without crashing (loading clears)', async () => {
    apiCloudAccount.listEvents.mockRejectedValue(new Error('boom'));

    render(<CloudAccountSecurity accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('passes accountId hook through to useCloudFilter', async () => {
    render(<CloudAccountSecurity accountId='acc-42' serviceName='ec2' />);
    expect(mockUseCloudFilter).toHaveBeenCalledWith('acc-42');
    // Wait for the mount-effect's async fetch to settle, otherwise the
    // setLoading/setEvents/setEventsCount that fire after this test returns
    // leak into the next test and trigger an act() warning.
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
  });
});
