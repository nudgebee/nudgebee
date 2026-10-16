/**
 * Onboarding readiness — the single source of truth behind the home-screen
 * Setup Center (`@components/onboarding/SetupCenter`).
 *
 * Before this module the home page carried a hand-written checklist that never
 * read any state: it offered the same four "connect your messaging tool" rows
 * whether or not Slack was already connected, and it only ever appeared when a
 * tenant had exactly one account (the demo). Everything here is derived from
 * `apiAccount.getAllAccount()` — the same round-trip the Integrations admin page
 * already makes — so the checklist cannot drift from reality.
 *
 * The shape is six outcome-named panels, each holding items. A panel is named
 * for what the customer gets, not for the integration category underneath it,
 * because the old flat ladder asked people to care about our taxonomy before
 * they had any reason to.
 *
 * Scoring is deliberately tenant-wide, not per-account: an observability or LLM
 * provider connected once serves the tenant, and a per-account matrix is a
 * different (larger) surface.
 *
 * Pure functions only — no React, no network. The UI decides how to render.
 */

import { ITEMS, MODULES, ONBOARDING_CLOUD_PROVIDERS, REGISTRY_PANELS } from './onboarding/setupRegistry';

// Re-exported so every existing call site keeps importing it from here; the
// table itself now lives with the rest of the registry data.
export { MODULES, ONBOARDING_CLOUD_PROVIDERS };
import { evaluatorFor, type ItemAccountRow } from './onboarding/itemEvaluators';

export type { ItemAccountRow };

// Integration `type` values as they come back from `integrations_list`. Matching
// is case-insensitive because the backend is not internally consistent here
// (`ES` is upper-case, every sibling is lower-case), and route slugs diverge
// from types in several places (`otel` → `otel_clickhouse`, `msteams` → `ms_teams`).
export interface OnboardingAccount {
  id?: string;
  account_name?: string;
  cloud_provider?: string;
  status?: string;
  /** Used only to tell a fresh account's agent-still-installing state from an
   *  established one's real agent outage — see `isEstablished` in SetupGateContext. */
  created_at?: string;
}

export interface OnboardingIntegration {
  id?: string;
  type?: string;
  status?: string;
  name?: string;
  /** `'agent'` for a row the cluster agent created on its own; anything else
   *  (or absent) was added by a person. */
  source?: string;
  /** Per linked account, carrying the `default_*_provider` flags. */
  integrations_cloud_accounts?: unknown;
}

export interface OnboardingMessagingPlatform {
  platform?: string;
  channels?: unknown;
}

