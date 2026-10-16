/**
 * How each item answers "is this connected?".
 *
 * Almost every item answers it the same way: match the providers it declares in
 * the registry against the integrations the tenant actually has. That is
 * `providerMatchEvaluator`, and it is the default — so **adding a provider-backed
 * item needs no code at all**, just a registry entry.
 *
 * `ITEM_EVALUATORS` holds the exceptions: items whose connectedness cannot be
 * derived that way. Today that is `account` alone, which reads cloud accounts
 * and the K8s/self-hosted agent-health table rather than the integrations map.
 *
 * A guard test enforces the invariant that matters: every live item is
 * evaluatable — it either names an evaluator here, or declares providers the
 * default can match. An item that does neither would render permanently blank
 * with no way to satisfy it.
 */
import { accountRowState, type OnboardingAccount, type OnboardingItemDef, type OnboardingState } from '@lib/onboardingReadiness';
import { ONBOARDING_CLOUD_PROVIDERS } from './setupRegistry';

/** Everything an evaluator is allowed to look at. */
export interface EvaluatorContext {
  /** Non-demo accounts on a provider we recognise, already filtered. */
  realAccounts: OnboardingAccount[];
  /** `cloud_provider` values of those accounts, e.g. ['AWS', 'K8s']. */
  connectedProviders: string[];
  /** Integration type → its state, unioned with the messaging-platform list. */
  stateByType: Map<string, OnboardingState>;
  /** Integration types with at least one row a person added (not `source: 'agent'`). */
  userManagedTypes: Set<string>;
  /** Per telemetry item id, the integration types flagged as its default provider. */
  defaultTypesBySignal: Record<string, Set<string>>;
  /** K8s and self-hosted-VM agent health by account id; null until the
   * lookup resolves. See `accountRowState`'s own doc comment for why the
   * name stayed `k8sAgentConnected` after self-hosted coverage (#41115). */
  k8sAgentConnected: Record<string, boolean> | null;
  /** AWS/GCP/Azure first-cost-sync phase by account id; null until the
   * lookup resolves. See `accountRowState`'s own doc comment — unlike
   * `k8sAgentConnected`, this one never reaches `SetupGateContext`'s lock. */
  cloudSyncState: Record<string, 'waiting' | 'done' | 'failed'> | null;
  /** Whether the tenant has at least one knowledge base, tenant-wide. `null`
   * until the lookup resolves — never inferred from AI/LLM being connected,
   * which is a prerequisite panel, not the same fact. */
  hasKnowledgeBase: boolean | null;
  /** Whether any connected alert-webhook integration has ever received an
   * event, tenant-wide. `null` until the lookup resolves or no alert
   * integration is connected — the `alerts` item then falls back to
   * connection status alone, the same anti-flakiness fallback
   * `accountRowState` uses for `k8sAgentConnected`. */
  hasReceivedAlert: boolean | null;
  /** Worst-wins comparison, so one failure is never hidden by a healthy sibling. */
  worst: (a: OnboardingState, b: OnboardingState) => OnboardingState;
  /** Maps a raw status string to a state; shared so the rules stay in one place. */
  stateOfStatus: (status: unknown) => OnboardingState;
  normalize: (value: unknown) => string;
}

/** What an evaluator returns — the state plus what it is connected by. */
export interface ItemVerdict {
  state: OnboardingState;
  connectedBy: string[];
  /** Each `connectedBy` label's own state — see `OnboardingItemStatus`'s
   *  own doc comment for why this exists (known issue #23). */
  connectedByState?: Record<string, OnboardingState>;
  /** True when the agent found it without the user configuring anything. */
  autoDetected: boolean;
  /** `connectedBy` labels flagged as the default provider for this item. */
  defaultBy?: string[];
  /** The real account rows behind `connectedBy` — populated only by
   *  `accountEvaluator`. A provider-label chip can't tell two AWS accounts
   *  apart, or say which one is still waiting on its agent; this can. */
  accounts?: ItemAccountRow[];
}

