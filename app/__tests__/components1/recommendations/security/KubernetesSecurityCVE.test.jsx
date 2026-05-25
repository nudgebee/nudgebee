import React from 'react';
import { render, screen, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listCVESecurityRecommendation: jest.fn(),
    getSecuritySeverityGrouping: jest.fn(),
  },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
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
  default: ({ query }) => <div data-testid='security-details'>{query?.vulnerabilityId || ''}</div>,
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ id, tableData, headers, loading, expandable }) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='headers'>{headers.join('|')}</div>
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

import KubernetesSecurityCVE from '@components1/recommendations/security/KubernetesSecurityCVE';

const apiRecommendations = require('@api1/recommendation').default;

const sampleCVEs = [
  {
    vulnerability_id: 'CVE-2024-1234',
    count_image: 3,
    count_workload_name: 5,
    count: 12,
    severity: 'Critical',
  },
  {
    vulnerability_id: 'CVE-2024-5678',
    count_image: 1,
    count_workload_name: 2,
    count: 4,
    severity: 'High',
  },
];

const sampleSeverityGrouping = {
  recommendation_security_groupings_v2: {
    rows: [
      {
        count_severity_critical: 5,
        count_severity_high: 10,
        count_severity_medium: 20,
        count_severity_low: 15,
        count_vulnerability_id: 50,
        count_image: 8,
      },
    ],
  },
};

describe('KubernetesSecurityCVE (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiRecommendations.listCVESecurityRecommendation.mockResolvedValue({
      recommendation_security_groupings_v2: { rows: sampleCVEs },
    });
    apiRecommendations.getSecuritySeverityGrouping.mockResolvedValue(sampleSeverityGrouping);
  });

  it('fetches CVE list + severity grouping on mount', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' />);

    await waitFor(() => {
      expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalledWith('acc-1', { status: 'Open' });
      expect(apiRecommendations.getSecuritySeverityGrouping).toHaveBeenCalled();
    });
  });

  it('skips severity infographic fetch when disableInfographic prop set', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' disableInfographic />);

    await waitFor(() => expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.getSecuritySeverityGrouping).not.toHaveBeenCalled();
    expect(screen.queryByTestId('severity-infographic')).not.toBeInTheDocument();
    expect(screen.queryByTestId('infographic-list')).not.toBeInTheDocument();
  });

  it('passes namespace + workload + severity to API query', async () => {
    render(
      <KubernetesSecurityCVE
        kubernetes={{ id: 'acc-1' }}
        query={{ workload_name: 'web', namespace: 'prod', severity: 'Critical', status: 'Open' }}
        tableId='cve-table'
      />
    );

    await waitFor(() => expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalledWith('acc-1', {
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

  it('renders CVE table rows with vulnerability_id + counts + severity', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' />);

    // CVE id appears in both row cell AND expandable detail tab (test stub) — use getAllByText
    await waitFor(() => expect(screen.getAllByText('CVE-2024-1234').length).toBeGreaterThan(0));
    expect(screen.getByText('CVE-2024-5678')).toBeInTheDocument();
    expect(screen.getByTestId('label-Critical')).toBeInTheDocument();
    expect(screen.getByTestId('label-High')).toBeInTheDocument();
  });

  it('renders severity infographic with counts from API', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' />);

    await waitFor(() => expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:5'));
    expect(screen.getByTestId('severity-High')).toHaveTextContent('High:10');
    expect(screen.getByTestId('severity-Medium')).toHaveTextContent('Medium:20');
    expect(screen.getByTestId('severity-Low')).toHaveTextContent('Low:15');
  });

  it('renders CVE + Images counts from API', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' />);

    await waitFor(() => expect(screen.getByTestId('count-CVE')).toHaveTextContent('CVE:50'));
    expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:8');
  });

  it('renders correct table headers', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{}} tableId='cve-table' />);

    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('CVE|Images|Applications|Count|Severity'));
  });

  it('renders expandable Details tab with KubernetesSecurityDetails', async () => {
    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' />);

    await waitFor(() => expect(screen.getByTestId('expandable-tabs')).toHaveTextContent('Details'));
    expect(screen.getByTestId('row-0-details')).toBeInTheDocument();
    expect(screen.getByTestId('security-details')).toHaveTextContent('CVE-2024-1234');
  });

  it('refetches when query prop changes', async () => {
    const { rerender } = render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='cve-table' />);
    await waitFor(() => expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalled());
    apiRecommendations.listCVESecurityRecommendation.mockClear();

    rerender(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{ status: 'Closed' }} tableId='cve-table' />);

    await waitFor(() => expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalledWith('acc-1', { status: 'Closed' });
  });

  it('refetches when kubernetes.id changes', async () => {
    const { rerender } = render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{}} tableId='cve-table' />);
    await waitFor(() => expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalled());
    apiRecommendations.listCVESecurityRecommendation.mockClear();

    rerender(<KubernetesSecurityCVE kubernetes={{ id: 'acc-2' }} query={{}} tableId='cve-table' />);

    await waitFor(() => expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.listCVESecurityRecommendation).toHaveBeenCalledWith('acc-2', {});
  });

  it('shows loading state during fetch', async () => {
    let resolveFn;
    apiRecommendations.listCVESecurityRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{}} tableId='cve-table' />);

    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn({ recommendation_security_groupings_v2: { rows: [] } });
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles fetch rejection without crashing (loading clears)', async () => {
    apiRecommendations.listCVESecurityRecommendation.mockRejectedValue(new Error('boom'));

    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{}} tableId='cve-table' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles severity fetch rejection without crashing', async () => {
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    apiRecommendations.getSecuritySeverityGrouping.mockRejectedValue(new Error('boom'));

    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{}} tableId='cve-table' />);

    // Default initial dashes still shown
    await waitFor(() => expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:-'));
    errorSpy.mockRestore();
  });

  it('shows initial dash placeholders before severity loads', async () => {
    let resolveFn;
    apiRecommendations.getSecuritySeverityGrouping.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesSecurityCVE kubernetes={{ id: 'acc-1' }} query={{}} tableId='cve-table' />);

    expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:-');
    expect(screen.getByTestId('count-Apps')).toHaveTextContent('Apps:-');
    expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:-');

    await act(async () => {
      resolveFn(sampleSeverityGrouping);
    });

    await waitFor(() => expect(screen.getByTestId('count-CVE')).toHaveTextContent('CVE:50'));
  });
});