export interface EvaluateOnboardingInput {
  accounts?: OnboardingAccount[] | null;
  integrations?: OnboardingIntegration[] | null;
  messagingPlatforms?: OnboardingMessagingPlatform[] | null;
  /**
   * Module ids the customer selected, e.g. `['ts', 'opt']`. Absent or empty
   * behaves as **every module selected**, so the card is never mysteriously
   * empty and a tenant who has expressed no preference sees the fullest view.
   */
  selectedModules?: string[] | null;
  /**
   * Agent-connection status for K8s clusters *and* self-hosted VM fleets,
   * keyed by account id — `true` once `listK8sAccAgentHealth(ids, null)`
   * reports the agent `CONNECTED` for that account, `false` once the health
   * table has been checked and says otherwise. A `cloud_accounts` row exists
   * the instant the account is created; the agent (in-cluster, or a forager
   * on a self-hosted fleet) still has to check in before there is anything
   * to watch, so neither kind of account is `done` on `status` alone (#41115
   * for self-hosted — it used to read `done` with no forager ever started).
   * Omit (or omit an id from the map) when the health lookup has not
   * resolved — that account then falls back to `status` alone, the same as
   * every other provider, rather than being stuck at `partial` on a lookup
   * that never ran. The name predates self-hosted coverage; kept to avoid
   * a mechanical rename across every file that threads it through.
   */
  k8sAgentConnected?: Record<string, boolean> | null;
  /**
   * Whether the tenant has at least one knowledge base, checked with one
   * tenant-wide `getKnowledgeBases('')` call — see `useOnboardingReadiness`'s
   * own fetch. Omit when the check has not resolved; `null`/absent reads as
   * "unknown", not "none exists" — the `skills` item must never show
   * connected on an assumption.
   */
  hasKnowledgeBase?: boolean | null;
  /**
   * Whether any alert-webhook integration has ever received an event, checked
   * tenant-wide across every connected alert-type integration row — see
   * `useOnboardingReadiness`'s own fetch. Omit when the check has not
   * resolved or no alert integration is connected; `null`/absent reads as
   * "unknown" and the `alerts` item falls back to its connection status
   * alone, same as `k8sAgentConnected` when that lookup hasn't run.
   */
  hasReceivedAlert?: boolean | null;
  /**
   * First-cost-sync phase for AWS/GCP/Azure accounts, keyed by account id —
   * see `useOnboardingReadiness`'s own fetch, which reduces
   * `listCloudAccountFirstSync` with `evaluateCloudFirstSync` (the same
   * function `CloudFirstSync.tsx`'s own wait screen uses) per account,
   * `startedAt: 0` so any past result counts rather than only one newer than
   * some attempt that never happened here. Omit an id (or the whole map) when
   * unresolved — that account then falls back to `status` alone, same as
   * every other provider, rather than being stuck at `partial` forever on a
   * lookup that never ran.
   */
  cloudSyncState?: Record<string, 'waiting' | 'done' | 'failed'> | null;
}

/** Essential panels gate the product's core loop; recommended panels widen it. */
export type OnboardingTier = 'essential' | 'recommended';

/**
 * The four states, and only four. "Auto-detected" is not a fifth — it is `done`
 * carrying a chip, because a fifth glyph would make the legend unlearnable.
 */
export type OnboardingState = 'done' | 'partial' | 'error' | 'blank';

/** How much a module needs an item. `na` still renders, faded but clickable. */
export type NeedLevel = 'req' | 'opt' | 'na';

export interface OnboardingProviderDef {
  /** `?cloudProvider=` value on /accounts/account-form — the real deep link. */
  route: string;
  label: string;
  /** Integration `type` values that satisfy this provider. */
  types?: string[];
  /** What this provider covers, e.g. 'EC2, ECS, RDS, S3, EKS'. Tiles only. */
  blurb?: string;
  /**
   * Whether this provider's account picker offers AWS/GCP/Azure/CloudFoundry
   * accounts, not just Kubernetes/Self-hosted. Mirrors `CLOUD_CAPABLE_INTEGRATIONS`
   * in `IntegrationDynamicFormModal.jsx` exactly — keep the two in sync. Absent/false
   * means Kubernetes and Self-hosted only (either the source needs an in-cluster
   * relay agent, or it's K8s-only by product decision — see that file's own
   * comments for which). Used to prioritise tiles the tenant can actually use
   * over ones it can't yet, never to hide a provider outright.
   */
  cloudCapable?: boolean;
}

