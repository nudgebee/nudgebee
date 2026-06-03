import React from 'react';
import { render, screen, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listAppSecurityRecommendation: jest.fn(),
    getSecuritySeverityGrouping: jest.fn(),
  },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text }) => <span data-testid={`label-${text}`}>{text}</span>,
}));

jest.mock('@components1/k8s/common/SeverityInfographic', () => ({
  __esModule: true,
  default: ({ severityData }) => (
    <div data-testid='severity-infographic'>
      {(severityData || []).map((s) => (
        <span key={s.label} data-testid={`severity-${s.label}`}>
          {s.label}:{s.value}
        </span>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/InfographicList', () => ({
  __esModule: true,
  default: ({ sequence }) => (
    <div data-testid='infographic-list'>
      {(sequence || []).map((s) => (
        <span key={s.text} data-testid={`count-${s.text}`}>
          {s.text}:{s.value}
        </span>
      ))}
    </div>
  ),
}));

jest.mock('./../../../../src/components1/recommendations/security/KubernetesSecurityDetails', () => ({
  __esModule: true,
  default: ({ workload_name, query }) => (
    <div data-testid='security-details'>
      {workload_name}|ns:{query?.namespace || ''}|st:{query?.status || ''}
    </div>
  ),
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ id, tableData, headers, loading, expandable }) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='headers'>{headers.map((h, idx) => (typeof h === 'string' ? h : h.name || `col-${idx}`)).join('|')}</div>
      <div data-testid='header-components'>
        {headers.map((h, idx) => (typeof h !== 'string' && h.component ? <span key={idx}>{h.component}</span> : null))}
      </div>
      <div data-testid='expandable-tabs'>{(expandable?.tabs || []).map((t) => t.text).join('|')}</div>
      {(tableData || []).map((row, i) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell, j) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
          {i === 0 && expandable?.tabs?.[0] && (
            <div data-testid={`row-${i}-details`}>{expandable.tabs[0].componentFn({}, row[0]?.drilldownQuery)}</div>
          )}
        </div>
      ))}
    </div>
  ),
}));

import KubernetesSecurityApps from '@components1/recommendations/security/KubernetesSecurityApps';

const apiRecommendations = require('@api1/recommendation').default;

const sampleApps = [
  {
    workload_name: 'web-api',
    namespace: 'prod',
    count_image: 3,
    count_severity_critical: 2,
    count_severity_high: 5,
    count_severity_medium: 10,
    count_severity_low: 3,
  },
  {
    workload_name: 'worker',
    namespace: 'workers',
    count_image: 1,
    count_severity_critical: 0,
    count_severity_high: 1,
    count_severity_medium: 4,
    count_severity_low: 2,
  },
];

const sampleSeverity = {
  recommendation_security_groupings_v2: {
    rows: [
      {
        count_severity_critical: 4,
        count_severity_high: 12,
        count_severity_medium: 30,
        count_severity_low: 8,
        count_image: 5,
        count_workload_name: 7,
      },
    ],
  },
};

