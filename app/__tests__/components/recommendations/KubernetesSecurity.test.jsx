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

jest.mock('@utils/colors');

const lastChildProps = { Apps: null, Details: null, Images: null, CVE: null };

jest.mock('@components/recommendations/security/KubernetesSecurityApps', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.Apps = props;
    return <div data-testid='child-Apps'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('@components/recommendations/security/KubernetesSecurityImages', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.Images = props;
    return <div data-testid='child-Images'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('@components/recommendations/security/KubernetesSecurityCVE', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.CVE = props;
    return <div data-testid='child-CVE'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('@components/recommendations/security/KubernetesSecurityDetails', () => ({
  __esModule: true,
  default: (props) => {
    lastChildProps.Details = props;
    return <div data-testid='child-Details'>{JSON.stringify(props.query)}</div>;
  },
}));

jest.mock('@shared/buttons/DownloadButton', () => ({
  __esModule: true,
  default: ({ onClick }) => (
    <button data-testid='download-btn' onClick={onClick}>
      DL
    </button>
  ),
}));

// ds/ToggleGroup replaces the old BoxLayout2.toggleButtons API. Renders buttons
// keyed by option.value (not the old `id`). Tests still query `toggle-{value}`.
jest.mock('@ui/ToggleGroup', () => ({
  __esModule: true,
  ToggleGroup: ({ options = [], onChange, value: activeValue }) => (
    <div data-testid='toggle-buttons'>
      {options.map((opt) => (
        <button key={opt.value} data-testid={`toggle-${opt.value}`} onClick={() => onChange?.(opt.value)}>
          {opt.label}
        </button>
      ))}
      <div data-testid='active-toggle'>{activeValue}</div>
    </div>
  ),
}));

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout = ({ children, id }) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout };
});

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect, multiple }) => {
    if (multiple) {
      // Multi-select: severity in source expects `e.target.value` to be the array.
      return (
        <button data-testid={`multi-${label}`} onClick={() => onSelect?.({ target: { value: (options || []).slice(0, 1) } })}>
          {label}
        </button>
      );
    }
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

jest.mock('@ui/SearchInput', () => ({
  __esModule: true,
  default: ({ label, value, onChange, onEnterPress }) => (
    <input
      data-testid={`search-${label}`}
      defaultValue={value || ''}
      onChange={(e) => onChange?.(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === 'Enter' && onEnterPress) onEnterPress();
      }}
    />
  ),
}));

import KubernetesSecurity from '@components/recommendations/KubernetesSecurity';

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

  // Flush pending mount-effect fetches (namespaces / workloads) so their
  // setState calls don't leak past the test boundary. Run multiple flushes
  // because the chain is: API resolve → setState → effect re-run → another
  // resolve.
  afterEach(async () => {
    for (let i = 0; i < 5; i++) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
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
    await act(async () => {});

    fireEvent.click(screen.getByTestId('toggle-cve'));

    expect(screen.getByTestId('child-CVE')).toBeInTheDocument();
    expect(screen.queryByTestId('child-Apps')).not.toBeInTheDocument();
  });

  it('switches to Details child when Details toggle clicked', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);
    await act(async () => {});

    fireEvent.click(screen.getByTestId('toggle-details'));

    expect(screen.getByTestId('child-Details')).toBeInTheDocument();
  });

  it('shows Image search filter only for Images + Details tabs', async () => {
    render(<KubernetesSecurity kubernetes={{ id: 'acc-1' }} />);

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
    // Wait for mount-effect namespace fetch to settle before firing further events,
    // otherwise the late setNamespaces leaks past the test boundary.
    await waitFor(() => expect(recommendationApi.listRecommendationNamesapces).toHaveBeenCalled());

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