export interface OnboardingItemDef {
  id: string;
  name: string;
  /** Scored. An item that is neither required nor planned is a bonus. */
  required?: boolean;
  /** Never drags a panel backwards, and never counted in a denominator. */
  optional?: boolean;
  /** Not built yet: rendered as Planned, no action, excluded from every count. */
  planned?: boolean;
  /**
   * Kept in the registry — evaluator, copy, everything — but not rendered
   * anywhere and not present in `PANELS`. For an item like Preferences/Rules
   * that has a real backend but no settled answer for what "connected" even
   * means inside a per-account checklist (per-user? tenant-wide? — see
   * `docs/setup-center-build-spec.md` §9): showing it half-decided, even
   * tagged Planned, invites the exact "why doesn't this work" question
   * `planned` alone can't answer. Distinct from `planned`, which still
   * renders (inert) — `hidden` renders nothing at all. Drop it once the
   * scoping question is settled, rather than deleting the item.
   */
  hidden?: boolean;
  /** Satisfies the same need as a required sibling (a gateway instead of a provider). */
  alt?: boolean;
  /** No alternatives to choose between, so the row opens its form directly. */
  direct?: boolean;
  /**
   * Product modules this item is required by — display-only, rendered as the
   * "Required by" chips behind the disclosure (§3.2/§5 of the spec). Not the
   * Phase 2 need-matrix: no req/opt/na level, just which modules care at all.
   */
  modules?: string[];
  /** "Used by <modules> to <capability>" — answers why and who in one line. */
  purpose: string;
  why: string;
  /** [label, value] pairs — When and Effort, shown behind the row's disclosure. */
  meta: Array<[string, string]>;
  unlocks?: string[];
  /** [surface, what stays worse] — the cost of skipping, per product surface. */
  impact: Array<[string, string]>;
  /**
   * How much each module needs this item: `req` counts toward progress, `opt` is
   * tagged Optional, `na` renders faded but stays clickable. Keyed by module id.
   *
   * Read only when module chips ship (Phase 2b); carried now so the matrix lives
   * with the rest of the item's data rather than arriving as a separate table
   * later. An item's effective level is the **strongest across the modules the
   * user selected**, so `na` for one module never hides something another needs.
   *
   * Deliberately separate from account-type filtering, which answers a different
   * question — "is this even possible for this account" rather than "does this
   * customer want it". The two are layered, never merged.
   */
  need?: Record<string, 'req' | 'opt' | 'na'>;
  providers: OnboardingProviderDef[];
}

export interface OnboardingPanelDef {
  id: string;
  title: string;
  /** The outcome, in the customer's words. Rendered under the title. */
  sub: string;
  tier: OnboardingTier;
  /** Render the tier label on the panel header. Accounts only — see PANELS. */
  tagTier?: boolean;
  /** Required items are satisfied-by-any rather than all-of. */
  anyOf?: boolean;
  /** Prerequisite panel id. Panel 4 is meaningless before panel 3 exists. */
  needs?: string;
  /**
   * True when this panel's item *is* the panel — skip the item's own name
   * line, since the panel header already says it (Accounts and clusters /
   * Cluster or cloud account). An explicit flag rather than inferring it
   * from `items.length === 1`: `smarter` can *reach* one visible item too,
   * once `hidden` items are filtered out of `items` below, and its panel
   * title ("Make your AI agents smarter") does not name Skills the way
   * Accounts' does — inferring compact from count would wrongly swallow
   * Skills' own name line.
   */
  compact?: boolean;
  items: OnboardingItemDef[];
}

export interface OnboardingItemStatus extends Omit<OnboardingItemDef, 'need'> {
  state: OnboardingState;
  /** Provider labels actually connected, e.g. ['Prometheus']. */
  connectedBy: string[];
  /** Each `connectedBy` label's own state, where an evaluator can tell them
   *  apart (`providerMatchEvaluator` only — one broken provider must not
   *  color its healthy sibling's chip, known issue #23). Absent, not empty,
   *  where an evaluator has only one aggregate state to give (e.g. Skills). */
  connectedByState?: Record<string, OnboardingState>;
  /** True when every connected provider was created by the agent, none by a
   *  person — renders a chip, not a state. */
  autoDetected: boolean;
  /** The `connectedBy` labels flagged as the default provider for this signal. */
  defaultBy?: string[];
  /** The real account rows behind `connectedBy` — populated only on the
   *  `account` item. See `ItemAccountRow`. */
  accounts?: ItemAccountRow[];
  /**
   * How much the **selected** modules need this item — the strongest level
   * across them, so `na` for one module never hides what another requires.
   *
   * Distinct from `required`, and the two legitimately disagree. `required` is
   * structural: what has to be connected for *this panel* to read done.
   * `need` is what the customer's chosen modules actually use. Chat is
   * `required` in the delivery panel yet `opt` for every module; traces is
   * structurally optional in telemetry yet `req` for Troubleshoot. Panel state
   * reads the first, progress counts the second.
   */
  need: NeedLevel;
}

