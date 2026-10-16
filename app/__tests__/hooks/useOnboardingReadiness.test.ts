import { act, renderHook, waitFor } from '@testing-library/react';
import { useOnboardingReadiness } from '@hooks/useOnboardingReadiness';

jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getAllAccount: jest.fn(),
  },
}));
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    listK8sAccAgentHealth: jest.fn(),
    listCloudAccountFirstSync: jest.fn(),
  },
}));
jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: {
    getKnowledgeBases: jest.fn(),
  },
}));
jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    hasReceivedWebhookEvents: jest.fn(),
  },
}));

import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import apiKnowledgeBase from '@api1/knowledge-base';
import apiIntegrations from '@api1/integrations';

const mockGetAllAccount = apiAccount.getAllAccount as jest.Mock;
const mockListK8sAccAgentHealth = apiKubernetes1.listK8sAccAgentHealth as jest.Mock;
const mockListCloudAccountFirstSync = apiKubernetes1.listCloudAccountFirstSync as jest.Mock;
const mockGetKnowledgeBases = apiKnowledgeBase.getKnowledgeBases as jest.Mock;
const mockHasReceivedWebhookEvents = apiIntegrations.hasReceivedWebhookEvents as jest.Mock;

// AWS, not K8s or SelfHosted: keeps every account out of the agent-health
// lookup's own filter, so these tests don't also need to mock
// `listK8sAccAgentHealth`.
const awsAccount = (id: string) => ({ id, cloud_provider: 'AWS', account_name: id, status: 'active' });
const payload = (accounts: unknown[], integrations: unknown[] = []) => ({
  data: { all_accounts: accounts, integrations, messaging_platforms: [] },
});
const agentHealth = (ids: string[], status = 'CONNECTED') => ({ data: { data: { agent: ids.map((id) => ({ cloud_account_id: id, status })) } } });
// Every requested account reads as already synced — so tests below that use
// an AWS/GCP/Azure fixture for something unrelated to cloud-sync timing
// (knowledge base, alerts, …) keep seeing it as fully done, the same as
// before this lookup existed. Tests about cloud-sync timing itself override
// this mock directly.
const cloudSyncDone = (ids: string[]) =>
  ids.map((id) => ({ cloud_account_id: id, status: 'CONNECTED', connection_status: { spends: { updated_at: new Date().toISOString() } } }));

