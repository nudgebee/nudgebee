import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    getDistinctTagKeys: jest.fn(),
    getDistinctTagValues: jest.fn(),
    getCloudResource: jest.fn(),
  },
}));

let nextPageVal = 0;
let pageSizeVal = 10;
const mockSetPage = jest.fn((p) => {
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

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{String(value || '—')}</span>,
}));

jest.mock('@components1/cloudaccount/common', () => ({
  DataBlock: ({ title, data }: any) => <div data-testid={`datablock-${title}`}>{data}</div>,
}));

jest.mock('@components1/cloudaccount/TagsCell', () => ({
  __esModule: true,
  default: ({ tags }: any) => <div data-testid='tags-cell'>{JSON.stringify(tags)}</div>,
}));

jest.mock('@components1/cloudaccount/CloudAccountEvents', () => ({
  __esModule: true,
  default: ({ accountId, serviceName, subjectName }: any) => (
    <div data-testid='cloud-account-events'>
      {accountId}/{serviceName}/{subjectName}
    </div>
  ),
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, filterOptions = [] }: any) => (
    <div data-testid='box-layout'>
      {filterOptions.map((f: any, i: number) => {
        if (f.type === 'input') {
          return (
            <input
              key={i}
              data-testid={`search-${f.label}`}
              value={f.value || ''}
              onChange={f.onSelect}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && f.onEnter) f.onEnter();
              }}
            />
          );
        }
        if (f.type === 'dropdown') {
          return (
            <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} disabled={f.enabled === false} onChange={f.onSelect}>
              <option value=''>--</option>
              {(f.options || []).map((opt: any, idx: number) => {
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
        }
        return null;
      })}
      {children}
    </div>
  ),
}));

jest.mock('@components1/cloudaccount/CloudAccountTable', () => ({
  __esModule: true,
  default: ({ id, headers, data, totalRows, loading, pageNumber, onPageChange, expandable }: any) => (
    <div data-testid='cloud-account-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{(headers || []).join('|')}</div>
      <div data-testid='expandable-tabs'>{(expandable?.tabs || []).map((t: any) => t.text).join('|')}</div>
      {(data || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
          {/* Render the first expandable tab content for row 0 to exercise the componentFn */}
          {i === 0 && expandable?.tabs?.[0] && (
            <div data-testid={`row-${i}-detail-tab`}>{expandable.tabs[0].componentFn({}, row[0]?.drilldownQuery)}</div>
          )}
          {i === 0 && expandable?.tabs?.[1] && (
            <div data-testid={`row-${i}-events-tab`}>{expandable.tabs[1].componentFn({}, row[0]?.drilldownQuery)}</div>
          )}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2)}>
        Next
      </button>
    </div>
  ),
}));

import CFResources from '@components1/cloudaccount/cloudfoundry/CFResources';

const apiCloudAccount = require('@api1/cloud-account').default;

const sampleOrgs = [
  {
    resourse_id: 'org-1-guid',
    name: 'team-platform',
    status: 'Active',
    meta: JSON.stringify({ suspended: false }),
    tags: JSON.stringify({ env: 'prod' }),
    resourse_created_on: '2026-05-01T10:00:00Z',
  },
];

const sampleSpaces = [
  {
    resourse_id: 'space-1-guid',
    name: 'production',
    status: 'Active',
    meta: JSON.stringify({ org_name: 'team-platform' }),
    tags: JSON.stringify({ org: 'team-platform' }),
    resourse_created_on: '2026-05-01T10:00:00Z',
  },
];

const sampleRoutes = [
  {
    resourse_id: 'route-1-guid',
    name: 'api.example.com',
    status: 'Active',
    meta: JSON.stringify({
      url: 'https://api.example.com/v1',
      host: 'api.example.com',
      protocol: 'https',
      destination_apps: ['app-guid-1'],
    }),
    tags: JSON.stringify({ space: 'production' }),
    resourse_created_on: '2026-05-01T10:00:00Z',
  },
];

