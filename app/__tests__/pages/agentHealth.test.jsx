import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import AgentHealth, { buildDisconnectedServices, integrationServed } from '@pages/agentHealth';
import k8sApi from '@api1/kubernetes';

const mockHash = { current: 'agent' };
jest.mock('next/router', () => ({
  useRouter: () => ({
    query: { accountId: 'acc-1' },
    asPath: `/agentHealth#${mockHash.current}`,
    push: jest.fn(),
    replace: jest.fn(),
  }),
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { getAgentHealth: jest.fn(), getLatestVersions: jest.fn(), triggerCloudSync: jest.fn() },
}));

jest.mock('@context/DataContext', () => ({ useData: () => ({ selectedCluster: { cloud_provider: 'K8S', type: 'k8s' } }) }));
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => true }));
jest.mock('@utils/colors');
jest.mock('@shared/AccountGuard', () => ({ withAccountGuard: (Component) => Component }));
jest.mock('@components/vm/VmAgents', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/icons/SafeIcon', () => ({ __esModule: true, default: () => null }));
jest.mock('@shared/format/Text', () => ({ __esModule: true, default: ({ value }) => <span>{value}</span> }));
jest.mock('@shared/format/Datetime', () => ({ __esModule: true, default: ({ value }) => <span>{String(value)}</span> }));
// Renders headers and cell text so the Telemetry Sources table can be asserted on.
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ headers, tableData }) => (
    <table>
      <thead>
        <tr>
          {(headers || []).map((h) => (
            <th key={h}>{h}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {(tableData || []).map((row, i) => (
          <tr key={i} data-testid='row'>
            {row.map((cell, j) => (
              <td key={j}>{cell.text ?? cell.component ?? ''}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  ),
}));
jest.mock('@shared/navigation/Tabs', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn(), success: jest.fn() } }));
jest.mock('@ui/ListingLayout', () => {
  const ListingLayout = ({ children }) => <div>{children}</div>;
  ListingLayout.Toolbar = ({ title }) => <div>{title}</div>;
  ListingLayout.Body = ({ children }) => <div>{children}</div>;
  return { __esModule: true, ListingLayout };
});

// A connected agent that serves everything itself — the pre-existing shape.
const agentOnlyStatus = {
  relayConnection: true,
  prometheusConnection: true,
  prometheusUrl: 'http://prom.svc:9090',
  alertManagerConnection: true,
  logsConnection: true,
  logsConnectionProvider: 'loki',
  logProviderUrl: 'http://loki.svc:3100',
  tracesEnabled: true,
  nodeAgentConnection: true,
};

const renderPage = async (connectionStatus) => {
  k8sApi.getLatestVersions.mockResolvedValue({ data: { nudgebee_list_versions: { agent_version_latest: '0.1.22' } } });
  k8sApi.getAgentHealth.mockResolvedValue({
    data: [{ type: 'k8s', status: 'CONNECTED', version: '0.1.22', connection_status: connectionStatus }],
  });
  render(<AgentHealth />);
  await waitFor(() => expect(screen.getByText('Features')).toBeInTheDocument());
};

// The <li> that owns a label, matching how the e2e locators find one.
const featureItem = (label) => screen.getAllByRole('listitem').find((item) => item.textContent.startsWith(`${label} - `));

describe('agentHealth Features block', () => {
  afterEach(() => jest.clearAllMocks());

  it('defers a signal it does not serve instead of reporting the agent flag as its status', async () => {
    await renderPage({
      ...agentOnlyStatus,
      // The agent has no Loki of its own — which is exactly why its flag must not win.
      logsConnection: false,
      providerStatus: {
        checkedAt: '2026-09-05T01:10:00Z',
        logs: { provider: 'loki', source: 'user', integration_id: 'loki-1', integration_name: 'Grafana Cloud', connected: true },
      },
    });

    const logs = featureItem('Logs');
    expect(logs).toHaveTextContent('served by an integration (see Observability)');
    // The Agent tab must not pass judgement on a backend it never touches.
    expect(logs).not.toHaveTextContent('Status -');
    expect(logs).not.toHaveTextContent('Provider - loki');
    expect(screen.queryByText(/services are disconnected/)).not.toBeInTheDocument();
  });

  it('keeps a failing integration out of the agent banner, since the agent is not what failed', async () => {
    await renderPage({
      ...agentOnlyStatus,
      providerStatus: {
        checkedAt: '2026-09-05T01:10:00Z',
        metrics: {
          provider: 'prometheus',
          source: 'user',
          integration_id: 'amp-1',
          integration_name: 'Amazon Managed Prometheus',
          connected: false,
          error: 'connection test failed: 403',
        },
      },
    });

    // The entry keeps its agent-facing label; only its status is delegated.
    const prometheus = featureItem('Prometheus');
    expect(prometheus).toHaveTextContent('served by an integration (see Observability)');
    expect(prometheus).not.toHaveTextContent('Status -');
    expect(screen.queryByText(/services are disconnected/)).not.toBeInTheDocument();
  });

  it('renders an agent-only cluster exactly as before', async () => {
    await renderPage(agentOnlyStatus);

    const prometheus = featureItem('Prometheus');
    expect(prometheus).toHaveTextContent('Status - Connected');
    expect(prometheus).toHaveTextContent('URL - http://prom.svc:9090');
    expect(featureItem('Logs')).toHaveTextContent('Provider - loki');
    expect(screen.queryByText(/served by an integration/)).not.toBeInTheDocument();
    expect(screen.queryByText(/services are disconnected/)).not.toBeInTheDocument();
  });
});

describe('Observability tab', () => {
  beforeEach(() => {
    mockHash.current = 'observability';
  });
  afterEach(() => {
    mockHash.current = 'agent';
    jest.clearAllMocks();
  });

  const rowFor = (signal) => screen.getAllByTestId('row').find((r) => r.textContent.startsWith(signal));

  it('names the backend behind each signal and probes only the integration-served ones', async () => {
    k8sApi.getLatestVersions.mockResolvedValue({ data: { nudgebee_list_versions: {} } });
    k8sApi.getAgentHealth.mockResolvedValue({
      data: [
        {
          type: 'k8s',
          status: 'CONNECTED',
          connection_status: {
            ...agentOnlyStatus,
            providerStatus: {
              checkedAt: '2026-09-05T01:10:00Z',
              logs: { provider: 'loki', source: 'agent' },
              // Nothing configured at all — distinct from "the agent serves it".
              unconfigured: { provider: '', source: '' },
              metrics: {
                provider: 'prometheus',
                source: 'user',
                integration_id: 'amp-1',
                integration_name: 'Amazon Managed Prometheus',
                connected: false,
                error: 'connection test failed: 403',
              },
              traces: { provider: '', source: '' },
            },
          },
        },
      ],
    });
    render(<AgentHealth />);
    await waitFor(() => expect(screen.getByText('Observability')).toBeInTheDocument());

    // Agent-served: named, with the agent's own flag for that signal as its status.
    const logs = rowFor('Logs');
    expect(logs).toHaveTextContent('loki');
    expect(logs).toHaveTextContent('Agent');
    expect(logs).toHaveTextContent('Connected');

    // Integration-served: the integration is named and its probe result shown.
    const metrics = rowFor('Metrics');
    expect(metrics).toHaveTextContent('Integration "Amazon Managed Prometheus"');
    expect(metrics).toHaveTextContent('Disconnected');
    expect(metrics).toHaveTextContent('connection test failed: 403');

    // Nothing resolved at all — no invented provider, and no claim the agent reported one.
    const traces = rowFor('Traces');
    expect(traces).toHaveTextContent('Not configured');
    expect(traces).not.toHaveTextContent('Reported by agent');
  });
});

describe('Observability tab on an agent-served cluster', () => {
  beforeEach(() => {
    mockHash.current = 'observability';
  });
  afterEach(() => {
    mockHash.current = 'agent';
    jest.clearAllMocks();
  });

  // No non-agent integration means the provider-status check never stamps this cluster, so
  // the tab has to fill itself in from what the agent reported — otherwise the majority of
  // clusters open on an empty table.
  it('names the agent-reported backends when there is no stamp at all', async () => {
    k8sApi.getLatestVersions.mockResolvedValue({ data: { nudgebee_list_versions: {} } });
    k8sApi.getAgentHealth.mockResolvedValue({
      data: [{ type: 'k8s', status: 'CONNECTED', connection_status: { ...agentOnlyStatus, traceProvider: 'otel_clickhouse' } }],
    });
    render(<AgentHealth />);
    await waitFor(() => expect(screen.getByText('Observability')).toBeInTheDocument());

    const rowFor = (signal) => screen.getAllByTestId('row').find((r) => r.textContent.startsWith(signal));

    expect(rowFor('Logs')).toHaveTextContent('loki');
    expect(rowFor('Logs')).toHaveTextContent('Connected');
    expect(rowFor('Metrics')).toHaveTextContent('prometheus');
    expect(rowFor('Traces')).toHaveTextContent('otel_clickhouse');
  });

  it("reports the agent's own failure for a signal it does serve", async () => {
    k8sApi.getLatestVersions.mockResolvedValue({ data: { nudgebee_list_versions: {} } });
    k8sApi.getAgentHealth.mockResolvedValue({
      data: [{ type: 'k8s', status: 'CONNECTED', connection_status: { ...agentOnlyStatus, prometheusConnection: false } }],
    });
    render(<AgentHealth />);
    await waitFor(() => expect(screen.getByText('Observability')).toBeInTheDocument());

    const metrics = screen.getAllByTestId('row').find((r) => r.textContent.startsWith('Metrics'));
    expect(metrics).toHaveTextContent('prometheus');
    expect(metrics).toHaveTextContent('Disconnected');
  });

  it('says Not configured for traces the agent has not enabled', async () => {
    k8sApi.getLatestVersions.mockResolvedValue({ data: { nudgebee_list_versions: {} } });
    k8sApi.getAgentHealth.mockResolvedValue({
      data: [
        {
          type: 'k8s',
          status: 'CONNECTED',
          // The provider name is a constant every agent sends; only tracesEnabled is evidence.
          connection_status: { ...agentOnlyStatus, tracesEnabled: false, traceProvider: 'otel_clickhouse' },
        },
      ],
    });
    render(<AgentHealth />);
    await waitFor(() => expect(screen.getByText('Observability')).toBeInTheDocument());

    const traces = screen.getAllByTestId('row').find((r) => r.textContent.startsWith('Traces'));
    expect(traces).toHaveTextContent('Not configured');
    expect(traces).not.toHaveTextContent('otel_clickhouse');
  });
});

describe('integrationServed', () => {
  it('claims a signal only when an integration id is stamped', () => {
    expect(integrationServed(null, 'logs')).toBeNull();
    expect(integrationServed({ logs: { provider: 'loki', source: 'agent' } }, 'logs')).toBeNull();
    expect(integrationServed({ logs: { integration_id: 'x' } }, 'logs')).toEqual({ integration_id: 'x' });
  });
});

describe('buildDisconnectedServices', () => {
  const healthyAgent = {
    relayConnection: true,
    prometheusConnection: true,
    alertManagerConnection: true,
    logsConnection: true,
    nodeAgentConnection: true,
  };

  it('keeps the agent flags for agent-served signals', () => {
    expect(buildDisconnectedServices({ ...healthyAgent, logsConnection: false }, null)).toEqual(['Logs']);
    expect(buildDisconnectedServices({ ...healthyAgent, prometheusConnection: false }, null)).toEqual(['Prometheus']);
    expect(buildDisconnectedServices(healthyAgent, null)).toEqual([]);
  });

  it('omits an integration-served signal whether it is healthy or failing', () => {
    // A stale agent flag must not raise the banner for a backend the agent does not serve …
    const healthy = { logs: { integration_id: 'loki-1', connected: true } };
    expect(buildDisconnectedServices({ ...healthyAgent, logsConnection: false }, healthy)).toEqual([]);

    // … and a failing one belongs to Telemetry Sources, not to the agent's banner.
    const failing = { logs: { integration_id: 'loki-1', connected: false } };
    expect(buildDisconnectedServices({ ...healthyAgent, logsConnection: false }, failing)).toEqual([]);
  });

  it('still names an agent-served signal whose own flag is down', () => {
    expect(buildDisconnectedServices({ ...healthyAgent, prometheusConnection: false }, { metrics: { source: 'agent' } })).toEqual(['Prometheus']);
  });
});
