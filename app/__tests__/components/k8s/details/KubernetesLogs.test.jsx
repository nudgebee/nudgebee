import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import KubernetesLogs, { buildStructuredQueryFromOperations } from '@components/k8s/details/KubernetesLogs';
import { useData } from '@context/DataContext';
import apiAccount from '@api1/account';
import observability from '@api1/observability';

jest.mock('@shared/buttons/DownloadButton', () => ({
  __esModule: true,
  default: ({ onClick }) => (
    <button data-testid='download-btn' onClick={onClick}>
      Download
    </button>
  ),
}));

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

// Source uses ds/ToggleGroup which renders custom DOM (not plain <button>).
// Mock it as a simple button group so the test's `getByRole('button', { name: ... })`
// queries match.
jest.mock('@ui/ToggleGroup', () => ({
  ToggleGroup: ({ options = [], onChange, value: activeValue }) => (
    <div role='group'>
      {options.map((opt) => (
        <button key={opt.value} type='button' onClick={() => onChange?.(opt.value)} aria-pressed={activeValue === opt.value}>
          {opt.label}
        </button>
      ))}
    </div>
  ),
}));

// Mock useData
jest.mock('@context/DataContext', () => ({
  useData: jest.fn(),
}));

// Log analysis opens the app-level chat drawer, whose provider lives in _app.
jest.mock('@context/NubiGlobalChatContext', () => ({
  useNubiGlobalChat: () => ({ openWithContext: jest.fn() }),
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

// Builder-mode "Operations" (line filters) were dropped from the submitted
// query_request — only label chips were sent. These lock in the conversion into
// `content` pseudo-field clauses the log sources map to line filters.
describe('buildStructuredQueryFromOperations', () => {
  it('converts a backend-token operation into a content clause', () => {
    expect(buildStructuredQueryFromOperations([{ id: 1, op: '_contains', value: 'raman' }])).toEqual([
      { _binary: { content: { _contains: 'raman' } } },
    ]);
  });

  it('maps legacy UI operator strings to backend tokens', () => {
    expect(buildStructuredQueryFromOperations([{ id: 1, op: 'REGEX', value: 'err.*' }])).toEqual([{ _binary: { content: { _regex: 'err.*' } } }]);
  });

  it('skips empty-value and unmapped-operator rows but keeps falsy non-null values', () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    expect(
      buildStructuredQueryFromOperations([
        { id: 1, op: '_contains', value: '   ' },
        { id: 2, op: '_contains', value: '' },
        { id: 3, op: 'bogus_op', value: 'x' },
        { id: 4, op: '_nlike', value: 'debug' },
        { id: 5, op: '_contains', value: 0 },
        { id: 6, op: '_contains', value: false },
      ])
    ).toEqual([
      { _binary: { content: { _nlike: 'debug' } } },
      { _binary: { content: { _contains: 0 } } },
      { _binary: { content: { _contains: false } } },
    ]);
    warn.mockRestore();
  });

  it('returns an empty array for no operations', () => {
    expect(buildStructuredQueryFromOperations([])).toEqual([]);
    expect(buildStructuredQueryFromOperations(undefined)).toEqual([]);
  });
});
