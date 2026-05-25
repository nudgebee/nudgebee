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
}));

jest.mock('@lib/datetime', () => ({
  getLast7Days: () => new Date('2026-05-10T00:00:00Z'),
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

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{String(value || '—')}</span>,
}));

jest.mock('@components1/common/widgets/SeverityIcon', () => ({
  __esModule: true,
  default: ({ severityType }: any) => <span data-testid={`severity-${severityType}`}>sev</span>,
}));

jest.mock('@components1/common/CustomTicketLink', () => ({
  __esModule: true,
  default: ({ ticketID }: any) => <a data-testid='ticket-link'>{ticketID}</a>,
}));

jest.mock('@components1/k8s/common/ClusterNameWithRegion', () => ({
  __esModule: true,
  default: ({ name, region }: any) => (
    <span data-testid='cluster-name'>
      {name}
      {region}
    </span>
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

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, filterOptions = [], dateTimeRange, heading }: any) => (
    <div data-testid='box-layout'>
      <h2 data-testid='box-heading'>{heading}</h2>
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
      {dateTimeRange?.enabled && <div data-testid='date-range-enabled'>dr</div>}
      {children}
    </div>
  ),
}));

jest.mock('@components1/cloudaccount/CloudAccountTable', () => ({
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

import CloudAccountTools from '@components1/cloudaccount/CloudAccountTools';

const apiCloudAccount = require('@api1/cloud-account').default;

const sampleEvents = [
  {
    title: 'Tool detected',
    subject_name: 'i-1234',
    subject_namespace: 'prod',
    aggregation_key: 'aws:tool',
    principal: 'arn:aws:iam::123:role/x',
    priority: 'high',
    starts_at: '2026-05-15T10:00:00Z',
  },
  {
    title: 'Config drift',
    subject_name: 'i-5678',
    subject_namespace: null,
    aggregation_key: 'aws:drift',
    principal: 'system',
    priority: 'medium',
    starts_at: '2026-05-15T11:00:00Z',
  },
];

const mockResponse = (events = sampleEvents, count?: number) => ({
  data: {
    events,
    events_aggregate: { aggregate: { count: count ?? events.length } },
  },
});

describe('CloudAccountTools (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockUseCloudFilter.mockReturnValue({
      serviceNamesFilter: [{ label: 'EC2', value: 'ec2' }],
      severityFilterType: [
        { label: 'High', value: 'high' },
        { label: 'Medium', value: 'medium' },
      ],
    });
    apiCloudAccount.listEvents.mockResolvedValue(mockResponse());
  });

  it('does not fetch when accountId missing', async () => {
    render(<CloudAccountTools accountId={undefined} serviceName={undefined} />);
    await act(async () => {});
    expect(apiCloudAccount.listEvents).not.toHaveBeenCalled();
  });

  it('fetches events on mount with accountId + serviceName + default pagination', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    const [params, limit, offset] = apiCloudAccount.listEvents.mock.calls[0];
    expect(params).toEqual({ accountId: 'acc-1', subjectNamespace: 'ec2' });
    expect(limit).toBe(10);
    expect(offset).toBe(0);
  });

  it('renders event rows with title + subject + severity + principal', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByText('Tool detected')).toBeInTheDocument());
    expect(screen.getByText('Config drift')).toBeInTheDocument();
    expect(screen.getByText('i-1234')).toBeInTheDocument();
    expect(screen.getByText('ns: prod')).toBeInTheDocument();
    expect(screen.getByText('arn:aws:iam::123:role/x')).toBeInTheDocument();
    expect(screen.getByTestId('severity-high')).toBeInTheDocument();
    expect(screen.getByTestId('severity-medium')).toBeInTheDocument();
  });

  it('renders heading "Events"', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Events'));
  });

  it('populates service + severity dropdowns from useCloudFilter', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByTestId('filter-Service Name')).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'EC2' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'High' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Medium' })).toBeInTheDocument();
  });

  it('refetches and resets page when severity filter changes', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    apiCloudAccount.listEvents.mockClear();

    fireEvent.change(screen.getByTestId('filter-Severity'), { target: { value: 'high' } });

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    expect(apiCloudAccount.listEvents.mock.calls[0][2]).toBe(0); // offset reset to 0
  });

  it('refetches and resets page when service name filter changes', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    apiCloudAccount.listEvents.mockClear();

    fireEvent.change(screen.getByTestId('filter-Service Name'), { target: { value: 'ec2' } });

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    expect(apiCloudAccount.listEvents.mock.calls[0][2]).toBe(0);
  });

  it('paginates and updates offset on next page', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    apiCloudAccount.listEvents.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiCloudAccount.listEvents).toHaveBeenCalled());
    expect(apiCloudAccount.listEvents.mock.calls[0][2]).toBe(10);
  });

  it('opens HelpBee modal when HelpBee menu clicked', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();

    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    expect(screen.getByTestId('helpbee-modal')).toBeInTheDocument();
  });

  it('closes HelpBee modal on close', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    fireEvent.click(screen.getByTestId('helpbee-close'));

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('Create Ticket menu does not open HelpBee', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('enables date-time range filter', async () => {
    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    await waitFor(() => expect(screen.getByTestId('date-range-enabled')).toBeInTheDocument());
  });

  it('shows loading during fetch and clears after', async () => {
    let resolveFn: any;
    apiCloudAccount.listEvents.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);
    expect(screen.getByTestId('loading')).toBeInTheDocument();

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles API rejection without crashing (loading clears)', async () => {
    apiCloudAccount.listEvents.mockRejectedValue(new Error('boom'));

    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('passes accountId through to useCloudFilter hook', async () => {
    render(<CloudAccountTools accountId='acc-42' serviceName='ec2' />);
    expect(mockUseCloudFilter).toHaveBeenCalledWith('acc-42');
  });

  it('handles empty events list gracefully', async () => {
    apiCloudAccount.listEvents.mockResolvedValue(mockResponse([], 0));

    render(<CloudAccountTools accountId='acc-1' serviceName='ec2' />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