/** One connected account, named and stated for a per-account chip. */
export interface ItemAccountRow {
  id: string;
  label: string;
  cloudProvider: string;
  state: OnboardingState;
}

export type ItemEvaluator = (ctx: EvaluatorContext, item: OnboardingItemDef) => ItemVerdict;

/**
 * The default. An item is connected when any provider it declares appears in the
 * tenant's integrations; its state is the worst across everything that matched,
 * so a failing connection is never masked by a healthy one.
 */
export const providerMatchEvaluator: ItemEvaluator = (ctx, item) => {
  const matched = item.providers.filter((p) => (p.types || []).some((t) => ctx.stateByType.has(ctx.normalize(t))));
  const stateOfProvider = (p: (typeof matched)[number]): OnboardingState =>
    (p.types || []).reduce<OnboardingState>((s, t) => ctx.worst(s, ctx.stateByType.get(ctx.normalize(t)) ?? 'blank'), 'blank');
  const matchedState = matched.reduce<OnboardingState>((acc, p) => ctx.worst(acc, stateOfProvider(p)), 'blank');
  // Per-provider, not just the item's aggregate — a healthy Loki chip must not
  // render critical just because its sibling Elasticsearch integration is
  // broken (known issue #23). Keyed by label to match `connectedBy` itself.
  const connectedByState: Record<string, OnboardingState> = {};
  matched.forEach((p) => {
    connectedByState[p.label] = stateOfProvider(p);
  });

  if (matched.length === 0) return { state: 'blank', connectedBy: [], autoDetected: false };

  // "Auto-detected" is what the agent created, read off the row's own `source`
  // — never inferred from the account's resolved default, which also reports a
  // person's own Prometheus/Loki/Datadog. It holds only while no matched
  // provider has a person-added row, so a manual source is always listed.
  const isUserManaged = (p: (typeof matched)[number]) => (p.types || []).some((t) => ctx.userManagedTypes.has(ctx.normalize(t)));
  const autoDetected = !matched.some(isUserManaged);

  const defaultTypes = ctx.defaultTypesBySignal[item.id];
  const defaultBy = defaultTypes ? matched.filter((p) => (p.types || []).some((t) => defaultTypes.has(ctx.normalize(t)))).map((p) => p.label) : [];

  return { state: matchedState, connectedBy: matched.map((p) => p.label), connectedByState, autoDetected, defaultBy };
};

/**
 * Alerts is `providerMatchEvaluator` plus one more fact it cannot see: a
 * webhook URL existing (even one GCP creates on its own with zero user
 * action) is not the same as anything having actually been POSTed to it.
 * Reusing the base evaluator keeps the matching/label/auto-detected logic in
 * one place; this only ever moves a `done` down to `partial` once, and only
 * when the webhook-events lookup has positively confirmed zero.
 *
 * `hasReceivedAlert === null` (lookup unresolved, or nothing to check yet)
 * leaves `done` as `done` rather than stranding it at `partial` forever —
 * the same anti-flakiness fallback `accountRowState` uses for a K8s agent
 * health check that never ran.
 */
export const alertsEvaluator: ItemEvaluator = (ctx, item) => {
  const base = providerMatchEvaluator(ctx, item);
  if (base.state !== 'done' || ctx.hasReceivedAlert !== false) return base;
  return { ...base, state: 'partial' };
};

/**
 * Accounts are the one item the integrations map cannot answer for.
 *
 * The item's own `state` answers "has an account actually finished
 * connecting" — the setup milestone. A row existing is not enough on its
 * own: a K8s account whose in-cluster agent, or a self-hosted account whose
 * forager, has never checked in is not a milestone reached, so it must not
 * check the whole item off (this was this function's own earlier, wrong
 * answer for K8s — see known-issues.md; #41115 is the same gap for
 * self-hosted).
 *
 * `state` is `done` the moment *any one* account's own `rowState` is `done`
 * — not every account. A second account still pending or broken is real and
 * stays visible per-row (`accounts`, below), but it must not hold the whole
 * item at "not done" once one account genuinely works, the same way a
 * second broken integration on another item doesn't un-connect a healthy
 * one. Only when nothing has succeeded yet does worst-wins decide what to
 * show (a real error surfaces over a merely-pending row).
 */
