import React from 'react';
import { render, screen, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listImageSecurityRecommendation: jest.fn(),
    getSecuritySeverityGrouping: jest.fn(),
  },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='datetime'>{String(value || '—')}</span>,
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
  default: ({ query }) => (
    <div data-testid='security-details'>
      {query?.image || ''}|{query?.package_id || ''}|{query?.status || ''}
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

import KubernetesSecurityImages from '@components1/recommendations/security/KubernetesSecurityImages';

const apiRecommendations = require('@api1/recommendation').default;

const sampleImages = [
  {
    image: 'nginx:1.21',
    package_id: 'pkg-libssl',
    count_severity_critical: 1,
    count_severity_high: 3,
    count_severity_medium: 5,
    count_severity_low: 2,
    created_at: '2026-05-15T10:00:00Z',
  },
  {
    image: 'redis:6.0',
    package_id: 'pkg-curl',
    count_severity_critical: 0,
    count_severity_high: 1,
    count_severity_medium: 2,
    count_severity_low: 0,
    created_at: '2026-05-15T11:00:00Z',
  },
];

const sampleSeverity = {
  recommendation_security_groupings_v2: {
    rows: [
      {
        count_severity_critical: 2,
        count_severity_high: 8,
        count_severity_medium: 15,
        count_severity_low: 4,
        count_image: 9,
        count_workload_name: 12,
      },
    ],
  },
};

describe('KubernetesSecurityImages (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiRecommendations.listImageSecurityRecommendation.mockResolvedValue({
      recommendation_security_groupings_v2: { rows: sampleImages },
    });
    apiRecommendations.getSecuritySeverityGrouping.mockResolvedValue(sampleSeverity);
  });

  it('fetches image security list + severity on mount', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='img-table' />);

    await waitFor(() => {
      expect(apiRecommendations.listImageSecurityRecommendation).toHaveBeenCalledWith('acc-1', { status: 'Open' });
      expect(apiRecommendations.getSecuritySeverityGrouping).toHaveBeenCalled();
    });
  });

  it('skips severity infographic fetch when disableInfographic set', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='img-table' disableInfographic />);

    await waitFor(() => expect(apiRecommendations.listImageSecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.getSecuritySeverityGrouping).not.toHaveBeenCalled();
    expect(screen.queryByTestId('severity-infographic')).not.toBeInTheDocument();
  });

  it('passes image filter through to API query', async () => {
    render(
      <KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{ image: 'nginx', namespace: 'prod', status: 'Open' }} tableId='img-table' />
    );

    await waitFor(() => expect(apiRecommendations.listImageSecurityRecommendation).toHaveBeenCalled());
    expect(apiRecommendations.listImageSecurityRecommendation).toHaveBeenCalledWith('acc-1', {
      image: 'nginx',
      namespace: 'prod',
      status: 'Open',
    });
  });

  it('renders rows with image + package_id', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);

    await waitFor(() => expect(screen.getByText('nginx:1.21')).toBeInTheDocument());
    expect(screen.getByText('redis:6.0')).toBeInTheDocument();
    expect(screen.getByText('pkg-libssl')).toBeInTheDocument();
    expect(screen.getByText('pkg-curl')).toBeInTheDocument();
  });

  it('renders headers Image | Package ID + severity columns + Created at', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);

    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent('Image|Package ID'));
    expect(screen.getByTestId('headers')).toHaveTextContent('Created at');
    expect(screen.getAllByTestId('label-Critical').length).toBeGreaterThan(0);
    expect(screen.getAllByTestId('label-Low').length).toBeGreaterThan(0);
  });

  it('renders severity infographic with counts from severity API', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);

    await waitFor(() => expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:2'));
    expect(screen.getByTestId('severity-High')).toHaveTextContent('High:8');
    expect(screen.getByTestId('severity-Medium')).toHaveTextContent('Medium:15');
    expect(screen.getByTestId('severity-Low')).toHaveTextContent('Low:4');
  });

  it('renders Images + Apps counts from severity API', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);

    await waitFor(() => expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:9'));
    expect(screen.getByTestId('count-Apps')).toHaveTextContent('Apps:12');
  });

  it('renders expandable Details tab with image + package_id drilldown', async () => {
    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{ status: 'Closed' }} tableId='img-table' />);

    await waitFor(() => expect(screen.getByTestId('expandable-tabs')).toHaveTextContent('Details'));
    expect(screen.getByTestId('row-0-details')).toBeInTheDocument();
    expect(screen.getByTestId('security-details').textContent).toBe('nginx:1.21|pkg-libssl|Closed');
  });

  it('refetches when query prop changes', async () => {
    const { rerender } = render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{ status: 'Open' }} tableId='img-table' />);
    await waitFor(() => expect(apiRecommendations.listImageSecurityRecommendation).toHaveBeenCalled());
    apiRecommendations.listImageSecurityRecommendation.mockClear();

    rerender(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{ status: 'Closed' }} tableId='img-table' />);

    await waitFor(() => expect(apiRecommendations.listImageSecurityRecommendation).toHaveBeenCalledWith('acc-1', { status: 'Closed' }));
  });

  it('shows loading during fetch and clears after', async () => {
    let resolveFn;
    apiRecommendations.listImageSecurityRecommendation.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn({ recommendation_security_groupings_v2: { rows: [] } });
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles fetch rejection without crashing', async () => {
    apiRecommendations.listImageSecurityRecommendation.mockRejectedValue(new Error('boom'));

    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('shows initial dash placeholders before severity loads', async () => {
    let resolveFn;
    apiRecommendations.getSecuritySeverityGrouping.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<KubernetesSecurityImages kubernetes={{ id: 'acc-1' }} query={{}} tableId='img-table' />);

    expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:-');
    expect(screen.getByTestId('severity-Critical')).toHaveTextContent('Critical:-');

    await act(async () => {
      resolveFn(sampleSeverity);
    });

    await waitFor(() => expect(screen.getByTestId('count-Images')).toHaveTextContent('Images:9'));
  });
});
