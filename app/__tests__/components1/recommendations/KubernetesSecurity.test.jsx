import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockRouterReplace = jest.fn();
let mockRouterQuery = {};
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: mockRouterReplace,
    query: mockRouterQuery,
    pathname: '/k8s/security',
    asPath: '/k8s/security',
    route: '/k8s/security',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('@lib/router', () => ({
  applyFiltersOnRouter: jest.fn(),
}));

jest.mock('@utils/common', () => ({
  syncFilterFromQuery: (options, query) => {
    if (!query) return [];
    const vals = String(query).split(',');
    return (options || []).filter((o) => vals.includes(o.value || o));
  },
}));

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listRecommendationNamesapces: jest.fn(),
    listRecommendationWorkloads: jest.fn(),
  },
  RECOMMENDATION_STATUS: [
    { label: 'Open', value: 'Open' },
    { label: 'Closed', value: 'Closed' },
  ],
  RECOMMENDATION_SERVERITY: [
    { label: 'Critical', value: 'critical' },
    { label: 'High', value: 'high' },
  ],
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

const lastChildProps = { Apps: null, Details: null, Images: null, CVE: null };

jest.mock('./../../../src/components1/recommendations/security/KubernetesSecurityApps', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.Apps = props;
    return <div data-testid='child-Apps'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('./../../../src/components1/recommendations/security/KubernetesSecurityImages', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.Images = props;
    return <div data-testid='child-Images'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('./../../../src/components1/recommendations/security/KubernetesSecurityCVE', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.CVE = props;
    return <div data-testid='child-CVE'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('./../../../src/components1/recommendations/security/KubernetesSecurityDetails', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.Details = props;
    return <div data-testid='child-Details'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('@common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, heading, filterOptions = [], toggleButtons }) => (
    <div data-testid='box-layout'>
      <h2 data-testid='box-heading'>{heading}</h2>
      <div data-testid='toggle-buttons'>
        {(toggleButtons?.options || []).map((opt) => (
          <button key={opt.id} data-testid={`toggle-${opt.id}`} onClick={() => toggleButtons.handleSelectToggle({ target: { value: opt.id } })}>
            {opt.text}
          </button>
        ))}
        <div data-testid='active-toggle'>{toggleButtons?.activeButton}</div>
      </div>
      {filterOptions.map((f, i) => {
        if (!f.enabled) return null;
        if (f.type === 'dropdown') {
          return (
            <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={f.onSelect}>
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
          );
        }
        if (f.type === 'multi-dropdown') {
          return (
            <button key={i} data-testid={`multi-${f.label}`} onClick={() => f.onSelect({ target: { value: (f.options || []).slice(0, 1) } })}>
              {f.label}
            </button>
          );
        }
        if (f.type === 'search') {
          return (
            <input
              key={i}
              data-testid={`search-${f.label}`}
              defaultValue={f.value || ''}
              onChange={(e) => f.onSelect(e)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && f.onEnter) f.onEnter();
              }}
            />
          );
        }
        return null;
      })}
      {children}
    </div>
  ),
}));

import KubernetesSecurity from '@components1/recommendations/KubernetesSecurity';

const recommendationApi = require('@api1/recommendation').default;
const { applyFiltersOnRouter } = require('@lib/router');

