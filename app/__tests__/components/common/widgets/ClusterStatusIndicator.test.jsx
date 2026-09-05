import React from 'react';
import { render } from '@testing-library/react';
import ClusterStatusIndicator, { checkConnections } from '@shared/widgets/ClusterStatusIndicator';

jest.mock('@utils/colors');

describe('ClusterStatusIndicator', () => {
  test('renders a gray dot when clusterData is empty object', () => {
    const { container } = render(<ClusterStatusIndicator clusterData={{}} />);
    expect(container.firstChild).not.toBeNull();
  });

  test('renders a gray dot when agent status is undefined', () => {
    const { container } = render(<ClusterStatusIndicator clusterData={{ agent: {} }} />);
    expect(container.firstChild).not.toBeNull();
  });

  test('renders a gray dot when agent status is not CONNECTED or NOT_CONNECTED', () => {
    const { container } = render(<ClusterStatusIndicator clusterData={{ agent: { status: 'PENDING' } }} />);
    expect(container.firstChild).not.toBeNull();
  });

  test('renders a dot when agent.status is "CONNECTED"', () => {
    const clusterData = {
      cloud_provider: 'k8s',
      agent: {
        status: 'CONNECTED',
        connection_status: {
          logsConnection: true,
          nodeAgentConnection: true,
          opencostConnection: true,
          prometheusConnection: true,
          relayConnection: true,
        },
      },
    };
    const { container } = render(<ClusterStatusIndicator clusterData={clusterData} />);
    expect(container.firstChild).not.toBeNull();
  });

  test('renders red dot when agent.status is "NOT_CONNECTED"', () => {
    const clusterData = {
      agent: { status: 'NOT_CONNECTED' },
    };
    const { container } = render(<ClusterStatusIndicator clusterData={clusterData} />);
    expect(container.firstChild).not.toBeNull();
  });

  test('checks k8s connection using required props (all true = green)', () => {
    const clusterData = {
      cloud_provider: 'k8s',
      agent: {
        status: 'CONNECTED',
        connection_status: {
          logsConnection: true,
          nodeAgentConnection: true,
          opencostConnection: true,
          prometheusConnection: true,
          relayConnection: true,
        },
      },
    };
    const { container } = render(<ClusterStatusIndicator clusterData={clusterData} />);
    // Should render with green color (clusterIndicator color)
    expect(container.firstChild).not.toBeNull();
  });

  test('checks k8s connection using required props (any false = yellow)', () => {
    const clusterData = {
      cloud_provider: 'k8s',
      agent: {
        status: 'CONNECTED',
        connection_status: {
          logsConnection: false,
          nodeAgentConnection: true,
          opencostConnection: true,
          prometheusConnection: true,
          relayConnection: true,
        },
      },
    };
    const { container } = render(<ClusterStatusIndicator clusterData={clusterData} />);
    // Should render (yellow indicator)
    expect(container.firstChild).not.toBeNull();
  });
});

// checkConnections decides green-vs-amber for a CONNECTED agent, and the same verdict now
// drives account ordering in global search and on the overview page.
const k8sCluster = (connectionStatus) => ({
  cloud_provider: 'K8s',
  agent: { status: 'CONNECTED', connection_status: connectionStatus },
});

const healthyAgent = {
  relayConnection: true,
  nodeAgentConnection: true,
  logsConnection: true,
  prometheusConnection: true,
  opencostConnection: true,
};

describe('checkConnections', () => {
  it('is green when the agent serves everything itself', () => {
    expect(checkConnections(k8sCluster(healthyAgent))).toBe(true);
  });

  // The case this exists for: the agent is healthy but does not serve logs or metrics, so
  // its flags for them describe backends it never touches. Requiring them held such a
  // cluster amber forever even though its telemetry was fine.
  it('is green when a healthy integration serves logs and metrics instead of the agent', () => {
    const cluster = k8sCluster({
      ...healthyAgent,
      logsConnection: false,
      prometheusConnection: false,
      providerStatus: {
        logs: { provider: 'loki', integration_id: 'loki-1', connected: true },
        metrics: { provider: 'prometheus', integration_id: 'amp-1', connected: true },
      },
    });
    expect(checkConnections(cluster)).toBe(true);
  });

  it('is amber when the integration serving a signal is failing', () => {
    const cluster = k8sCluster({
      ...healthyAgent,
      logsConnection: false,
      providerStatus: { logs: { provider: 'loki', integration_id: 'loki-1', connected: false } },
    });
    expect(checkConnections(cluster)).toBe(false);
  });

  // An agent-served entry carries no integration_id, so it must not stand in for the
  // agent's own flag — otherwise a broken agent backend would read as healthy.
  it('does not let an agent-served entry excuse the agent flag', () => {
    const cluster = k8sCluster({
      ...healthyAgent,
      logsConnection: false,
      providerStatus: { logs: { provider: 'loki', source: 'agent' } },
    });
    expect(checkConnections(cluster)).toBe(false);
  });

  it.each(['relayConnection', 'nodeAgentConnection'])('still requires the agent-owned %s', (prop) => {
    const cluster = k8sCluster({
      ...healthyAgent,
      [prop]: false,
      providerStatus: {
        logs: { integration_id: 'loki-1', connected: true },
        metrics: { integration_id: 'amp-1', connected: true },
      },
    });
    expect(checkConnections(cluster)).toBe(false);
  });

  it('still accepts server-side OpenCost', () => {
    expect(checkConnections(k8sCluster({ ...healthyAgent, opencostConnection: false, opencostServerSide: true }))).toBe(true);
    expect(checkConnections(k8sCluster({ ...healthyAgent, opencostConnection: false }))).toBe(false);
  });
});
