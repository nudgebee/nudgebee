/**
 * The one place that turns `apiAccount.getAllAccount()` into an `OnboardingReadiness` snapshot — shared by
 * `SetupCenter` (the checklist) and `SetupProgressBanner` (the `/home`
 * reminder), so the two can never show a different "X of N" for the same
 * tenant at the same moment. They used to: the banner read a cheaper,
 * accounts-and-integrations-only snapshot off `SetupGateContext` that never
 * saw K8s agent health or Knowledge Base, so a K8s
 * account still waiting on its agent counted as connected there but not on
 * the card. Extracted here rather than fixed in place, so there is only ever
 * one fetch-and-evaluate implementation to keep correct.
 *
 * `SetupGateContext` still runs its own, separate, cheaper lookup — that one
 * decides whether the tenant is locked to `/setup` at all, a different
 * question that must stay fast on every page load. It is no longer
 * accounts-only, though: it moved from existence to the same per-row
 * "actually connecting or connected" check this hook and `accountEvaluator`
 * use (`accountRowState`), which for a K8s account needs agent health too —
 * still far cheaper than this hook's own fetch, since it skips
 * Knowledge Base and everything else this hook also asks for. This hook
 * stays "how much is left", fetched only where that is actually shown.
 */
import * as React from 'react';
import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import apiKnowledgeBase from '@api1/knowledge-base';
import apiIntegrations from '@api1/integrations';
import { AGENT_CONNECTED } from '@components/integrations/pendingAgentInstall';
import { evaluateCloudFirstSync } from '@components/integrations/CloudFirstSync';
import { ALERT_PROVIDERS, ONBOARDING_CLOUD_PROVIDERS } from '@lib/onboarding/setupRegistry';
import {
  evaluateOnboarding,
  type EvaluateOnboardingInput,
  type OnboardingAccount,
  type OnboardingIntegration,
  type OnboardingMessagingPlatform,
  type OnboardingReadiness,
} from '@lib/onboardingReadiness';

// Integration `type` values any ALERT_PROVIDERS entry matches — the same set
// `alerts`'s own registry providers declare, flattened once rather than per
// fetch.
const ALERT_TYPES = new Set(ALERT_PROVIDERS.flatMap((p) => p.types || []).map((t) => t.toLowerCase()));

// Shape `apiAccount.getAllAccount()` resolves with. It is a plain JS module, so
// this is declared here rather than imported; the fields are exactly the three
// `evaluateOnboarding` consumes.
type GetAllAccountResult =
  | {
      data?: {
        all_accounts?: OnboardingAccount[];
        integrations?: OnboardingIntegration[];
        messaging_platforms?: OnboardingMessagingPlatform[];
      };
    }
  | Error
  | undefined;

export interface UseOnboardingReadinessResult {
  readiness: OnboardingReadiness | null;
  loading: boolean;
  /** Re-fetches and updates `readiness`. Callers that also need the raw
   * `EvaluateOnboardingInput` (there are none today) can read the resolved
   * value off the returned promise. */
  refresh: () => Promise<EvaluateOnboardingInput | null>;
  /** The raw, unfiltered `all_accounts` this snapshot was built from — the
   * same fetch `evaluateOnboarding` itself reduces into `connectedProviders`,
   * exposed here too for a caller that needs an actual account id (e.g. to
   * scope a write), not just which cloud types are connected. Empty until
   * the first fetch resolves. */
  accounts: OnboardingAccount[];
}

/**
 * `selectedModules` narrows what counts as required, the same input
 * `evaluateOnboarding` itself already treats "empty = everything selected"
 * for — pass nothing for a consumer with no module-selection UI of its own
 * (the banner), or the live selection for one that has it (the card).
 *
 * `enabled: false` skips the mount fetch, for a consumer that already knows it
 * will render nothing (the banner, for a viewer who cannot act on it or has
 * snoozed it). `loading` stays true until the first enabled fetch resolves.
 */
