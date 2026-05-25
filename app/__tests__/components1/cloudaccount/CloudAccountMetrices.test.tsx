import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    getCloudResource: jest.fn(),
  },
}));

const mockUseMetricCloudFilter = jest.fn();
jest.mock('@hooks/useCloudFilters', () => ({
  useMetricCloudFilter: (...args: any[]) => mockUseMetricCloudFilter(...args),
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

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s: string) =>
    String(s || '')
      .toLowerCase()
      .replace(/_./g, (m) => ' ' + m[1].toUpperCase())
      .replace(/^./, (c) => c.toUpperCase()),
}));

jest.mock('@components1/cloudaccount/ec2/Instances', () => ({
  CustomText: ({ text1, text2 }: any) => (
    <span data-testid='custom-text'>
      {text1}
      {text2 ? ` ${text2}` : ''}
    </span>
  ),
}));

jest.mock('@components1/cloudaccount/ec2/Summary', () => ({
  __esModule: true,
  default: ({ accountId, resourceId, serviceName }: any) => (
    <div data-testid='optimize-summary'>
      {accountId}/{resourceId}/{serviceName}
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
        <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={f.onSelect}>
          <option value=''>--</option>
          {(f.options || []).map((opt: any) => (
            <option key={opt.value || opt.label} value={opt.value || opt.label}>
              {opt.label}
            </option>
          ))}
        </select>
      ))}
      <div data-testid='date-range-enabled'>{String(!!dateTimeRange?.enabled)}</div>
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

import CloudAccountMetrices from '@components1/cloudaccount/CloudAccountMetrices';

const apiCloudAccount = require('@api1/cloud-account').default;

const sampleResources = [
  {
    name: 'i-1234',
    resourse_id: 'i-1234-id',
    resourceId: 'i-1234-id',
    type: 'compute_instance',
  },
  {
    name: 'i-5678',
    resourse_id: 'i-5678-id',
    resourceId: 'i-5678-id',
    type: 'rds_db',
  },
];

const mockResponse = (items = sampleResources, count?: number) => ({
  data: { data: { cloud_resourses: items, cloud_resourses_aggregate: { aggregate: { count: count ?? items.length } } } },
});

describe('CloudAccountMetrices (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockUseMetricCloudFilter.mockReturnValue({
      serviceNamesFilter: [
        { label: 'EC2', value: 'AmazonEC2' },
        { label: 'RDS', value: 'AmazonRDS' },
      ],
      severityFilterType: [
        { label: 'High', value: 'high' },
        { label: 'Medium', value: 'medium' },
      ],
    });
    apiCloudAccount.getCloudResource.mockResolvedValue(mockResponse());
  });

  it('fetches resources on mount with accountId + serviceName', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='Metrics' />);

    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    const params = apiCloudAccount.getCloudResource.mock.calls[0][0];
    expect(params).toMatchObject({
      account_id: 'acc-1',
      serviceName: 'AmazonEC2',
      limit: 10,
      offset: 0,
      fetchTicket: true,
    });
  });

  it('uses heading prop when provided, empty string fallback', async () => {
    const { rerender } = render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='Right Sizing' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Right Sizing'));

    rerender(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading={undefined} />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toBeEmptyDOMElement());
  });

  it('renders rows with instance names + snake-to-title-cased type', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);

    await waitFor(() => expect(screen.getByText('i-1234')).toBeInTheDocument());
    expect(screen.getByText('i-5678')).toBeInTheDocument();
    // type: 'compute_instance' → 'Compute Instance'
    expect(screen.getByText('Compute Instance')).toBeInTheDocument();
    expect(screen.getByText('Rds Db')).toBeInTheDocument();
  });

  it('renders Currency with /mo suffix', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);

    await waitFor(() => expect(screen.getAllByTestId('currency').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('currency')[0].textContent).toMatch(/\/mo$/);
  });

  it('renders RIGHT_SIZING_HEADER columns', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() =>
      expect(screen.getByTestId('headers')).toHaveTextContent('Instance Name|Service Name|Current type|Recommendation type|Savings|Updated at|Action')
    );
  });

  it('populates service + severity filters from useMetricCloudFilter', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);

    await waitFor(() => expect(screen.getByTestId('filter-Service Name')).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'EC2' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'High' })).toBeInTheDocument();
  });

  it('refetches and resets page when service name filter changes', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    apiCloudAccount.getCloudResource.mockClear();

    fireEvent.change(screen.getByTestId('filter-Service Name'), { target: { value: 'AmazonRDS' } });

    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    expect(apiCloudAccount.getCloudResource.mock.calls[0][0].offset).toBe(0);
  });

  it('refetches when severity filter changes', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    apiCloudAccount.getCloudResource.mockClear();

    fireEvent.change(screen.getByTestId('filter-Severity'), { target: { value: 'high' } });

    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
  });

  it('paginates and updates offset on next page', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    apiCloudAccount.getCloudResource.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiCloudAccount.getCloudResource).toHaveBeenCalled());
    expect(apiCloudAccount.getCloudResource.mock.calls[0][0].offset).toBe(10);
  });

  it('opens HelpBee modal on HelpBee menu click', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    expect(screen.getByTestId('helpbee-modal')).toBeInTheDocument();
  });

  it('closes HelpBee modal on close', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-HelpBee').length).toBeGreaterThan(0));
    fireEvent.click(screen.getAllByTestId('menu-HelpBee')[0]);

    fireEvent.click(screen.getByTestId('helpbee-close'));

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('Create Ticket menu does not open HelpBee', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(screen.getAllByTestId('menu-Create Ticket').length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId('menu-Create Ticket')[0]);

    expect(screen.queryByTestId('helpbee-modal')).not.toBeInTheDocument();
  });

  it('expandable componentFn renders OptimizeSummary with AmazonEC2 hardcoded serviceName', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonRDS' heading='' />);
    await waitFor(() => expect(screen.getByTestId('row-0-expandable')).toBeInTheDocument());
    // serviceName is hardcoded to 'AmazonEC2' in the componentFn regardless of prop
    expect(screen.getByTestId('optimize-summary').textContent).toBe('acc-1/i-1234-id/AmazonEC2');
  });

  it('renders RecommendationJobDetails with krr_scan job', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(screen.getByTestId('job-details')).toHaveTextContent('krr_scan'));
  });

  it('disables date-time range filter', async () => {
    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(screen.getByTestId('date-range-enabled')).toHaveTextContent('false'));
  });

  it('passes accountId through to useMetricCloudFilter hook', async () => {
    render(<CloudAccountMetrices accountId='acc-42' serviceName='AmazonEC2' heading='' />);
    expect(mockUseMetricCloudFilter).toHaveBeenCalledWith('acc-42');
  });

  it('shows loading during fetch and clears after', async () => {
    let resolveFn: any;
    apiCloudAccount.getCloudResource.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles fetch rejection without crashing', async () => {
    apiCloudAccount.getCloudResource.mockRejectedValue(new Error('boom'));

    render(<CloudAccountMetrices accountId='acc-1' serviceName='AmazonEC2' heading='' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });
});
