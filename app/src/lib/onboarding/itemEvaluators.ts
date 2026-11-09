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
  /** Per chat integration type (normalized), whether the tenant has a default
   *  channel for it — see `chatDefaultChannelByType`. `null` until the lookup
   *  resolves; a type missing from the map is unknown, never "no channel". */
  chatDefaultChannel: Record<string, boolean> | null;
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
  /** Chat `connectedBy` labels installed with no default channel — populated
   *  only by `chatEvaluator`. */
  missingDefaultChannel?: string[];
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
 * Chat is `providerMatchEvaluator` plus the step an install alone leaves
 * open: picking a default channel. A notification no rule routes goes to each
 * app's default channel, so an app without one has nowhere to post it — yet
 * the install alone used to check the whole item off, and nothing on Setup
 * said a step was left. Each such app's chip moves `done` → `partial`, and
 * worst-wins carries that to the item. Unknown (`null`, or a type missing
 * from the map) never downgrades anything.
 */
export const chatEvaluator: ItemEvaluator = (ctx, item) => {
  const base = providerMatchEvaluator(ctx, item);
  const defaults = ctx.chatDefaultChannel;
  if (!defaults || !base.connectedByState) return base;
  const missing = item.providers
    .filter((p) => base.connectedByState?.[p.label] === 'done' && (p.types || []).every((t) => defaults[ctx.normalize(t)] === false))
    .map((p) => p.label);
  if (missing.length === 0) return base;
  const connectedByState = { ...base.connectedByState };
  missing.forEach((label) => {
    connectedByState[label] = 'partial';
  });
  const state = Object.values(connectedByState).reduce<OnboardingState>((acc, s) => ctx.worst(acc, s), 'blank');
  return { ...base, state, connectedByState, missingDefaultChannel: missing };
};

/** A chat integration row as `listIntegrations` returns it — only the fields read here. */
export interface ChatIntegrationRow {
  type?: string;
  status?: string;
  integration_config_values?: unknown;
}

const configOf = (raw: unknown): Record<string, unknown> => {
  try {
    const arr = typeof raw === 'string' ? JSON.parse(raw) : raw;
    return Array.isArray(arr) ? Object.fromEntries(arr.map((c: { name?: string; value?: unknown }) => [c?.name, c?.value])) : {};
  } catch {
    return {};
  }
};

// A legacy `messaging_platforms.channels` value: a JSON string or object —
// `{id, name}` for Slack/Discord, `{team_id, channels: [...]}` for Teams.
const legacyHasChannel = (raw: unknown): boolean => {
  let v = raw;
  if (typeof v === 'string') {
    try {
      v = JSON.parse(v);
    } catch {
      return false;
    }
  }
  if (Array.isArray(v)) return v.some(legacyHasChannel);
  if (!v || typeof v !== 'object') return false;
  const obj = v as { id?: unknown; name?: unknown; channels?: unknown };
  if ('channels' in obj) return legacyHasChannel(obj.channels);
  return Boolean(obj.id || obj.name);
};

/**
 * Whether each chat platform has a default channel, by integration type.
 * Mirrors how notifications-server resolves one, so this can't call a
 * platform ready that the sender would skip: `_default_channel_shape`
 * (Slack/Discord need `default_channel_id`; Teams needs `default_team_id`
 * and `default_channel_id`) and `_resolve_default_gchat_space` (a single
 * bound space is the implicit default, otherwise the one flagged
 * `is_default`). Integration rows win over legacy `messaging_platforms` rows
 * for the same platform, the same precedence `getMessagingInstallations` uses.
 */
export const chatDefaultChannelByType = (
  integrationRows: ChatIntegrationRow[],
  legacyPlatforms: Array<{ platform?: string; channels?: unknown }>
): Record<string, boolean> => {
  const result: Record<string, boolean> = {};
  const live = integrationRows.filter((r) => r && typeof r.type === 'string' && r.status !== 'disabled');
  const byType: Record<string, Record<string, unknown>[]> = {};
  live.forEach((r) => {
    (byType[r.type!.toLowerCase()] ||= []).push(configOf(r.integration_config_values));
  });
  Object.entries(byType).forEach(([type, cfgs]) => {
    if (type === 'google_chat_space') result[type] = cfgs.length === 1 || cfgs.some((c) => c.is_default === 'true');
    // Per installation, as the sender resolves it: a second workspace with no
    // default must not hide behind the first one's.
    else if (type === 'ms_teams') result[type] = cfgs.every((c) => Boolean(c.default_team_id && c.default_channel_id));
    else result[type] = cfgs.every((c) => Boolean(c.default_channel_id));
  });
  legacyPlatforms.forEach((m) => {
    const type = typeof m?.platform === 'string' ? m.platform.toLowerCase() : '';
    if (!type || byType[type]) return;
    result[type] = (result[type] ?? false) || legacyHasChannel(m.channels);
  });
  return result;
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
  chat: chatEvaluator,
};

export const evaluatorFor = (itemId: string): ItemEvaluator => ITEM_EVALUATORS[itemId] ?? providerMatchEvaluator;