export function useOnboardingReadiness(selectedModules: string[] = [], { enabled = true }: { enabled?: boolean } = {}): UseOnboardingReadinessResult {
  const [inputs, setInputs] = React.useState<EvaluateOnboardingInput | null>(null);
  const [loading, setLoading] = React.useState(true);

  const fetchInputs = React.useCallback(async (): Promise<EvaluateOnboardingInput | null> => {
    const raw = (await apiAccount.getAllAccount().catch(() => undefined)) as GetAllAccountResult;

    // `getAllAccount` swallows its own failures and *returns* the error rather
    // than rejecting, so `.catch` alone would never fire. Treat a missing
    // payload as "unknown", not as "nothing connected" — rendering a full
    // setup checklist to an established tenant because one query blipped is
    // worse than rendering nothing.
    const data = raw instanceof Error ? undefined : raw?.data;
    if (!data) return null;

    // A `cloud_accounts` row goes in the instant the K8s add-account wizard
    // submits step 1 — or a self-hosted account is named and saved — well
    // before any agent has checked in. Reading `status` alone would mark the
    // account (and the whole panel) done before any install command has
    // even run, so both are checked against the same agent-health table the
    // K8s wizard's own "waiting for agent" step and the global pending-install
    // banner already trust (#41115 extended this from K8s-only to also cover
    // self-hosted VM fleets). One batched, untyped call (`agentType: null`)
    // covers both kinds together — each account only ever carries agents of
    // its own kind, the same reasoning `PendingAgentInstallBanner` already
    // relies on. Unlike the per-account telemetry lookup below, there is no
    // per-request cost to cap.
    const agentCheckedAccountIds = (data.all_accounts || [])
      .filter(
        (a): a is OnboardingAccount & { id: string } =>
          typeof a?.id === 'string' &&
          a.id.length > 0 &&
          a.id !== 'demo' &&
          typeof a?.cloud_provider === 'string' &&
          ['k8s', 'selfhosted'].includes(a.cloud_provider.trim().toLowerCase())
      )
      .map((a) => a.id);

    // Every connected alert-webhook integration's row id, so the one
    // tenant-wide "has anything ever arrived" call can cover all of them —
    // a second Alertmanager or Datadog webhook must not hide behind the
    // first one's own silence.
    const alertIntegrationIds = (data.integrations || [])
      .filter(
        (i): i is OnboardingIntegration & { id: string } =>
          typeof i?.id === 'string' && i.id.length > 0 && ALERT_TYPES.has(String(i?.type || '').toLowerCase())
      )
      .map((i) => i.id);

    // AWS/GCP/Azure accounts, grouped by provider: `listCloudAccountFirstSync`
    // takes one provider per call, the same as each account modal's own wait
    // screen already calls it (`useCloudFirstSync` in `CloudFirstSync.tsx`).
    // K8s/self-hosted are excluded — those are covered by the agent-health
    // lookup above instead, a different signal for a different reason to wait.
    const cloudSyncAccountIdsByProvider = ONBOARDING_CLOUD_PROVIDERS.filter((p) => !['k8s', 'selfhosted'].includes(p.provider.toLowerCase())).map(
      (p) => ({
        provider: p.provider,
        ids: (data.all_accounts || [])
          .filter(
            (a): a is OnboardingAccount & { id: string } =>
              typeof a?.id === 'string' &&
              a.id.length > 0 &&
              a.id !== 'demo' &&
              typeof a?.cloud_provider === 'string' &&
              a.cloud_provider.trim().toLowerCase() === p.provider.toLowerCase()
          )
          .map((a) => a.id),
      })
    );

    const [[agentHealthResult, kbResult, hasReceivedAlert], cloudSyncRowsByProvider] = await Promise.all([
      Promise.all([
        agentCheckedAccountIds.length > 0
          ? apiKubernetes1.listK8sAccAgentHealth(agentCheckedAccountIds, null).catch(() => undefined)
          : Promise.resolve(undefined),
        // Tenant-wide in one call (empty accountId — same mode SetupCenter's own
        // KB-chip fetch and KnowledgeBaseTab.jsx already use), not one request per
        // account. This used to be a per-account, capped loop — a knowledge base is tenant-wide
        // for scoring purposes (any account having one satisfies the item), so
        // that only added up to 10 requests to save one and, worse, could
        // disagree with SetupCenter's own tenant-wide fetch for the same fact:
        // the Skills item read "not connected" while its own chip list (built
        // from the wider read) showed real, named knowledge bases. A lookup that
        // fails or finds none still reads as "not connected" rather than
        // blocking the rest of the card — there is no fallback substrate for
        // this item the way `status` backstops the K8s agent-health lookup, so a
        // transient failure under-reports rather than risking a false done.
        apiKnowledgeBase.getKnowledgeBases('').catch(() => undefined),
        // Tenant-wide, one aggregate call across every connected alert-webhook
        // row — `undefined` when none is connected or the call itself failed,
        // so `evaluateOnboarding` falls back to connection status alone rather
        // than downgrading a healthy integration on a lookup that errored.
        alertIntegrationIds.length > 0
          ? apiIntegrations.hasReceivedWebhookEvents(alertIntegrationIds).catch(() => undefined)
          : Promise.resolve(undefined),
      ]),
      // `listCloudAccountFirstSync` never rejects (an internal try/catch
      // resolves `[]` on any failure), so no `.catch` needed here — only the
      // "skip when there is nothing to ask about" guard the other lookups use.
      Promise.all(
        cloudSyncAccountIdsByProvider.map(({ provider, ids }) =>
          ids.length > 0 ? apiKubernetes1.listCloudAccountFirstSync(ids, provider) : Promise.resolve([])
        )
      ),
    ]);

    // Absent entirely (call failed, or no K8s/self-hosted accounts to ask
    // about) means the lookup never resolved — leave it `undefined` so the
    // model falls back to `status` alone rather than stranding every
    // agent-backed account at `partial`.
    let k8sAgentConnected: Record<string, boolean> | undefined;
    const agentRows = (agentHealthResult as { data?: { data?: { agent?: Array<{ cloud_account_id?: string; status?: string }> } } })?.data?.data
      ?.agent;
    if (Array.isArray(agentRows)) {
      k8sAgentConnected = {};
      agentRows.forEach((row) => {
        if (!row?.cloud_account_id) return;
        k8sAgentConnected![row.cloud_account_id] = k8sAgentConnected![row.cloud_account_id] || row.status === AGENT_CONNECTED;
      });
    }

    // `undefined` means the call itself rejected (`.catch(() => undefined)`
    // above) — but `getKnowledgeBases` has its own internal try/catch and
    // structurally never rejects; a real failure instead resolves normally
    // with `{ data: [], errors: [...] }` (issue #24, reopened: the
    // `undefined`-only check below looked like a fix but could never
    // actually fire for this call — only a test mocking a rejection directly
    // could trigger it). Both shapes mean "unknown", not "confirmed zero";
    // `null` matches the model's own contract for that distinction
    // (`skillsEvaluator` already treats `null` as unresolved, `false` as a
    // real zero), the same pattern `k8sAgentConnected` above already uses.
    const kbLookupFailed = kbResult === undefined || (Array.isArray(kbResult?.errors) && kbResult.errors.length > 0);
    const hasKnowledgeBase = kbLookupFailed ? null : Array.isArray(kbResult?.data) && kbResult.data.length > 0;

    // Reduce into a per-account phase. `evaluateCloudFirstSync` is the exact
    // function `CloudFirstSync.tsx`'s own wait screen uses — called once per
    // account here (cheap: the rows are already fetched, this only re-reduces
    // them in memory) with `startedAt: 0` so a result from any time in the
    // past counts, not only one newer than an attempt that never started in
    // this hook the way it does in that dialog.
    const cloudSyncRows = cloudSyncRowsByProvider.flat();
    const cloudSyncState: Record<string, 'waiting' | 'done' | 'failed'> = {};
    cloudSyncAccountIdsByProvider.forEach(({ ids }) => {
      ids.forEach((id) => {
        const { allReported, issues } = evaluateCloudFirstSync([id], cloudSyncRows, 0);
        cloudSyncState[id] = !allReported ? 'waiting' : issues.length > 0 ? 'failed' : 'done';
      });
    });

    return {
      accounts: data.all_accounts,
      integrations: data.integrations,
      messagingPlatforms: data.messaging_platforms,
      k8sAgentConnected,
      cloudSyncState,
      hasKnowledgeBase,
      hasReceivedAlert: hasReceivedAlert ?? null,
    };
  }, []);

  const refresh = React.useCallback(() => {
    return fetchInputs().then((next) => {
      if (next) setInputs(next);
      return next;
    });
  }, [fetchInputs]);

  React.useEffect(() => {
    if (!enabled) return undefined;
    let cancelled = false;
    fetchInputs()
      .then((next) => {
        if (cancelled) return;
        setInputs(next);
        setLoading(false);
      })
      .catch(() => {
        if (cancelled) return;
        setInputs(null);
        setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [fetchInputs, enabled]);

  // Toggling a module changes what is required, not what is connected — so it
  // re-evaluates the payload already fetched rather than going back to the
  // network. The fetch happens once; the chips are free.
  const readiness = React.useMemo(() => (inputs ? evaluateOnboarding({ ...inputs, selectedModules }) : null), [inputs, selectedModules]);

  return { readiness, loading, refresh, accounts: inputs?.accounts ?? [] };
}