export interface OnboardingPanelStatus extends Omit<OnboardingPanelDef, 'items'> {
  items: OnboardingItemStatus[];
  state: OnboardingState;
  /** True while the panel's prerequisite is unmet. */
  blocked: boolean;
  requiredTotal: number;
  requiredDone: number;
  /**
   * False when nothing in the panel can be connected yet, because every item is
   * still `planned`. Such a panel renders — so its shape is not a surprise when
   * the feature lands — but stays out of every denominator: counting a panel
   * the customer is unable to act on would hold the progress bar short of 100%
   * forever and keep the card on screen for a tenant who has finished.
   */
}

export interface OnboardingReadiness {
  panels: OnboardingPanelStatus[];
  /** True once at least one non-demo cloud account or cluster exists. */
  hasCloudAccount: boolean;
  /** Cloud providers already connected, e.g. ['AWS', 'K8s']. */
  connectedProviders: string[];
  /** Panels, for the "N essential panels left" line. */
  essentialTotal: number;
  essentialDone: number;
  /**
   * Required **items** for the selected modules — the unit the progress bar and
   * the banner both count in. Items, not panels: a panel whose items are all
   * `planned` simply contributes nothing, with no special case needed.
   */
  total: number;
  done: number;
  /** 0-100, rounded, over required items. */
  percent: number;
  /** True when nothing at all is connected — the true first-login state. */
  isFreshTenant: boolean;
  /**
   * The `total - done` slice of the same required-items list, in registry
   * order — what the progress banner names. Derived from the identical
   * `requiredItems` filter that produces `done`/`total`, not a second copy of
   * it: a banner and a progress bar computing "what's still required" two
   * different ways is exactly how the account-count mismatch (known issues
   * #10) happened, and there is no reason to grow a second instance of it.
   */
  outstandingRequired: OnboardingItemStatus[];
}

/**
 * Follow-ups that a provider needs *after* the account exists, keyed by
 * `cloud_provider`. These are surfaced as hints on the accounts panel rather
 * than scored items: whether a CUR is actually attached is not visible in
 * `accounts_list`, so claiming "done" or "missing" here would be a guess. They
 * exist because today the AWS/GCP billing warning is shown once inside the
 * create modal (`ValidationResultBanner`) and then never again — an account can
 * sit for months with every cost surface silently empty.
 *
 * `cost` marks a follow-up that only matters to a customer doing cost work, so
 * the UI can weight it once module selection lands rather than shouting billing
 * at an SRE who will never open the Cost tab.
 */
export const PROVIDER_FOLLOW_UPS: Record<string, Array<{ text: string; cost?: boolean }>> = {
  AWS: [
    {
      text: 'Attach a Cost & Usage Report under Edit Billing Config — spend, rightsizing and cost recommendations stay empty without it.',
      cost: true,
    },
    { text: 'Connect EventBridge to receive AWS health and change events in real time.' },
  ],
  GCP: [
    { text: 'Set the BigQuery billing export under Edit Billing Config — cost recommendations stay empty without it.', cost: true },
    { text: 'Enable real-time alerts to receive Cloud Monitoring events as they fire.' },
  ],
  Azure: [{ text: 'Connect Event Grid to receive Azure Monitor events in real time.' }],
  K8s: [{ text: 'Confirm the in-cluster agent reports Prometheus and Alertmanager as connected on the account tile.' }],
};

// Provider lists, ordered by how often tenants actually connect them. The card
// shows the leading few as tiles and puts the rest behind one dropdown, so the
// order here is load-bearing, not cosmetic.

