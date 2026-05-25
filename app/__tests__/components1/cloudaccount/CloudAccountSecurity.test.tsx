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

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, filterOptions = [], heading }: any) => (
    <div data-testid='box-layout'>
      <h2>{heading}</h2>
      {filterOptions.map((f: any, i: number) => (
        <select key={i} data-testid={`filter-${f.label}`} onChange={f.onSelect}>
          <option value=''>--</option>
          {(f.options || []).map((opt: any) => (
            <option key={opt.value || opt.label} value={opt.value || opt.label}>
              {opt.label}
            </option>
          ))}
        </select>
      ))}
      {children}
    </div>
  ),
}));

jest.mock('./../../../src/components1/cloudaccount/CloudAccountTable', () => ({
  __esModule: true,
  default: ({ id, data, totalRows, loading, pageNumber, onPageChange, expandable }: any) => (
    <div data-testid='cloud-account-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='expandable-text'>{expandable?.tabs?.[0]?.text}</div>
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

jest.mock('@components1/helpbee', () => ({
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

jest.mock('@components1/common/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }: any) => (
    <div data-testid='three-dots'>
      {(menuItems || []).map((mi: any) => (
        <button key={mi.id} data-testid={`menu-${mi.label}`} onClick={() => onMenuClick(mi, data)}>
          {mi.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/k8s/common/ClusterNameWithRegion', () => ({
  __esModule: true,
  default: ({ name }: any) => <span data-testid='cluster-name'>{name}</span>,
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value}</span>,
}));

jest.mock('@components1/common/widgets/SeverityIcon', () => ({
  __esModule: true,
  default: ({ severityType }: any) => <span data-testid={`severity-${severityType}`}>sev</span>,
}));

import CloudAccountSecurity from '@components1/cloudaccount/CloudAccountSecurity';

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
      serviceNamesFilter: [{ label: 'EC2', value: 'ec2' }],
      severityFilterType: [
        { label: 'High', value: 'high' },
        { label: 'Critical', value: 'critical' },
      ],
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

    fireEvent.change(screen.getByTestId('filter-Severity'), { target: { value: 'high' } });

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
  });
});