describe('KubernetesSecurityApps (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiRecommendations.listAppSecurityRecommendation.mockResolvedValue({
      recommendation_security_groupings_v2: { rows: sampleApps },
    });
    apiRecommendations.getSecuritySeverityGrouping.mockResolvedValue(sampleSeverity);
  });

  it('fetches app security list + severity grouping on mount', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='apps-table' />);

    await waitFor(() => {
      expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalledWith('acc-1', { status: 'Open' });
      expect(apiRecommendations.getSecuritySeverityGrouping).toHaveBeenCalled();
    });
  });

  it('skips severity infographic fetch when disableInfographic is set', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='apps-table' disableInfographic />);

    await waitFor(() => expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.getSecuritySeverityGrouping).not.toHaveBeenCalled();
    expect(screen.queryByTestId('severity-infographic')).not.toBeInTheDocument();
    expect(screen.queryByTestId('infographic-list')).not.toBeInTheDocument();
  });

  it('passes workload + namespace + severity to API query', async () => {
    render(
      <KubernetesSecurityApps
        kubernetes={{ id: 'acc-1' }}
        query={{ workload_name: 'web', namespace: 'prod', severity: 'Critical', status: 'Open' }}
        tableId='apps-table'
      />
    );

    await waitFor(() => expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalledWith('acc-1', {
      workload_name: 'web',
      namespace: 'prod',
      severity: 'Critical',
      status: 'Open',
    });
    expect(apiRecommendations.getSecuritySeverityGrouping).toHaveBeenCalledWith({
      accountId: 'acc-1',
      workload: 'web',
      namespace: 'prod',
      severity: 'Critical',
      status: 'Open',
    });
  });

  it('renders rows with workload name + namespace + severity counts', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);

    await waitFor(() => expect(screen.getByText('web-api')).toBeInTheDocument());
    expect(screen.getByText('worker')).toBeInTheDocument();
    expect(screen.getByText('ns: prod')).toBeInTheDocument();
    expect(screen.getByText('ns: workers')).toBeInTheDocument();
  });

  it('renders headers: Application, Images count + 4 severity columns', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);

    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Application|Images count'));
    expect(screen.getAllByTestId('label-Critical').length).toBeGreaterThan(0);
    expect(screen.getAllByTestId('label-High').length).toBeGreaterThan(0);
    expect(screen.getAllByTestId('label-Medium').length).toBeGreaterThan(0);
    expect(screen.getAllByTestId('label-Low').length).toBeGreaterThan(0);
  });

  it('renders severity infographic with counts from severity API', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);

    await waitFor(() => expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:4'));
    expect(screen.getByTestId('severity-High')).toHaveTextContent('High:12');
    expect(screen.getByTestId('severity-Medium')).toHaveTextContent('Medium:30');
    expect(screen.getByTestId('severity-Low')).toHaveTextContent('Low:8');
  });

  it('renders Images + Apps counts from severity API', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);

    await waitFor(() => expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:5'));
    expect(screen.getByTestId('count-Apps')).toHaveTextContent('Apps:7');
  });

  it('renders expandable Details tab with KubernetesSecurityDetails carrying drilldown data', async () => {
    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{ status: 'Closed', severity: 'High' }} tableId='apps-table' />);

    await waitFor(() => expect(screen.getByTestId('expandable-tabs')).toHaveTextContent('Details'));
    expect(screen.getByTestId('row-0-details')).toBeInTheDocument();
    expect(screen.getByTestId('security-details').textContent).toBe('web-api|ns:prod|st:Closed');
  });

  it('refetches when query prop changes', async () => {
    const { rerender } = render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='apps-table' />);
    await waitFor(() => expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalled());
    apiRecommendations.listAppSecurityRecommendation.mockClear();

    rerender(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{ status: 'Closed' }} tableId='apps-table' />);

    await waitFor(() => expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalledWith('acc-1', { status: 'Closed' }));
  });

  it('refetches when kubernetes.id changes', async () => {
    const { rerender } = render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);
    await waitFor(() => expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalled());
    apiRecommendations.listAppSecurityRecommendation.mockClear();

    rerender(<KubernetesSecurityApps kubernetes={{ id: 'acc-2' }} query={{}} tableId='apps-table' />);

    await waitFor(() => expect(apiRecommendations.listAppSecurityRecommendation).toHaveBeenCalledWith('acc-2', {}));
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    apiRecommendations.listAppSecurityRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn({ recommendation_security_groupings_v2: { rows: [] } });
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('shows initial dash placeholders before severity loads', async () => {
    let resolveFn;
    apiRecommendations.getSecuritySeverityGrouping.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesSecurityApps kubernetes={{ id: 'acc-1' }} query={{}} tableId='apps-table' />);

    expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:-');
    expect(screen.getByTestId('count-Apps')).toHaveTextContent('Apps:-');
    expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:-');

    await act(async () => {
      resolveFn(sampleSeverity);
    });

    await waitFor(() => expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:5'));
  });
});