export const accountEvaluator: ItemEvaluator = (ctx) => {
  if (ctx.realAccounts.length === 0) return { state: 'blank', connectedBy: [], autoDetected: false };

  // Per-row — feeds the per-account chip, the provider tile's "Connected"
  // badge, and now the aggregate `state` above too (all three must agree on
  // what "this one account" actually is). `accountRowState` is shared with
  // `SetupGateContext`'s own lock, so there is exactly one such definition —
  // though `cloudSyncState` itself never reaches the lock (see its own
  // comment in `onboardingReadiness.ts`).
  const rowState = (a: OnboardingAccount): OnboardingState => accountRowState(a, ctx.k8sAgentConnected, ctx.cloudSyncState);

  const state = ctx.realAccounts.some((a) => rowState(a) === 'done')
    ? 'done'
    : ctx.realAccounts.reduce<OnboardingState>((acc, a) => ctx.worst(acc, rowState(a)), 'blank');

  // `connectedProviders` holds `cloud_provider` codes ('K8s', 'SelfHosted', …) —
  // every other evaluator's `connectedBy` is display labels, which is what the
  // generic ProviderTile/ProviderPicker "Connected" badge compares against
  // (`connectedLabels.includes(p.label)`). AWS/GCP/Azure's code and label are
  // identical strings, which is what let this go unnoticed — K8s ('K8s' vs
  // 'Kubernetes') and Self-hosted ('SelfHosted' vs 'Self-hosted') never matched,
  // so those two tiles never showed as connected even once truly connected.
  const connectedBy = ONBOARDING_CLOUD_PROVIDERS.filter((p) => ctx.connectedProviders.includes(p.provider)).map((p) => p.label);

  // One row per real account, not per provider — lets the card name which
  // specific account (of several on the same provider) is still pending,
  // which a single "AWS" provider-label chip can't.
  const accounts: ItemAccountRow[] = ctx.realAccounts.map((a, i) => ({
    id: a.id || `${ctx.normalize(a.cloud_provider)}-${i}`,
    label: a.account_name || 'Untitled',
    cloudProvider: a.cloud_provider || '',
    state: rowState(a),
  }));

  return { state, connectedBy, autoDetected: false, accounts };
};

/**
 * Skills is Knowledge Base under different copy — the onboarding item names
 * the capability in the user's terms ("tell it once what 'prod' means here"),
 * the product feature underneath is the one already shipped in
 * `KnowledgeBaseTab.jsx` (`apiKnowledgeBase.getKnowledgeBases`, tenant-wide).
 * No provider row can match this the way telemetry/integrations do — a
 * knowledge base is not an `integrations_list` row — so it needs its own
 * evaluator rather than a registry entry.
 *
 * Strict on purpose: `done` only when a real knowledge base exists for a real
 * account, never inferred from the `ai` panel (its own prerequisite) being
 * connected. Having an LLM configured is not having written anything down for
 * it to use — those are two different facts, and treating the first as proof
 * of the second is exactly the false-green this item must not show.
 */
export const skillsEvaluator: ItemEvaluator = (ctx) => {
  if (ctx.hasKnowledgeBase === null) return { state: 'blank', connectedBy: [], autoDetected: false };
  return {
    state: ctx.hasKnowledgeBase ? 'done' : 'blank',
    connectedBy: ctx.hasKnowledgeBase ? ['Knowledge base'] : [],
    autoDetected: false,
  };
};

/** Exceptions only. Anything absent here uses `providerMatchEvaluator`. */
export const ITEM_EVALUATORS: Record<string, ItemEvaluator> = {
  account: accountEvaluator,
  skills: skillsEvaluator,
  alerts: alertsEvaluator,
};

export const evaluatorFor = (itemId: string): ItemEvaluator => ITEM_EVALUATORS[itemId] ?? providerMatchEvaluator;
