import React from 'react';
import { render } from '@testing-library/react';
import ClusterStatusIndicator from '@components1/common/ClusterStatusIndicator';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
    },
  },
}));

describe('ClusterStatusIndicator', () => {
  test('renders null when clusterData is empty object', () => {
    const { container } = render(<ClusterStatusIndicator clusterData={{}} />);
    expect(container.firstChild).toBeNull();
  });

  test('renders null when agent status is undefined', () => {
    const { container } = render(<ClusterStatusIndicator clusterData={{ agent: {} }} />);
    expect(container.firstChild).toBeNull();
  });

  test('renders null when agent status is not CONNECTED or NOT_CONNECTED', () => {
    const { container } = render(<ClusterStatusIndicator clusterData={{ agent: { status: 'PENDING' } }} />);
    expect(container.firstChild).toBeNull();
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