/**
 * The six panels, resolved from the registry.
 *
 * The data lives in `./onboarding/setupRegistry` — this is the shape the rest of
 * the app already consumes, with each panel's item ids swapped for the item
 * objects they name. Re-ordering panels or moving an item between them is an
 * edit to the registry, not to any component.
 *
 * An unknown id is dropped rather than thrown on: a half-finished registry edit
 * should cost one row, not the whole card. The guard tests fail on it in CI.
 *
 * A `hidden` item is dropped here too, on purpose — not just skipped by
 * `SetupCenter.tsx`'s own render loop. Filtering this early means every
 * consumer of `PANELS`/`evaluateOnboarding()` (today's card, tomorrow's
 * admin overview, a test) sees the same reduced set without each one having
 * to remember the rule; the item itself stays fully defined in `ITEMS`,
 * `hidden` is the only thing that changes.
 */
export const PANELS: OnboardingPanelDef[] = REGISTRY_PANELS.map((panel) => ({
  ...panel,
  items: panel.items.map((id) => ITEMS[id]).filter((item): item is OnboardingItemDef => Boolean(item) && !item.hidden),
}));

const normalize = (value: unknown): string => (typeof value === 'string' ? value.trim().toLowerCase() : '');

/**
 * `integrations.status` is a closed set — `enabled` | `error` | `disabled`,
 * enforced by a foreign key to `integration_statuses`. Accounts use their own
 * vocabulary: `active` | `pending` | `inactive` | `failed`.
 *
 * `error` is legal on an integration but **nothing in the backend ever writes
 * it**: every INSERT hardcodes `enabled`, and both UPDATE paths are the admin's
 * own enable/disable toggle. So the arm below is correct and simply never fires
 * today. It stays because the moment a health probe lands it becomes live, and
 * because the alternative — reading `enabled` as healthy — would paint a green
 * check over an integration whose credential expired months ago.
 *
 * Rows with no status at all are real connections from before the column was
 * populated, and must not be dropped for want of a field.
 */
const stateOfStatus = (status: unknown): OnboardingState => {
  const s = normalize(status);
  if (s === 'error' || s === 'failed') return 'error';
  if (s === 'pending' || s === 'connecting') return 'partial';
  if (s === 'disabled' || s === 'inactive' || s === 'deleted') return 'blank';
  return 'done';
};

/** Providers whose account row needs its own agent to have checked in before
 *  counting as connected, not just added — K8s's in-cluster agent and a
 *  self-hosted account's forager (#41115: the row used to read `done` the
 *  instant it was created, with nothing collected and no agent in sight).
 *  Every other provider's status is the whole answer, since there is no
 *  separate agent of its own to wait on. */
const AGENT_CHECKED_PROVIDERS = new Set(['k8s', 'selfhosted']);

/** Providers whose account row needs its first cost sync to have reported
 *  before counting as fully connected — the sync is what the row's own
 *  `status` cannot see for itself (it goes `done` the instant the account is
 *  created, same gap #41115 closed for the agent-backed providers above, just
 *  on a background job instead of an agent). */
const CLOUD_SYNC_CHECKED_PROVIDERS = new Set(['aws', 'gcp', 'azure']);

/**
 * Whether one account row counts as actually connected — not merely added.
 * A K8s or self-hosted row needs its own agent (in-cluster, or a forager on
 * a self-hosted VM fleet) to have checked in; an AWS/GCP/Azure row needs its
 * first cost sync to have reported; every other provider's status is the
 * whole answer (nothing else has an "installing" step of its own to wait on).
 *
 * Exported so there is exactly one definition of "is this account row
 * done", not two that can quietly disagree: `accountEvaluator`
 * (`itemEvaluators.ts`) uses it for the Accounts item's per-row and
 * aggregate state, and `SetupGateContext` uses it for the `/setup` lock
 * itself.
 *
 * `cloudSyncState` is deliberately NOT symmetric with `agentConnected` here:
 * `SetupGateContext` calls this with two positional args, never a third, so
 * a cloud account mid-first-sync still unlocks the app — only the agent-
 * backed providers above hold the gate shut. Unlike a K8s/VM agent, a cloud
 * account's resource and event collection runs independently of the cost
 * sync and isn't blocked by it, so there is nothing to protect the lock
 * against; this only has to stop the Setup checklist's own chip claiming
 * "done" before the sync has actually finished.
 *
 * Both lookups absent (the health/sync check failed, or was never asked)
 * fall back to trusting `status` alone, same as `stateOfStatus` on its own —
 * a flaky check must not strand every account at `partial` forever. Agent
 * health is keyed by account id only, not by agent type: the lookup behind it
 * (`listK8sAccAgentHealth(ids, null)`) already covers both kinds in one call,
 * and an account only ever carries agents of its own kind.
 */