describe('useOnboardingReadiness', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListK8sAccAgentHealth.mockResolvedValue(agentHealth([]));
    mockListCloudAccountFirstSync.mockImplementation((ids: string[]) => Promise.resolve(cloudSyncDone(ids)));
    mockHasReceivedWebhookEvents.mockResolvedValue(undefined);
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

  // #41114: a webhook URL existing (even one GCP creates with zero user
  // action) is not the same as anything having actually arrived on it.
  describe('hasReceivedAlert — #41114', () => {
    const alertIntegration = { id: 'int-1', type: 'datadog_webhook', status: 'enabled', source: 'user' };

    it('is null when no alert-webhook integration is connected, without calling the lookup', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasReceivedAlert).toBeNull();
      expect(mockHasReceivedWebhookEvents).not.toHaveBeenCalled();
    });

    it('asks only for the connected alert-webhook rows, by id', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')], [alertIntegration, { id: 'int-2', type: 'jira', status: 'enabled' }]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      mockHasReceivedWebhookEvents.mockResolvedValue(true);
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(mockHasReceivedWebhookEvents).toHaveBeenCalledWith(['int-1']);
    });

    it('is null when the lookup fails, not false — a connected signal must not read as unreceived on a flaky query', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')], [alertIntegration]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      mockHasReceivedWebhookEvents.mockResolvedValue(undefined);
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasReceivedAlert).toBeNull();
    });

    it('is false when the lookup succeeds and finds nothing yet', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')], [alertIntegration]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      mockHasReceivedWebhookEvents.mockResolvedValue(false);
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasReceivedAlert).toBe(false);
    });

    it('is true when the lookup finds at least one event', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')], [alertIntegration]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      mockHasReceivedWebhookEvents.mockResolvedValue(true);
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.hasReceivedAlert).toBe(true);
    });
  });

  // #41115: self-hosted accounts used to be invisible to the agent-health
  // lookup entirely — only K8s ids were ever asked about.
  describe('agent health covers K8s and self-hosted together — #41115', () => {
    const k8sAccount = (id: string) => ({ id, cloud_provider: 'K8s', account_name: id, status: 'active' });
    const selfHostedAccount = (id: string) => ({ id, cloud_provider: 'SelfHosted', account_name: id, status: 'active' });

    it('asks about a self-hosted account the same as a K8s one, in a single untyped call', async () => {
      mockGetAllAccount.mockResolvedValue(payload([k8sAccount('k8s-1'), selfHostedAccount('vm-1'), awsAccount('aws-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(mockListK8sAccAgentHealth).toHaveBeenCalledTimes(1);
      const [ids, agentType] = mockListK8sAccAgentHealth.mock.calls[0];
      expect(ids).toEqual(expect.arrayContaining(['k8s-1', 'vm-1']));
      expect(ids).not.toContain('aws-1');
      expect(agentType).toBeNull();
    });

    it('makes no call at all when every account is a kind with no agent to check', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('aws-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(mockListK8sAccAgentHealth).not.toHaveBeenCalled();
    });
  });

  // AWS/GCP/Azure's counterpart to the agent-health lookup above — a
  // first-cost-sync phase per account, kept out of `SetupGateContext`'s own
  // lock (see `accountRowState`'s own comment) by never reaching it.
  describe('cloud-sync state covers AWS, GCP and Azure, one call per provider', () => {
    const gcpAccount = (id: string) => ({ id, cloud_provider: 'GCP', account_name: id, status: 'active' });
    const azureAccount = (id: string) => ({ id, cloud_provider: 'Azure', account_name: id, status: 'active' });

    it('asks each provider only about its own accounts', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('aws-1'), gcpAccount('gcp-1'), azureAccount('az-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      expect(mockListCloudAccountFirstSync).toHaveBeenCalledTimes(3);
      const calls = Object.fromEntries(mockListCloudAccountFirstSync.mock.calls.map(([ids, provider]) => [provider, ids]));
      expect(calls.AWS).toEqual(['aws-1']);
      expect(calls.GCP).toEqual(['gcp-1']);
      expect(calls.Azure).toEqual(['az-1']);
    });

    it('makes no call for a provider with no accounts of its kind', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('aws-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      const providersAsked = mockListCloudAccountFirstSync.mock.calls.map(([, provider]) => provider);
      expect(providersAsked).toEqual(['AWS']);
    });

    it('reads waiting when nothing has reported yet, and carries that into the readiness state', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('aws-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      mockListCloudAccountFirstSync.mockResolvedValue([]);
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.cloudSyncState).toEqual({ 'aws-1': 'waiting' });
      const account = result.current.readiness?.panels
        .find((p) => p.id === 'accounts')
        ?.items.find((i) => i.id === 'account')
        ?.accounts?.find((a) => a.id === 'aws-1');
      expect(account?.state).toBe('partial');
    });

    it('reads failed when the sync reported a problem', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('aws-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });
      mockListCloudAccountFirstSync.mockResolvedValue([
        {
          cloud_account_id: 'aws-1',
          status: 'NOT_CONNECTED',
          status_message: 'Access denied',
          connection_status: { spends: { updated_at: new Date().toISOString() } },
        },
      ]);
      const { result } = renderHook(() => useOnboardingReadiness());
      await waitFor(() => expect(result.current.loading).toBe(false));

      let input: Awaited<ReturnType<typeof result.current.refresh>>;
      await act(async () => {
        input = await result.current.refresh();
      });
      expect(input?.cloudSyncState).toEqual({ 'aws-1': 'failed' });
    });
  });

  describe('enabled', () => {
    it('makes no request while disabled, then fetches once enabled', async () => {
      mockGetAllAccount.mockResolvedValue(payload([awsAccount('acc-1')]));
      mockGetKnowledgeBases.mockResolvedValue({ data: [] });

      const { result, rerender } = renderHook(({ enabled }) => useOnboardingReadiness([], { enabled }), { initialProps: { enabled: false } });
      expect(mockGetAllAccount).not.toHaveBeenCalled();
      expect(result.current.loading).toBe(true);

      rerender({ enabled: true });
      await waitFor(() => expect(result.current.loading).toBe(false));
      expect(mockGetAllAccount).toHaveBeenCalledTimes(1);
    });
  });
});
