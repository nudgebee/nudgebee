import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import KubernetesLogs from '@components1/k8s/details/KubernetesLogs';
import { useData } from '@context/DataContext';
import apiAccount from '@api1/account';
import observability from '@api1/observability';

jest.mock('@assets/loki.png', () => ({
  default: { src: 'loki.png' },
}));

jest.mock('@assets/LoggleIcon.png', () => ({
  default: { src: 'loggle.png' },
}));

jest.mock('@assets/SignozIcon.png', () => ({
  default: { src: 'signoz.png' },
}));

jest.mock('@assets/NubiIcon.png', () => ({
  default: { src: 'nubi.png' },
}));

// Mock useData
jest.mock('@context/DataContext', () => ({
  useData: jest.fn(),
}));

// Mock apiAccount
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getDefaultProvider: jest.fn(),
  },
}));

// Mock observability
jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: {
    fetchLogs: jest.fn(),
    fetchLogLabels: jest.fn(),
    createUserHistory: jest.fn(),
  },
}));

describe('KubernetesLogs Loki', () => {
  const accountId = '123';

  beforeEach(() => {
    useData.mockReturnValue({
      selectedCluster: {
        agent: { connection_status: { logsConnectionProvider: 'loki' } },
        cloud_account_attrs: [],
      },
    });

    apiAccount.getDefaultProvider.mockResolvedValue({
      data: { data: { get_default_provider: { provider: 'loki' } }, errors: null },
    });

    observability.fetchLogs.mockResolvedValue({
      data: { data: { logs_list: [] } },
      error: null,
    });

    observability.fetchLogLabels.mockResolvedValue({
      data: { data: { logs_label_names: [] } },
      error: null,
    });
  });

  it('renders Builder, Code, and AI toggle buttons', async () => {
    render(
      <KubernetesLogs
        accountId={accountId}
        showQueryTextBox={true}
        showDateFilter={false}
        showPolling={false}
        dateTime={{
          startTime: new Date().getTime() - 3600 * 1000,
          endTime: new Date().getTime(),
        }}
      />
    );

    // Wait for logProvider to be set
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Builder/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /Code/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /AI/i })).toBeInTheDocument();
    });
  });
});

describe('KubernetesLogs Signoz', () => {
  const accountId = '123';

  beforeEach(() => {
    useData.mockReturnValue({
      selectedCluster: {
        agent: { connection_status: { logsConnectionProvider: 'signoz' } },
        cloud_account_attrs: [],
      },
    });

    apiAccount.getDefaultProvider.mockResolvedValue({
      data: { data: { get_default_provider: { provider: 'signoz' } }, errors: null },
    });

    observability.fetchLogs.mockResolvedValue({
      data: { data: { logs_list: [] } },
      error: null,
    });

    observability.fetchLogLabels.mockResolvedValue({
      data: { data: { logs_list_labels: [] } },
      error: null,
    });
  });

  it('renders Builder', async () => {
    render(
      <KubernetesLogs
        accountId={accountId}
        showQueryTextBox={true}
        showDateFilter={false}
        showPolling={false}
        dateTime={{
          startTime: new Date().getTime() - 3600 * 1000,
          endTime: new Date().getTime(),
        }}
      />
    );

    // Wait for logProvider to be set
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Builder/i })).toBeInTheDocument();
    });
  });
});
