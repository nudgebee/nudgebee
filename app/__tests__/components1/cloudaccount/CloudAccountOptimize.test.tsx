import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    getCloudResource: jest.fn(),
  },
}));

jest.mock('@lib/datetime', () => ({
  getLast7Days: () => new Date('2026-05-10T00:00:00Z'),
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@components1/cloudaccount/common', () => ({
  MENU_ITEMS: [
    { label: 'Create Ticket', id: 0 },
    { label: 'HelpBee', id: 1 },
  ],
}));

jest.mock('@components1/cloudaccount/ec2/Instances', () => ({
  CustomText: ({ text1, text2 }: any) => (
    <span data-testid='custom-text'>
      {text1}
      {text2 ? ` ${text2}` : ''}
    </span>
  ),
}));

jest.mock('@components1/cloudaccount/OptimizeUnutilized', () => ({
  __esModule: true,
  default: ({ account, workloadName }: any) => (
    <div data-testid='optimize-utilization'>
      {account}/{workloadName}
    </div>
  ),
}));

jest.mock('@components1/common/format/Currency', () => ({
  __esModule: true,
  default: ({ value, suffix }: any) => (
    <span data-testid='currency'>
      {value}
      {suffix}
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

jest.mock('@components1/k8s/common/RecommendationJobDetails', () => ({
  __esModule: true,
  default: ({ jobName }: any) => <div data-testid='job-details'>{jobName}</div>,
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, heading, filterOptions = [], dateTimeRange }: any) => (
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
  default: ({ id, data, totalRows, loading, pageNumber, onPageChange, expandable, headers }: any) => (
    <div data-testid='cloud-account-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{(headers || []).join('|')}</div>
      <div data-testid='expandable-text'>{expandable?.tabs?.[0]?.text}</div>
      {(data || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
          {i === 0 && expandable?.tabs?.[0] && (
            <div data-testid={`row-${i}-expandable`}>{expandable.tabs[0].componentFn('acc-1', row[0]?.drilldownQuery)}</div>
          )}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2)}>
        Next
      </button>
    </div>
  ),
}));

import CloudAccountOptimize from '@components1/cloudaccount/CloudAccountOptimize';

const apiCloudAccount = require('@api1/cloud-account').default;

const sampleResources = [
  {
    name: 'i-1234',
    resourse_id: 'i-1234-id',
    resourceId: 'i-1234-id',
    meta: { namespace: 'prod' },
  },
  {
    name: 'i-5678',
    resourse_id: 'i-5678-id',
    resourceId: 'i-5678-id',
    meta: { namespace: 'dev' },
  },
];

const mockResponse = (items = sampleResources, count?: number) => ({
  data: { data: { cloud_resourses: items, cloud_resourses_aggregate: { aggregate: { count: count ?? items.length } } } },
});

describe('CloudAccountOptimize (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiCloudAccount.getCloudResource.mockResolvedValue(mockResponse());
  });

  it('does not fetch when accountId missing', async () => {
    render(<CloudAccountOptimize accountId={undefined} heading='' />);
    await act(async () => {});
    expect(apiCloudAccount.getCloudResource).not.toHaveBeenCalled();
  });

  it('fetches resources on mount with EC2 service params', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='Optimize' />);

    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    const params = apiCloudAccount.getCloudResource.mock.calls[0][0];
    expect(params).toMatchObject({
      account_id: 'acc-1',
      serviceName: 'AmazonEC2',
      type: 'compute-instance',
      limit: 10,
      offset: 0,
      fetchTicket: true,
    });
  });

  it('uses heading prop when provided, empty string when undefined', async () => {
    const { rerender } = render(<CloudAccountOptimize accountId='acc-1' heading='Right Sizing' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Right Sizing'));

    rerender(<CloudAccountOptimize accountId='acc-1' heading={undefined} />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toBeEmptyDOMElement());
  });

  it('renders rows with instance names + EC2 service + static type/cpu/savings', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);

    await waitFor(() => expect(screen.getByText('i-1234')).toBeInTheDocument());
    expect(screen.getByText('i-5678')).toBeInTheDocument();
    expect(screen.getAllByText('EC2').length).toBe(2);
    expect(screen.getAllByText('t3.xlarge').length).toBe(2);
    expect(screen.getAllByText('8.5 vCPU').length).toBe(2);
    expect(screen.getAllByText('8h ago').length).toBe(2);
  });

  it('renders Currency with /mo suffix for savings', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);

    await waitFor(() => expect(screen.getAllByTestId('currency').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('currency')[0].textContent).toMatch(/\/mo$/);
  });

  it('renders headers per RIGHT_SIZING_HEADER constants', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() =>
      expect(screen.getByTestId('headers')).toHaveTextContent('Instance Name|Service Name|Current type|Recommendation type|Savings|Updated at|Action')
    );
  });

  it('renders expandable tab with CPU & Memory Utilization text', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getByTestId('expandable-text')).toHaveTextContent('CPU & Memory Utilization'));
  });

  it('expandable componentFn renders OptimizeUtilization with drilldown data', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getByTestId('row-0-expandable')).toBeInTheDocument());
    expect(screen.getByTestId('optimize-utilization').textContent).toBe('acc-1/i-1234');
  });

  it('opens HelpBee modal when HelpBee menu clicked', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();

    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    expect(screen.getByTestId('helpbee-modal')).toBeInTheDocument();
  });

  it('closes HelpBee modal on close', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    fireEvent.click(screen.getByTestId('helpbee-close'));

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('Create Ticket menu does not open HelpBee', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('renders RecommendationJobDetails with krr_scan job', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getByTestId('job-details')).toHaveTextContent('krr_scan'));
  });

  it('enables date-time range filter', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getByTestId('date-range-enabled')).toBeInTheDocument());
  });

  it('paginates and updates offset on next page', async () => {
    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    apiCloudAccount.getCloudResource.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    expect(apiCloudAccount.getCloudResource.mock.calls[0][0].offset).toBe(10);
  });

  it('shows loading state during fetch', async () => {
    let resolveFn: any;
    apiCloudAccount.getCloudResource.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudAccountOptimize accountId='acc-1' heading='' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles fetch rejection without crashing (loading clears)', async () => {
    apiCloudAccount.getCloudResource.mockRejectedValue(new Error('boom'));

    render(<CloudAccountOptimize accountId='acc-1' heading='' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });
});