describe('KubernetesSecurity (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    Object.keys(lastChildProps).forEach((k) => (lastChildProps[k] = null));
    recommendationApi.listRecommendationNamesapces.mockResolvedValue([
      { label: 'prod', value: 'prod' },
      { label: 'kube-system', value: 'kube-system' },
    ]);
    recommendationApi.listRecommendationWorkloads.mockResolvedValue([
      { label: 'web', value: 'web' },
      { label: 'api', value: 'api' },
    ]);
  });

  it('renders Apps child by default with default Open status', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(screen.getByTestId('child-Apps')).toBeInTheDocument());
    expect(screen.queryByTestId('child-Images')).not.toBeInTheDocument();
    expect(screen.queryByTestId('child-CVE')).not.toBeInTheDocument();
    expect(screen.queryByTestId('child-Details')).not.toBeInTheDocument();

    expect(lastChildProps.Apps.kubernetes.id).toBe('acc-1');
    expect(lastChildProps.Apps.query.status).toBe('Open');
    expect(lastChildProps.Apps.query.severity).toBeUndefined();
  });

  it('fetches namespaces on mount with Security category + Open status', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled());
    expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalledWith({
      accountId: 'acc-1',
      category: 'Security',
      status: 'Open',
    });
  });

  it('does not fetch workloads until namespace selected', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled());
    expect(recommendationApi.listRecommendationWorkloads).not.toHaveBeenCalled();
  });

  it('fetches workloads when namespace is selected from router query', async () => {
    mockRouterQuery = { namespace: 'prod' };
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(recommendationApi.listRecommendationWorkloads).toHaveBeenCalled());
    expect(recommendationApi.listRecommendationWorkloads).toHaveBeenCalledWith({
      accountId: 'acc-1',
      category: 'Security',
      status: 'Open',
      namespaceName: 'prod',
    });
  });

  it('initializes severity filter from router query', async () => {
    mockRouterQuery = { severity: 'critical' };
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    await waitFor(() => expect(lastChildProps.Apps).not.toBeNull());
    await waitFor(() => expect(lastChildProps.Apps.query.severity).toEqual([{ label: 'Critical', value: 'critical' }]));
  });

  it('switches to Images child when Images toggle clicked', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('child-Apps')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('toggle-images'));

    expect(screen.getByTestId('child-Images')).toBeInTheDocument();
    expect(screen.queryByTestId('child-Apps')).not.toBeInTheDocument();
    expect(screen.getByTestId('active-toggle')).toHaveTextContent('images');
  });

  it('switches to CVE child when CVE toggle clicked', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    fireEvent.click(screen.getByTestId('toggle-cve'));

    expect(screen.getByTestId('child-CVE')).toBeInTheDocument();
    expect(screen.queryByTestId('child-Apps')).not.toBeInTheDocument();
  });

  it('switches to Details child when Details toggle clicked', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    fireEvent.click(screen.getByTestId('toggle-details'));

    expect(screen.getByTestId('child-Details')).toBeInTheDocument();
  });

  it('shows Image search filter only for Images + Details tabs', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    // Default Apps tab: no image filter
    await waitFor(() => expect(screen.getByTestId('child-Apps')).toBeInTheDocument());
    expect(screen.queryByTestId('search-Image')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('toggle-images'));
    expect(screen.getByTestId('search-Image')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('toggle-details'));
    expect(screen.getByTestId('search-Image')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('toggle-cve'));
    expect(screen.queryByTestId('search-Image')).not.toBeInTheDocument();
  });

  it('propagates status filter change to children', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('child-Apps')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Closed' } });

    await waitFor(() => expect(lastChildProps.Apps.query.status).toBe('Closed'));
  });

  it('refetches namespaces when status filter changes', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled());
    recommendationApi.listRecommendationNamesapces.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Closed' } });

    await waitFor(() => expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled());
    expect(recommendationApi.listRecommendationNamesapces.mock.calls[0][0].status).toBe('Closed');
  });

  it('updates router and resets workload + reloads when namespace changes', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('filter-Namespace')).toBeInTheDocument());

    fireEvent.change(screen.getByTestId('filter-Namespace'), { target: { value: 'prod' } });

    await waitFor(() => expect(applyFiltersOnRouter).toHaveBeenCalledWith(expect.anything(), { namespace: 'prod' }));
    await waitFor(() => expect(recommendationApi.listRecommendationWorkloads).toHaveBeenCalled());
  });

  it('propagates severity multi-dropdown change to children', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await waitFor(() => expect(screen.getByTestId('child-Apps')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('multi-Severity'));

    await waitFor(() => expect(lastChildProps.Apps.query.severity).toEqual([{ label: 'Critical', value: 'critical' }]));
  });

  it('image search Enter propagates to children only on Images/Details tab', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

    fireEvent.click(screen.getByTestId('toggle-images'));
    const input = screen.getByTestId('search-Image');
    fireEvent.change(input, { target: { value: 'nginx:latest' } });
    fireEvent.keyDown(input, { key: 'Enter' });

    await waitFor(() => expect(lastChildProps.Images.query.image).toBe('nginx:latest'));
  });

  it('respects enableFilters prop — hides disabled filters', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} enableFilters={['status', 'severity']} />);
    await waitFor(() => expect(screen.getByTestId('filter-Status')).toBeInTheDocument());
    expect(screen.queryByTestId('filter-Namespace')).not.toBeInTheDocument();
    expect(screen.queryByTestId('filter-Workload')).not.toBeInTheDocument();
  });

  it('uses heading prop when provided, defaults to Security when undefined', async () => {
    const { rerender } = render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} heading='K8s Sec' />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('K8s Sec'));

    rerender(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} heading={undefined} />);
    await waitFor(() => expect(screen.getByTestId('box-heading')).toHaveTextContent('Security'));
  });

  it('initializes activeToggleButton from prop', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} activeToggleButton='cve' />);
    await waitFor(() => expect(screen.getByTestId('child-CVE')).toBeInTheDocument());
    expect(screen.getByTestId('active-toggle')).toHaveTextContent('cve');
  });

  it('initializes selectedWorkload from workload_name prop', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} workload_name='web-api' />);
    await waitFor(() => expect(lastChildProps.Apps).not.toBeNull());
    expect(lastChildProps.Apps.query.workload_name).toBe('web-api');
  });

  it('initializes recommendationImage from filters.image prop', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} filters={{ image: 'nginx' }} activeToggleButton='images' />);
    await waitFor(() => expect(lastChildProps.Images).not.toBeNull());
    expect(lastChildProps.Images.query.image).toBe('nginx');
  });

  it('passes disableInfographic prop to all children', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} disableInfographic />);
    await waitFor(() => expect(lastChildProps.Apps).not.toBeNull());
    expect(lastChildProps.Apps.disableInfographic).toBe(true);

    fireEvent.click(screen.getByTestId('toggle-images'));
    expect(lastChildProps.Images.disableInfographic).toBe(true);
  });

  it('does not fetch namespaces when kubernetes.id is missing', async () => {
    render(<KubernetesSecurity kubernetes={{}} />);
    await act(async () => {});
    expect(recommendationApi.listRecommendationNamesapces).not.toHaveBeenCalled();
  });
});