export const accountRowState = (
  a: OnboardingAccount,
  agentConnected: Record<string, boolean> | null | undefined,
  cloudSyncState?: Record<string, 'waiting' | 'done' | 'failed'> | null
): OnboardingState => {
  let s = stateOfStatus(a.status);
  if (s === 'done' && agentConnected && a.id && AGENT_CHECKED_PROVIDERS.has(normalize(a.cloud_provider)) && agentConnected[a.id] !== true) {
    s = 'partial';
  }
  if (s === 'done' && cloudSyncState && a.id && CLOUD_SYNC_CHECKED_PROVIDERS.has(normalize(a.cloud_provider))) {
    const sync = cloudSyncState[a.id];
    if (sync === 'waiting') s = 'partial';
    else if (sync === 'failed') s = 'error';
  }
  return s;
};

/** Worst state wins, so one failing connection is never hidden by a healthy sibling. */
const RANK: Record<OnboardingState, number> = { error: 3, partial: 2, done: 1, blank: 0 };
const worst = (a: OnboardingState, b: OnboardingState): OnboardingState => (RANK[b] > RANK[a] ? b : a);

/**
 * The strongest level across the modules the customer selected.
 *
 * Strongest, not weakest: selecting Troubleshoot and Optimize together must
 * surface everything either of them needs. An item `na` for one and `req` for
 * the other is `req` — hiding it would break the module that wants it.
 *
 * No selection behaves as every module selected, so the card is never empty for
 * someone who has expressed no preference. An item with no matrix row at all is
 * treated as `req`, which fails loudly in the UI rather than silently vanishing;
 * a guard test stops one reaching production.
 */
const NEED_RANK: Record<NeedLevel, number> = { req: 3, opt: 2, na: 1 };

export function needOf(item: OnboardingItemDef, selectedModules: string[]): NeedLevel {
  const row = item.need;
  if (!row) return 'req';
  const ids = selectedModules.length > 0 ? selectedModules : MODULES.map((m) => m.id);
  return ids.reduce<NeedLevel>((best, id) => {
    const level = row[id];
    return level && NEED_RANK[level] > NEED_RANK[best] ? level : best;
  }, 'na');
}

/** `integrations_cloud_accounts` flag → the telemetry item id it makes the default for. */
const DEFAULT_FLAG_SIGNAL: Record<string, string> = {
  default_metrics_provider: 'metrics',
  default_log_provider: 'logs',
  default_traces_provider: 'traces',
};

const CLOUD_PROVIDER_BY_NORMALIZED = new Map(ONBOARDING_CLOUD_PROVIDERS.map((p) => [normalize(p.provider), p]));

/**
 * Turn one `getAllAccount()` payload — plus, when it has resolved, the agent's
 * detected telemetry — into the full readiness picture.
 *
 * Tolerant by construction: every input is optional and a malformed row is
 * skipped rather than thrown on. A home page that renders a slightly stale
 * checklist is recoverable; one that white-screens on an unexpected null is not.
 */
