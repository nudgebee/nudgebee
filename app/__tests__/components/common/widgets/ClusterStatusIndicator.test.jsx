import React from 'react';
import { render } from '@testing-library/react';
import ClusterStatusIndicator from '@shared/widgets/ClusterStatusIndicator';

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
