/**
 * The one place that turns `apiAccount.getAllAccount()` plus the agent's own
 * telemetry detection into an `OnboardingReadiness` snapshot — shared by
 * `SetupCenter` (the checklist) and `SetupProgressBanner` (the `/home`
 * reminder), so the two can never show a different "X of N" for the same
 * tenant at the same moment. They used to: the banner read a cheaper,
 * accounts-and-integrations-only snapshot off `SetupGateContext` that never
 * saw K8s agent health, auto-detected telemetry or Knowledge Base, so a K8s
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
 * still far cheaper than this hook's own fetch, since it skips detection,
 * Knowledge Base, and everything else this hook also asks for. This hook
 * stays "how much is left", fetched only where that is actually shown.
 */
import * as React from 'react';
import apiAccount from '@api1/account';
import apiKubernetes1 from '@api1/kubernetes1';
import apiKnowledgeBase from '@api1/knowledge-base';
import { AGENT_CONNECTED } from '@components/integrations/pendingAgentInstall';
import {
  evaluateOnboarding,
  type DetectedSignals,
  type EvaluateOnboardingInput,
  type OnboardingAccount,
  type OnboardingIntegration,
  type OnboardingMessagingPlatform,
  type OnboardingReadiness,
} from '@lib/onboardingReadiness';

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

/**
 * `observability_list_provider_capabilities` is per-account, so detection costs
 * one request per account asked, on every `/home` load (via
 * `SetupProgressBanner`) as well as `/setup` — this is not a one-off cost.
 * Scoring is tenant-wide (a signal found anywhere counts), so a cap here is a
 * real tradeoff, not a free one: a tenant whose only working account for a
 * given signal sits beyond this position still reads "not connected",
 * indefinitely (known issue #17). Raised from 10 to reduce how often that
 * happens for realistic tenant sizes; still a cap, not a fix — see the doc.
 */
const DETECT_ACCOUNT_LIMIT = 30;

/**
 * Every signal the capabilities call can return — the backend asks exactly
 * these three per account — and the only telemetry item ids the evaluators
 * read detection for.
 */
const DETECTABLE_SIGNALS = ['metrics', 'logs', 'traces'];

/**
 * Accounts asked before the rest. The first account to report a signal wins,
 * so once all of `DETECTABLE_SIGNALS` have been seen, asking further accounts
 * cannot change the result. An established tenant usually has them all on its
 * first few accounts, which keeps its `/home` load at this many requests
 * instead of `DETECT_ACCOUNT_LIMIT`. The per-user GraphQL rate limit is a
 * fixed per-minute count, so what matters is how many requests go out, not how
 * many are in flight at once. When a signal is still missing, the rest go out
 * together, so the worst case costs one extra round trip, not one per batch.
 */
const DETECT_FIRST_WAVE = 5;

type CapabilitiesPayload = { observability_list_provider_capabilities?: Array<Record<string, unknown>> } | undefined;

const fetchCapabilities = (accountIds: string[]): Promise<CapabilitiesPayload[]> =>
  Promise.all(
    accountIds.map((id) =>
      apiAccount
        .listProviderCapabilities({ account_id: id })
        .then((res: unknown) => (res as { data?: { data?: CapabilitiesPayload } })?.data?.data)
        .catch(() => undefined)
    )
  );

async function detectSignals(accountIds: string[]): Promise<DetectedSignals> {
  const detectedSignals: DetectedSignals = {};
  // Account order is kept across both waves, so "first account to report a
  // signal wins" picks the same provider it would with every account asked.
  const record = (payloads: CapabilitiesPayload[]) =>
    payloads.forEach((payload) => {
      (payload?.observability_list_provider_capabilities || []).forEach((entry) => {
        const signal = typeof entry?.provider_type === 'string' ? entry.provider_type : '';
        const provider = typeof entry?.provider === 'string' ? entry.provider : '';
        // Presence is the whole signal: the backend returns an entry only for
        // a provider that actually resolves. First account to report a signal
        // wins — scoring is tenant-wide.
        if (signal && provider && !detectedSignals[signal]) detectedSignals[signal] = provider;
      });
    });

  record(await fetchCapabilities(accountIds.slice(0, DETECT_FIRST_WAVE)));
  if (accountIds.length > DETECT_FIRST_WAVE && DETECTABLE_SIGNALS.some((signal) => !detectedSignals[signal])) {
    record(await fetchCapabilities(accountIds.slice(DETECT_FIRST_WAVE)));
  }
  return detectedSignals;
}

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

    // Telemetry the agent resolved on its own has no integration row, so a
    // fully instrumented Kubernetes tenant reads as "not connected" without
    // this second call. Failures here are silent on purpose: detection is an
    // enhancement, and the panel still works from integration rows alone.
    const accountIds = (data.all_accounts || [])
      .map((a) => a?.id)
      .filter((id): id is string => typeof id === 'string' && id.length > 0 && id !== 'demo')
      .slice(0, DETECT_ACCOUNT_LIMIT);

    // A `cloud_accounts` row goes in the instant the K8s add-account wizard
    // submits step 1 — well before the agent has checked in. Reading `status`
    // alone would mark the account (and the whole panel) done before the
    // install command has even run, so it is checked against the same
    // agent-health table the wizard's own "waiting for agent" step and the
    // global pending-install banner already trust. One batched call, filtered
    // to K8s agents — unlike the per-account telemetry lookup below, there is
    // no per-request cost to cap.
    const k8sAccountIds = (data.all_accounts || [])
      .filter(
        (a): a is OnboardingAccount & { id: string } =>
          typeof a?.id === 'string' &&
          a.id.length > 0 &&
          a.id !== 'demo' &&
          typeof a?.cloud_provider === 'string' &&
          a.cloud_provider.trim().toLowerCase() === 'k8s'
      )
      .map((a) => a.id);

    const [detectedSignals, agentHealthResult, kbResult] = await Promise.all([
      detectSignals(accountIds),
      k8sAccountIds.length > 0 ? apiKubernetes1.listK8sAccAgentHealth(k8sAccountIds).catch(() => undefined) : Promise.resolve(undefined),
      // Tenant-wide in one call (empty accountId — same mode SetupCenter's own
      // KB-chip fetch and KnowledgeBaseTab.jsx already use), not one request per
      // account. This used to be a per-account, DETECT_ACCOUNT_LIMIT-capped loop
      // like the capability detection above — a knowledge base is tenant-wide
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
    ]);

    // Absent entirely (call failed, or no K8s accounts to ask about) means the
    // lookup never resolved — leave it `undefined` so the model falls back to
    // `status` alone rather than stranding every K8s account at `partial`.
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

    return {
      accounts: data.all_accounts,
      integrations: data.integrations,
      messagingPlatforms: data.messaging_platforms,
      detectedSignals,
      k8sAgentConnected,
      hasKnowledgeBase,
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