const sampleApps = [
  {
    resourse_id: 'app-guid-1',
    name: 'web-api',
    meta: JSON.stringify({ memory_in_mb: 256, instances: 2 }),
    tags: JSON.stringify({ org: 'team-platform', space: 'production' }),
  },
  {
    resourse_id: 'app-guid-2',
    name: 'worker',
    meta: JSON.stringify({ memory_in_mb: 128, instances: 1 }),
    tags: JSON.stringify({ org: 'team-platform', space: 'production' }),
  },
];

const mockResourceResponse = (items: any[], count?: number) => ({
  data: {
    data: {
      cloud_resourses: items,
      cloud_resourses_aggregate: { aggregate: { count: count ?? items.length } },
    },
  },
});

describe('CFResources (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    nextPageVal = 0;
    pageSizeVal = 10;
    apiCloudAccount.getDistinctTagKeys.mockResolvedValue([
      { label: 'env', value: 'env' },
      { label: 'team', value: 'team' },
    ]);
    apiCloudAccount.getDistinctTagValues.mockResolvedValue([
      { label: 'prod', value: 'prod' },
      { label: 'dev', value: 'dev' },
    ]);
    // Default: app enrichment fetch + resource list fetch
    apiCloudAccount.getCloudResource.mockImplementation((params: any) => {
      if (params.serviceName === 'apps') {
        return Promise.resolve(mockResourceResponse(sampleApps));
      }
      if (params.serviceName === 'organizations') {
        return Promise.resolve(mockResourceResponse(sampleOrgs));
      }
      if (params.serviceName === 'spaces') {
        return Promise.resolve(mockResourceResponse(sampleSpaces));
      }
      if (params.serviceName === 'routes') {
        return Promise.resolve(mockResourceResponse(sampleRoutes));
      }
      return Promise.resolve(mockResourceResponse([]));
    });
  });

  it('does not fetch when accountId missing', async () => {
    render(<CFResources accountId='' serviceName='organizations' />);
    await act(async () => {});
    expect(apiCloudAccount.getDistinctTagKeys).not.toHaveBeenCalled();
    expect(apiCloudAccount.getCloudResource).not.toHaveBeenCalled();
  });

  it('fetches tag keys + app enrichment + org list on mount with organizations service', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);

    await waitFor(() => expect(apiCloudAccount.getDistinctTagKeys).toHaveBeenCalledWith('acc-1', 'organizations', []));
    await waitFor(() => {
      const orgCalls = apiCloudAccount.getCloudResource.mock.calls.filter((c: any[]) => c[0].serviceName === 'organizations');
      expect(orgCalls.length).toBeGreaterThan(0);
    });
    const appsCalls = apiCloudAccount.getCloudResource.mock.calls.filter((c: any[]) => c[0].serviceName === 'apps');
    expect(appsCalls.length).toBeGreaterThan(0);
  });

  it('renders organizations headers', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Name|Apps|Instances|Memory|Status|Created At|'));
  });

  it('renders spaces headers when serviceName=spaces', async () => {
    render(<CFResources accountId='acc-1' serviceName='spaces' />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Name|Organization|Apps|Memory|Status|Created At|'));
  });

  it('renders routes headers when serviceName=routes', async () => {
    render(<CFResources accountId='acc-1' serviceName='routes' />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('URL / Host|Space|Protocol|Destinations|Status|Created At|'));
  });

  it('renders org row with enrichment counts from apps', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);

    await waitFor(() => expect(screen.getAllByText('team-platform').length).toBeGreaterThan(0));
    // org has 2 apps (web-api + worker), 3 total instances (2 + 1), 256*2 + 128*1 = 640 MB
    await waitFor(() => expect(screen.getAllByText('2').length).toBeGreaterThan(0));
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('640 MB')).toBeInTheDocument();
  });

  it('renders Active status badge correctly', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getAllByText('Active').length).toBeGreaterThan(0));
  });

  it('renders space row with parent org from tags', async () => {
    render(<CFResources accountId='acc-1' serviceName='spaces' />);

    await waitFor(() => expect(screen.getAllByText('production').length).toBeGreaterThan(0));
    expect(screen.getAllByText('team-platform').length).toBeGreaterThan(0);
  });

  it('renders route row with URL + protocol + destination chip resolving app name', async () => {
    render(<CFResources accountId='acc-1' serviceName='routes' />);

    await waitFor(() => expect(screen.getAllByText('https://api.example.com/v1').length).toBeGreaterThan(0));
    expect(screen.getAllByText('https').length).toBeGreaterThan(0);
    // destination_apps: ['app-guid-1'] → resolves via appNames map → 'web-api'
    await waitFor(() => expect(screen.getByText('web-api')).toBeInTheDocument());
  });

  it('renders Details + Events expandable tabs', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('expandable-tabs')).toHaveTextContent('Details|Events'));
  });

  it('expandable Details tab renders OrgDetails DataBlocks', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('row-0-detail-tab')).toBeInTheDocument());
    expect(screen.getByTestId('datablock-Name')).toHaveTextContent('team-platform');
    expect(screen.getByTestId('datablock-GUID')).toHaveTextContent('org-1-guid');
    expect(screen.getByTestId('datablock-Status')).toHaveTextContent('Active');
  });

  it('expandable Events tab passes correct subjectName to CloudAccountEvents', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('row-0-events-tab')).toBeInTheDocument());
    expect(screen.getByTestId('cloud-account-events').textContent).toBe('acc-1/organizations/team-platform');
  });

  it('refetches when tag key changes (and loads tag values)', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('filter-Tag Key')).toBeInTheDocument());

    apiCloudAccount.getCloudResource.mockClear();
    fireEvent.change(screen.getByTestId('filter-Tag Key'), { target: { value: 'env' } });

    await waitFor(() => expect(apiCloudAccount.getDistinctTagValues).toHaveBeenCalledWith('acc-1', 'env', 'organizations', []));
    await waitFor(() => {
      const call = apiCloudAccount.getCloudResource.mock.calls.find(
        (c: any[]) => c[0].serviceName === 'organizations' && c[0].tagFilterKey === 'env'
      );
      expect(call).toBeTruthy();
    });
  });

  it('disables Tag Value dropdown when no Tag Key selected', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('filter-Tag Value')).toBeInTheDocument());
    expect(screen.getByTestId('filter-Tag Value')).toBeDisabled();
  });

  it('search input fires onEnter to trigger fetch when page is 0', async () => {
    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('search-Search By Org Name')).toBeInTheDocument());

    apiCloudAccount.getCloudResource.mockClear();
    const input = screen.getByTestId('search-Search By Org Name');
    fireEvent.change(input, { target: { value: 'team' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => {
      const orgCalls = apiCloudAccount.getCloudResource.mock.calls.filter((c: any[]) => c[0].serviceName === 'organizations');
      expect(orgCalls.length).toBeGreaterThan(0);
    });
  });

  it('uses correct search label per resource type', async () => {
    const { rerender } = render(<CFResources accountId='acc-1' serviceName='spaces' />);
    await waitFor(() => expect(screen.getByTestId('search-Search By Space Name')).toBeInTheDocument());

    rerender(<CFResources accountId='acc-1' serviceName='routes' />);
    await waitFor(() => expect(screen.getByTestId('search-Search By Route URL')).toBeInTheDocument());
  });

  it('shows loading state during fetch', async () => {
    let resolveFn: any;
    apiCloudAccount.getCloudResource.mockImplementation((params: any) => {
      if (params.serviceName === 'apps') return Promise.resolve(mockResourceResponse(sampleApps));
      return new Promise((r) => {
        resolveFn = r;
      });
    });

    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockResourceResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles fetch rejection without crashing (loading clears)', async () => {
    apiCloudAccount.getCloudResource.mockImplementation((params: any) => {
      if (params.serviceName === 'apps') return Promise.resolve(mockResourceResponse(sampleApps));
      return Promise.reject(new Error('boom'));
    });
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});

    render(<CFResources accountId='acc-1' serviceName='organizations' />);
    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
    errorSpy.mockRestore();
  });

  it('defaults to organizations resource type when serviceName missing', async () => {
    render(<CFResources accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Name|Apps|Instances|Memory|Status|Created At|'));
  });
});
