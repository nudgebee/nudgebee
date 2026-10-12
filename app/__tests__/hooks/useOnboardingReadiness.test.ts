import { act, renderHook, waitFor } from '@testing-library/react';
import { useOnboardingReadiness } from '@hooks/useOnboardingReadiness';

jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getAllAccount: jest.fn(),
    listProviderCapabilities: jest.fn(),
  },
}));
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    listK8sAccAgentHealth: jest.fn(),
  },
}));
jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: {
    getKnowledgeBases: jest.fn(),
  },
}));

import apiAccount from '@api1/account';
import apiKnowledgeBase from '@api1/knowledge-base';

const mockGetAllAccount = apiAccount.getAllAccount as jest.Mock;
const mockListProviderCapabilities = apiAccount.listProviderCapabilities as jest.Mock;
const mockGetKnowledgeBases = apiKnowledgeBase.getKnowledgeBases as jest.Mock;

// AWS, not K8s: keeps every account out of the agent-health lookup's own
// filter, so these tests don't also need to mock `listK8sAccAgentHealth`.
const awsAccount = (id: string) => ({ id, cloud_provider: 'AWS', account_name: id, status: 'active' });
const payload = (accounts: unknown[]) => ({ data: { all_accounts: accounts, integrations: [], messaging_platforms: [] } });

describe('useOnboardingReadiness', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListProviderCapabilities.mockResolvedValue({ data: { data: { observability_list_provider_capabilities: [] } } });
  });

  // Known issue #24: a fully-failed lookup used to collapse to `false` (a
  // confirmed empty result), indistinguishable from a lookup that genuinely
  // found nothing. `refresh()`'s own resolved value carries the raw
  // `hasKnowledgeBase` the fix touches — `readiness.items` alone can't tell
  // these two cases apart, since `skillsEvaluator` renders both as `blank`.
  describe('hasKnowledgeBase — known issue #24', () => {
    it('is null when the lookup call itself fails, not false', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockRejectedValue(new Error('service unavailable'));
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasKnowledgeBase).toBeNull();
    });

    // The real failure shape: `getKnowledgeBases` has its own internal
    // try/catch and never actually rejects — it resolves normally with an
    // `errors` entry instead. The rejection test above alone can't catch a
    // regression here, since the real API can't produce what it mocks.
    it('is null when the lookup resolves with errors, not just when it rejects', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [], errors: [{ message: 'Failed to fetch knowledge bases' }] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasKnowledgeBase).toBeNull();
    });

    it('is false when the lookup succeeds and finds none', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasKnowledgeBase).toBe(false);
    });

    it('is true when the lookup succeeds and finds at least one', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [{ id: 'kb-1', name: 'Runbooks' }] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasKnowledgeBase).toBe(true);
    });
  });

  // Known issue #17: raised from 10 to 30. Locking in the new number so a
  // future change to it is a deliberate edit here, not a silent drift.
  describe('telemetry detection account cap — known issue #17', () => {
    it('asks at most 30 accounts, not more', async () => {
      const accounts = Array.from({ length: 35 }, (_, i) => awsAccount(`acc-${i}`));
      mockGetAllAccount.mockResolvedValue(payload(accounts));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(mockListProviderCapabilities).toHaveBeenCalledTimes(30);
    });

    it('asks every account when there are fewer than the cap', async () => {
      const accounts = Array.from({ length: 5 }, (_, i) => awsAccount(`acc-${i}`));
      mockGetAllAccount.mockResolvedValue(payload(accounts));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(mockListProviderCapabilities).toHaveBeenCalledTimes(5);
    });
  });

  // Detection only feeds the metrics/logs/traces items, and the first account
  // to report a signal wins — so once all three are found, asking further
  // accounts only spends the viewer's per-user GraphQL budget.
  describe('telemetry detection stops once every signal is found', () => {
    const caps = (...entries: Array<[string, string]>) => ({
      data: { data: { observability_list_provider_capabilities: entries.map(([provider_type, provider]) => ({ provider_type, provider })) } },
    });
    const answerPerAccount = (answers: Record<string, ReturnType<typeof caps>>) =>
      mockListProviderCapabilities.mockImplementation(({ account_id }: { account_id: string }) => Promise.resolve(answers[account_id] ?? caps()));
    const askedIds = () => mockListProviderCapabilities.mock.calls.map(([arg]) => arg.account_id);

    const loadThenReadSignals = async () => {
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));
      const asked = askedIds();
      mockListProviderCapabilities.mockClear();
      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      return { asked, detectedSignals: input?.detectedSignals };
    };

    beforeEach(() => {
      mockGetAllAccount.mockResolvedValue(payload(Array.from({ length: 12 }, (_, i) => awsAccount(`acc-${i}`))));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
    });

    it('asks only the first five accounts when they report every signal', async () => {
      answerPerAccount({ 'acc-0': caps(['metrics', 'prometheus'], ['logs', 'loki'], ['traces', 'clickhouse']) });

      const { asked, detectedSignals } = await loadThenReadSignals();

      expect(asked).toEqual(['acc-0', 'acc-1', 'acc-2', 'acc-3', 'acc-4']);
      expect(detectedSignals).toEqual({ metrics: 'prometheus', logs: 'loki', traces: 'clickhouse' });
    });

    it('asks the rest while a signal is still missing, and the first account to report a signal still wins', async () => {
      answerPerAccount({
        'acc-0': caps(['metrics', 'prometheus'], ['logs', 'loki']),
        'acc-7': caps(['metrics', 'datadog'], ['traces', 'tempo']),
      });

      const { asked, detectedSignals } = await loadThenReadSignals();

      expect(asked).toHaveLength(12);
      expect(detectedSignals).toEqual({ metrics: 'prometheus', logs: 'loki', traces: 'tempo' });
    });
  });

  describe('enabled', () => {
    it('makes no request while disabled, then fetches once enabled', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });

      const { result, rerender } = renderHook(({ enabled }) => useOnboardingReadiness([], { enabled }), { initialProps: { enabled: false } });
      expect(mockGetAllAccount).not.toHaveBeenCalled();
      expect(mockListProviderCapabilities).not.toHaveBeenCalled();
      expect(result.current.loading).toBe(true);

      rerender({ enabled: true });
      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(mockGetAllAccount).toHaveBeenCalledTimes(1);
      expect(mockListProviderCapabilities).toHaveBeenCalledTimes(1);
    });
  });
});