export function evaluateOnboarding(input: EvaluateOnboardingInput = {}): OnboardingReadiness {
  const accounts = Array.isArray(input.accounts) ? input.accounts : [];
  const integrations = Array.isArray(input.integrations) ? input.integrations : [];
  const messagingPlatforms = Array.isArray(input.messagingPlatforms) ? input.messagingPlatforms : [];
  const k8sAgentConnected = input.k8sAgentConnected && typeof input.k8sAgentConnected === 'object' ? input.k8sAgentConnected : null;
  const cloudSyncState = input.cloudSyncState && typeof input.cloudSyncState === 'object' ? input.cloudSyncState : null;
  const hasKnowledgeBase = typeof input.hasKnowledgeBase === 'boolean' ? input.hasKnowledgeBase : null;
  const hasReceivedAlert = typeof input.hasReceivedAlert === 'boolean' ? input.hasReceivedAlert : null;
  const selectedModules = Array.isArray(input.selectedModules) ? input.selectedModules.filter((m) => typeof m === 'string') : [];

  // The demo account is injected client-side by `apiHome.getCloudAccounts` and
  // is never a real connection — the old home page treated "exactly one
  // account" as the first-login signal precisely because of it.
  const realAccounts = accounts.filter(
    (a) => a && normalize(a.id) !== 'demo' && CLOUD_PROVIDER_BY_NORMALIZED.has(normalize(a.cloud_provider)) && stateOfStatus(a.status) !== 'blank'
  );

  const connectedProviders = ONBOARDING_CLOUD_PROVIDERS.filter((p) =>
    realAccounts.some((a) => normalize(a.cloud_provider) === normalize(p.provider))
  ).map((p) => p.provider);

  // Messaging arrives from two places — `messagingplatforms_list` (an installed
  // Slack/Teams app) and `integrations_list` — and a tenant can legitimately
  // appear in only one of them, so the sets are unioned. The platform list
  // carries no status, so anything in it is treated as connected.
  const stateByType = new Map<string, OnboardingState>();
  // Types with at least one row a person added — from either source: an
  // `integrations` row with `source !== 'agent'`, or any row in
  // `messagingPlatforms` (install is always a deliberate action there). A
  // type backed only by `source: 'agent'` integration rows, with nothing in
  // `messagingPlatforms` either, is what the Setup page calls "auto-detected".
  const userManagedTypes = new Set<string>();
  // Per telemetry signal, the types whose row is flagged default on a linked account.
  const defaultTypesBySignal: Record<string, Set<string>> = {};
  integrations.forEach((i) => {
    const type = i && normalize(i.type);
    if (!type) return;
    const state = stateOfStatus(i.status);
    if (state === 'blank') return;
    stateByType.set(type, worst(stateByType.get(type) ?? 'done', state));
    if (i.source !== 'agent') userManagedTypes.add(type);
    if (Array.isArray(i.integrations_cloud_accounts)) {
      (i.integrations_cloud_accounts as Array<Record<string, unknown>>).forEach((link) => {
        Object.entries(DEFAULT_FLAG_SIGNAL).forEach(([flag, signal]) => {
          if (link?.[flag] === true) (defaultTypesBySignal[signal] ??= new Set()).add(type);
        });
      });
    }
  });
  messagingPlatforms.forEach((m) => {
    const platform = m && normalize(m.platform);
    if (!platform) return;
    if (!stateByType.has(platform)) stateByType.set(platform, 'done');
    // Installing a messaging app (Slack's "Add to Slack", Teams) is always a
    // deliberate action — unlike a telemetry source, there is no agent path
    // that can install one on its own. This list carries no `source` field to
    // prove that per row the way `integrations` does above, so it's asserted
    // here instead of silently defaulting to "no proof of a person" →
    // "Auto-detected" (review finding: Slack/Teams always read auto-detected,
    // no matter who actually installed them).
    userManagedTypes.add(platform);
  });

  // Everything an evaluator may look at. Built once and shared, so no evaluator
  // can reach past it into the raw payload.
  const ctx = {
    realAccounts,
    connectedProviders,
    stateByType,
    userManagedTypes,
    defaultTypesBySignal,
    k8sAgentConnected,
    cloudSyncState,
    hasKnowledgeBase,
    hasReceivedAlert,
    worst,
    stateOfStatus,
    normalize,
  };

  const evaluateItem = (item: OnboardingItemDef): OnboardingItemStatus => {
    // Planned items have nothing behind them yet. They render so the shape of
    // the panel is not a surprise later, and are excluded from every count.
    const need = needOf(item, selectedModules);
    if (item.planned) return { ...item, need, state: 'blank', connectedBy: [], autoDetected: false };
    return { ...item, need, ...evaluatorFor(item.id)(ctx, item) };
  };

  const panels: OnboardingPanelStatus[] = PANELS.map((panel) => {
    const items = panel.items.map((item) => evaluateItem(item));

    // Only required, non-planned items are scored. An optional extra must never
    // drag a panel backwards, or the bar could never fill for a team that
    // connects traces but not logs.
    const scored = items.filter((i) => i.required && !i.planned);
    const requiredDone = scored.filter((i) => i.state === 'done').length;
    const satisfied = panel.anyOf ? requiredDone > 0 : scored.length > 0 && requiredDone === scored.length;

    // Any item failing wins outright; then anything mid-flight; then the
    // required set being satisfied; then any progress at all.
    const anyError = items.some((i) => i.state === 'error');
    const anyPartial = items.some((i) => i.state === 'partial');
    const anyProgress = items.some((i) => i.state === 'done');
    let state: OnboardingState = 'blank';
    if (anyError) state = 'error';
    else if (anyPartial) state = 'partial';
    else if (scored.length > 0 && satisfied) state = 'done';
    else if (anyProgress) state = 'partial';

    return { ...panel, items, state, blocked: false, requiredTotal: scored.length, requiredDone };
  });

  // A panel whose prerequisite is unmet is blocked, not failed: panel 4 asks you
  // to teach agents about your environment, which means nothing before panel 3
  // gives them a model to think with.
  const stateById = new Map(panels.map((p) => [p.id, p.state]));
  panels.forEach((p) => {
    if (p.needs) p.blocked = stateById.get(p.needs) !== 'done';
  });

  // Progress counts required **items** for the selected modules, not panels.
  //
  // This retires the `scorable` special case the panel count needed: a panel
  // whose items are all `planned` now contributes nothing on its own, because
  // planned items are excluded here. No panel has to be held out of a
  // denominator to stop the bar stranding short of 100%.
  //
  // An `alt` item (e.g. the AI gateway) never itself carries `need: 'req'` —
  // by design, see its own registry entry — so it's never in this list on its
  // own. Left alone, that means connecting only the alt path satisfies the
  // panel (`satisfied`/`state` above already handle that) but this count
  // stays stuck on the required sibling it replaced, which never becomes
  // `done` on its own (Mayank Pande's review: AI reads "required" forever
  // once only the gateway is connected). Credit that sibling as done here —
  // in the copies this list holds, not in `panels` itself — whenever the
  // panel's alt path is done, so the item's own row stays honest about what
  // is actually connected while the global tally reflects what's actually
  // outstanding.
  const requiredItems = panels.flatMap((p) => {
    const altSatisfied = p.anyOf && p.items.some((i) => i.alt && i.state === 'done');
    return p.items
      .filter((i) => i.need === 'req' && !i.planned)
      .map((i) => (altSatisfied && i.required && i.state !== 'done' ? { ...i, state: 'done' as OnboardingState } : i));
  });
  const done = requiredItems.filter((i) => i.state === 'done').length;
  const total = requiredItems.length;

  // Panels, still — this is the "N essential panels left" line, which talks
  // about panels and should keep doing so.
  const essentials = panels.filter((p) => p.tier === 'essential' && p.items.some((i) => i.required && !i.planned));

  return {
    panels,
    hasCloudAccount: realAccounts.length > 0,
    connectedProviders,
    essentialTotal: essentials.length,
    essentialDone: essentials.filter((p) => p.state === 'done').length,
    total,
    done,
    outstandingRequired: requiredItems.filter((i) => i.state !== 'done'),
    percent: total === 0 ? 0 : Math.round((done / total) * 100),
    isFreshTenant: panels.every((p) => p.state === 'blank'),
  };
}
