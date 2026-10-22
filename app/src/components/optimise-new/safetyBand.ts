import { type LabelTone } from '@ui/Label';
import { CRITICALITY_TONE, type Criticality } from '@api1/criticality';
import { safeJSONParse } from 'src/utils/common';

// Blast-radius safety band on a recommendation, computed by the knowledge-graph
// impact pipeline (see api-server recommendation/safety_band.go). Mirrors the
// backend string values.
export type SafetyBand = 'safe' | 'review' | 'risky' | 'unknown';

// DependentRef is one blast-radius dependent as persisted by the backend impact
// pipeline (recommendation_impact.go): identity plus risk context. namespace is
// absent for cloud resources; the list is capped server-side at 50 (closest
// first), with dependent_count carrying the true total. relationship/sources
// describe the connecting edge — how this node relates to the resource and
// which discovery sources asserted it.
export interface DependentRef {
  namespace?: string;
  name: string;
  node_type?: string;
  environment?: string;
  hops_away?: number;
  relationship?: string;
  sources?: string[];
  // Hosted-workload rollup annotation: pods of this workload on the seed node.
  pod_count?: number;
  // Curated business-criticality tier, when the workload carries one. Absent
  // means no tier is stated — an ordinary workload, or one that cannot carry a
  // tier at all (only k8s-sourced workloads can). Never read absence as "not
  // important".
  criticality?: Criticality;
  // When an active traffic signal last saw this DIRECT dependency (RFC3339).
  // Absent on indirect dependents and on edges only static sources assert.
  last_observed_at?: string;
}

export interface ImpactSummary {
  dependent_count?: number;
  production_dependents?: number;
  // Regime marker: true when the backend resolved dependent environments
  // against the per-account tiers (cloud_accounts.account_env). Summaries
  // persisted before that existed lack the key — for those, a zero prod count
  // means "environment never resolved", not "verified no production impact".
  environment_resolved?: boolean;
  // Marks that curated criticality tiers were read for this summary. Absent on
  // summaries persisted before criticality existed, so no tier on a dependent
  // there means "never looked up", not "ordinary".
  criticality_resolved?: boolean;
  // When this summary was computed. Ages derived from persisted timestamps are
  // measured against this, never against now: non-Open recommendations are
  // never recomputed, so their summaries freeze here.
  computed_at?: string;
  coverage_confidence?: 'none' | 'low' | 'observed' | 'high';
  truncated?: boolean;
  safety_reason?: string;
  dependents?: DependentRef[];
  // Reverse direction: what the resource itself calls / publishes to /
  // subscribes to (one hop). Context only — excluded from dependent_count and
  // the safety band.
  downstream_count?: number;
  downstream_dependencies?: DependentRef[];
  // Non-caller neighbourhoods (persisted only when present): infrastructure
  // attached to the resource (a volume's instance) and workloads hosted on it
  // (a node's pod-placement rollup). Kept out of dependent_count; a destructive
  // change refuses to soften while either is non-empty.
  infrastructure_count?: number;
  infrastructure_dependents?: DependentRef[];
  hosted_workload_count?: number;
  hosted_workloads?: DependentRef[];
}

// getImpactSummary pulls the blast-radius rollup out of a recommendation's
// finops_score_breakdown, tolerating the JSONB arriving as a string or an object.
export const getImpactSummary = (rec: any): ImpactSummary | null => {
  if (!rec) return null;
  let breakdown = rec.finops_score_breakdown;
  if (typeof breakdown === 'string') breakdown = safeJSONParse(breakdown);
  return (breakdown && breakdown.impact_summary) || null;
};

// Change class stamped by the backend classifier (recommendation/change_class.go):
// what the recommendation does to the resource, the first axis of the verdict.
// Absent on unclassified rules and pre-change-aware summaries.
export type ChangeClass = 'additive' | 'reductive' | 'destructive';

export const getChangeClass = (rec: any): ChangeClass | null => {
  if (!rec) return null;
  let breakdown = rec.finops_score_breakdown;
  if (typeof breakdown === 'string') breakdown = safeJSONParse(breakdown);
  const cls = breakdown && breakdown.change_class;
  return cls === 'additive' || cls === 'reductive' || cls === 'destructive' ? cls : null;
};

// ── Presentation ──────────────────────────────────────────────────────────
// The stored band (safe/review/risky/unknown) is a contract — persisted,
// queried, filtered on, and what the agent's tools take. What people see is
// the action it calls for, not a judgement about applying: production callers
// mean "plan it", not "risky". Red is kept for the one thing that can't be
// planned around — an irreversible change — and only when the graph also
// grades it risky; a dependent-free removal is amber, matching its banner
// (the backend never grades a removal safe, but the chip follows the banner
// even if one arrived that way).
export interface SafetyPresentation {
  key: 'safe' | 'quick_check' | 'plan' | 'irreversible' | 'stops' | 'not_assessed';
  label: string;
  tone: LabelTone;
}

// Rules graded destructive whose action stops the resource and keeps its
// definition, rather than removing it: a scale-down to zero replicas. The band
// treats both the same; the words should not.
export type DestructiveKind = 'removal' | 'stop';
const STOP_RULES = new Set(['abandoned_resource']);
export const destructiveKindOf = (rec: any): DestructiveKind => (STOP_RULES.has(rec?.rule_name) ? 'stop' : 'removal');

export const presentBand = (
  band: string | null | undefined,
  changeClass: ChangeClass | null,
  kind: DestructiveKind = 'removal'
): SafetyPresentation => {
  const b = (band || '').trim().toLowerCase();
  if (changeClass === 'destructive' && (b === 'safe' || b === 'review' || b === 'risky')) {
    const tone: LabelTone = b === 'risky' ? 'critical' : 'warning';
    return kind === 'stop' ? { key: 'stops', label: 'Stops workload', tone } : { key: 'irreversible', label: 'Irreversible', tone };
  }
  switch (b) {
    case 'safe':
      return { key: 'safe', label: 'Safe', tone: 'success' };
    case 'review':
      return { key: 'quick_check', label: 'Quick check', tone: 'info' };
    case 'risky':
      return { key: 'plan', label: 'Plan it', tone: 'warning' };
    default:
      return { key: 'not_assessed', label: 'Not assessed', tone: 'neutral' };
  }
};

// presentRecommendation is the one place a row's presentation is computed:
// band and change class together, so an irreversible change can never render
// as "Plan it" because a caller forgot the second half.
export const presentRecommendation = (rec: any): SafetyPresentation => presentBand(rec?.safety_band, getChangeClass(rec), destructiveKindOf(rec));

// presentBandOnly is for surfaces that have a band but no recommendation —
// filter chips and the legend. An irreversible change is counted under the
// band it was graded into, so these never say "Irreversible".
export const presentBandOnly = (band: string): SafetyPresentation => presentBand(band, null);

const TONE_DOT_COLOR: Record<LabelTone, string> = {
  success: 'var(--ds-green-500)',
  info: 'var(--ds-blue-500)',
  warning: 'var(--ds-amber-500)',
  critical: 'var(--ds-red-500)',
  neutral: 'var(--ds-gray-400)',
};

// safetyDotColor pins a filter chip's dot to its tone even while pressed.
export const safetyDotColor = (tone: LabelTone): string => TONE_DOT_COLOR[tone];

// coverageLabel capitalises a coverage confidence ("high", "observed", …).
export const coverageLabel = (cov?: string): string => (cov ? cov.charAt(0).toUpperCase() + cov.slice(1) : '');

const CHANGE_CLASS_LABEL: Record<ChangeClass, string> = {
  additive: 'Additive',
  reductive: 'Reductive',
  destructive: 'Destructive',
};

export const changeClassLabel = (cls?: ChangeClass | null): string | null => (cls ? CHANGE_CLASS_LABEL[cls] : null);

// deriveVerdict turns the band plus the impact counts into the one-line
// headline above the safety card. The band is the verdict, so the headline
// must never argue with it: production dependents lead the banner only when
// the band actually grades them dangerous — an additive change is Review
// *because* a larger allocation cannot starve them, and a red "N production
// dependents affected" would contradict the reason line right beneath it.
// A success tone means the card renders no banner at all, just the quiet
// reason text.
export const deriveVerdict = (
  band?: string,
  prod?: number,
  depCount?: number,
  truncated?: boolean,
  changeClass?: ChangeClass | null,
  kind: DestructiveKind = 'removal'
): { tone: 'success' | 'warning' | 'critical'; title: string } => {
  const prodCount = prod ?? 0;
  const destructiveTitle = kind === 'stop' ? 'Stops the workload' : 'Irreversible change';
  // Risky reads as a fact to plan around, not an alarm: amber, and "in the
  // blast radius" rather than "affected". Red is reserved for the one case
  // that cannot be planned around — an irreversible change.
  if (band === 'risky') {
    if (changeClass === 'destructive') return { tone: 'critical', title: destructiveTitle };
    if (prodCount > 0) return { tone: 'warning', title: `${prodCount} production dependent${prodCount === 1 ? '' : 's'} in the blast radius` };
    if (truncated) return { tone: 'warning', title: 'Large blast radius' };
    return { tone: 'warning', title: 'Dependents in the blast radius' };
  }
  if (band === 'unknown') return { tone: 'warning', title: 'Not in the dependency graph yet' };
  // Removal is irreversible even when the neighbourhood looks empty, so it
  // keeps a visible caution rather than a green "no known dependents".
  if (changeClass === 'destructive') return { tone: 'warning', title: destructiveTitle };
  // prodCount is checked alongside depCount because the two arrive from the
  // same persisted summary and a partial one must still name the additive
  // case rather than falling through to a generic headline.
  if (changeClass === 'additive' && ((depCount ?? 0) > 0 || prodCount > 0)) {
    return { tone: 'success', title: 'Capacity increase — dependents unaffected' };
  }
  if (depCount === 0 || band === 'safe') return { tone: 'success', title: 'No known dependents' };
  return { tone: 'success', title: 'Contained blast radius' };
};

export const CHANGE_CLASS_HELP: Record<ChangeClass, string> = {
  additive:
    'This change only adds capacity or commitments — dependents cannot be starved by it, so production callers cap the verdict at Quick check instead of Plan it. The remaining risk is apply mechanics (e.g. a rolling restart).',
  reductive: 'This change shrinks or reshapes something callers rely on. Production dependents mean planning the apply.',
  destructive:
    'This change removes the resource and cannot be undone, or stops a workload outright. It never reads Safe; a well-observed, dependent-free neighbourhood needs one glance, never none.',
};

// Relationship → short role chip, from the row's point of view. Upstream rows
// rely on the resource (the blast radius); downstream rows are what the
// resource itself uses. Mirrors backend RelationshipType strings.
const UPSTREAM_ROLE: Record<string, string> = {
  CALLS: 'Caller',
  RUNS_ON: 'Hosted',
  MANAGES: 'Manages it',
  OWNS: 'Owns it',
  MOUNTS: 'Mounts it',
  PUBLISHES_TO: 'Publisher',
  SUBSCRIBES_TO: 'Subscriber',
  PROVIDES_STORAGE: 'Uses storage',
  IS_BOUND_TO: 'Bound to it',
  EXPOSES: 'Exposes it',
  ROUTES_TO_SERVICE: 'Routes to it',
  HOSTED_ON: 'Attached',
};

const DOWNSTREAM_ROLE: Record<string, string> = {
  CALLS: 'Calls',
  PUBLISHES_TO: 'Publishes to',
  SUBSCRIBES_TO: 'Subscribes to',
};

// dependentRoleLabel renders the connecting edge as a role chip; null (no chip)
// when the backend didn't attribute an edge.
export const dependentRoleLabel = (relationship?: string, direction: 'upstream' | 'downstream' = 'upstream'): string | null => {
  if (!relationship) return null;
  if (direction === 'downstream') return DOWNSTREAM_ROLE[relationship] || 'Depends on';
  return UPSTREAM_ROLE[relationship] || 'Dependent';
};

// Provenance buckets, strongest human-readable claim first: what the dependency
// edge is backed by. Buckets mirror the backend discovery source names
// (flow_sources/edge_priority.go).
const PROVENANCE_BUCKETS: Array<{ sources: string[]; label: string }> = [
  { sources: ['manual'], label: 'User-declared' },
  { sources: ['traces', 'gcp-cloud-traces'], label: 'Observed in traces' },
  { sources: ['ebpf'], label: 'Observed traffic' },
  { sources: ['datadog-apm', 'elastic-apm', 'newrelic-apm'], label: 'Observed via APM' },
  { sources: ['k8s', 'aws', 'gcp', 'azure'], label: 'Platform metadata' },
];

// provenanceLabel collapses an edge's discovery sources into one chip label;
// unknown sources (rule/DNS/IP inference) read as 'Inferred'. Matching is
// case-insensitive — the names cross a persistence boundary, and a casing
// drift would otherwise degrade silently to 'Inferred'.
export const provenanceLabel = (sources?: string[]): string | null => {
  if (!sources || sources.length === 0) return null;
  for (const bucket of PROVENANCE_BUCKETS) {
    if (sources.some((s) => bucket.sources.includes(s.toLowerCase()))) return bucket.label;
  }
  return 'Inferred';
};

// Friendly names for raw discovery-source identifiers (edge_priority.go), for
// the coverage hover. Unknown sources render as written.
const SOURCE_DISPLAY: Record<string, string> = {
  ebpf: 'eBPF traffic',
  traces: 'Traces',
  'gcp-cloud-traces': 'GCP Cloud Traces',
  'datadog-apm': 'Datadog APM',
  'elastic-apm': 'Elastic APM',
  'newrelic-apm': 'New Relic APM',
  k8s: 'Kubernetes metadata',
  aws: 'AWS metadata',
  gcp: 'GCP metadata',
  azure: 'Azure metadata',
  manual: 'User-declared',
};

export const formatSourceName = (source: string): string => SOURCE_DISPLAY[source.toLowerCase()] || source;

// impactSignalSources unions the discovery sources persisted across the
// dependency lists — the concrete signals behind the coverage grade, shown on
// hover of the coverage subtitle. Empty for pre-attribution summaries.
export const impactSignalSources = (impact?: ImpactSummary | null): string[] => {
  const seen = new Set<string>();
  for (const dep of [...(impact?.dependents || []), ...(impact?.downstream_dependencies || [])]) {
    for (const source of dep?.sources || []) {
      if (source) seen.add(source.toLowerCase());
    }
  }
  return Array.from(seen)
    .map(formatSourceName)
    .sort((a, b) => a.localeCompare(b));
};

// criticalityTone maps a tier to its chip tone, reusing the one mapping the
// criticality manager renders so the two surfaces cannot drift. The tier
// arrives from persisted JSONB backed by a text column (validated in app code,
// not a DB enum), so an unrecognised value is possible at runtime whatever the
// type says: it falls back to neutral rather than rendering an undefined tone.
export const criticalityTone = (tier?: Criticality): LabelTone => (tier && CRITICALITY_TONE[tier]) || 'neutral';

// criticalityLabel — the chip text. 'medium' never appears (it is the unstored
// default), so a chip marks only an exceptional tier.
export const criticalityLabel = (tier?: Criticality): string | null => {
  if (!tier || tier === 'medium') return null;
  return tier.charAt(0).toUpperCase() + tier.slice(1);
};

// businessCriticalCount counts dependents carrying a critical/high tier. A
// positive claim only: the denominator is deliberately not reported, because
// dependents that cannot carry a tier are indistinguishable from untiered ones
// and any "N of M" would overstate what was actually assessed.
export const businessCriticalCount = (deps?: DependentRef[]): number =>
  (deps || []).filter((d) => d.criticality === 'critical' || d.criticality === 'high').length;

const DAY_MS = 24 * 60 * 60 * 1000;
// Flow edges are tombstoned after this many days without traffic; a snapshot
// older than that can no longer vouch for what it lists.
const EDGE_STALE_DAYS = 7;

// observedAgeLabel renders how long before the summary was computed a traffic
// signal last saw a direct dependency — "Last seen 5d ago". Null when: no
// observation, the age is under two days (hourly builds plus the signal's
// lookback make finer precision meaningless), or the snapshot itself is older
// than the tombstone window, at which point the listing may already be wrong.
export const observedAgeLabel = (dep: DependentRef, computedAt?: string): string | null => {
  if (!dep.last_observed_at || !computedAt) return null;
  const seen = Date.parse(dep.last_observed_at);
  const computed = Date.parse(computedAt);
  if (Number.isNaN(seen) || Number.isNaN(computed)) return null;
  if (Date.now() - computed > EDGE_STALE_DAYS * DAY_MS) return null;
  const days = Math.floor((computed - seen) / DAY_MS);
  if (days < 2) return null;
  return `Last seen ${days}d ago`;
};

export const OBSERVED_AGE_HELP =
  'How long before this assessment an active traffic signal (eBPF, traces or APM) last saw this dependency. A window, not an instant — dependencies stay listed for up to 7 days after traffic stops, so an old sighting may mean the dependency is already gone.';

export const CRITICALITY_HELP =
  'How business-critical this workload is, curated in Settings → Workload Criticality. Informational: it explains why a dependent matters, but it does not change the safety verdict.';

// isProdEnvironment mirrors the backend's isProdEnv so prod dependents get the
// critical treatment consistently.
export const isProdEnvironment = (env?: string): boolean => ['prod', 'production', 'prd'].includes((env || '').trim().toLowerCase());

// prodChipState decides what the production-dependents chip may claim.
// 'prod' — production dependents exist (critical). 'verified-zero' — the
// backend resolved environments and found none (success). 'unknown' — a
// pre-resolution summary, where zero is absence of data, not evidence
// (neutral). Non-Open recommendations are never recomputed, so 'unknown'
// summaries persist indefinitely and must not render as a verified zero.
export const prodChipState = (impact?: ImpactSummary | null): 'prod' | 'verified-zero' | 'unknown' | null => {
  if (impact?.production_dependents == null) return null;
  if (impact.production_dependents > 0) return 'prod';
  return impact.environment_resolved ? 'verified-zero' : 'unknown';
};

export const PROD_CHIP_HELP =
  'A dependent counts as production when it runs in an account marked Production (Settings → Accounts) or carries a production environment label — the label wins. Unresolved external callers are never assumed production.';

export const ENV_UNKNOWN_HELP =
  'This assessment predates environment resolution, so a zero here means "environment unknown", not "no production impact". It refreshes automatically while the recommendation is Open.';

// dependentCountNoun names what the headline count actually counts: a compute
// instance's blast radius is the pods it hosts, not "services".
export const dependentCountNoun = (deps?: DependentRef[]): string => {
  if (deps && deps.length > 0 && deps.every((d) => d.node_type === 'Pod')) return 'Dependent pods';
  return 'Dependent services';
};

export const DEPENDENT_COUNT_HELP =
  'Resources that depend on this one directly. A "+" means the blast radius exceeded the traversal cap, so the true count is higher; the list itself is capped at 50, closest first.';

// nodeTypeLabel renders a dependent's kind chip; ExternalService nodes are
// unresolved IPs and say so instead of masquerading as a known service.
export const nodeTypeLabel = (nodeType?: string): string | null => {
  if (!nodeType) return null;
  return nodeType === 'ExternalService' ? 'External (unresolved)' : nodeType;
};

// Graph-coverage presentation — phrase, subtitle, ⓘ copy, and the section-level
// explainer. Mirrors backend CoverageConfidence tiers (knowledge_graph core
// impact.go): none / low / observed / high.
// Said the way an engineer would say it out loud: how many things saw this, and
// whether they agree. "Corroborated by 2+ sources" was the grader's phrasing,
// not the reader's.
const COVERAGE_SUBTITLE: Record<string, string> = {
  high: 'two or more signals agree',
  observed: 'one live signal watching',
  low: 'one source, not cross-checked',
};
export const coverageSubtitle = (cov?: string): string | null => (cov && COVERAGE_SUBTITLE[cov]) || null;

// coveragePhrase names the reading itself. 'none' gets a phrase of its own —
// "None coverage" is not something anyone says, and the honest reading is that
// the resource is missing from the graph entirely.
export const coveragePhrase = (cov?: string): string | null => {
  if (!cov) return null;
  return cov === 'none' ? 'not in the dependency graph' : `${coverageLabel(cov)} coverage`;
};

export const COVERAGE_HELP =
  'How many independent signals saw these dependencies, and whether they agree. High = two or more signals agree; Observed = one live traffic signal (eBPF, traces or APM) is watching this scope; Low = a single source, never cross-checked against a second; None = the resource was not found in the graph.';

// coverageIsCaveat separates a coverage reading that undercuts the card's own
// claim — single-source, or the resource missing from the graph — from one that
// supports it. Only the former earns an alert.
export const coverageIsCaveat = (cov?: string): boolean => cov === 'low' || cov === 'none';

// coverageExplainer — the "Why coverage is X" banner under the section. Null
// when no caveat is needed (high coverage). Copy varies with whether any
// dependents were found: low + none-found is the "nobody was looking" caveat,
// observed + none-found is honest negative evidence.
export const coverageExplainer = (cov?: string, dependentCount?: number): string | null => {
  switch (cov) {
    case 'low':
      return dependentCount
        ? 'The dependencies we found each come from a single source and have not been cross-checked against a second signal (e.g. traces plus platform metadata). Treat this list as indicative, not exhaustive — there may be callers the graph has not seen yet.'
        : 'Nothing points at this resource, but no active traffic signal was watching it either — absence of evidence, not evidence of absence.';
    case 'observed':
      return dependentCount
        ? 'These dependencies come from an active traffic signal, but have not been corroborated by a second independent source.'
        : 'An active traffic signal (eBPF, traces, or APM) watches this scope and reports nothing calling this resource.';
    case 'none':
      return 'This resource was not found in the dependency graph, so impact is unknown.';
    default:
      return null;
  }
};

// hasBlastRadiusData decides whether a recommendation renders the Blast Radius
// & Safety card at all: a persisted band, or any part of an impact rollup.
export const hasBlastRadiusData = (rec: any): boolean => {
  if (!rec) return false;
  if (rec.safety_band) return true;
  const impact = getImpactSummary(rec);
  return !!(impact && (impact.dependent_count != null || impact.production_dependents != null || impact.coverage_confidence || impact.safety_reason));
};

// Per-band copy. `why` explains the reading; `action` is what to do about it.
// The banner shows the action alone — the headline above it already states the
// reason, and the backend's reason string restates that headline — while the
// chip tooltip shows both.
export const SAFETY_BAND_COPY: Record<SafetyPresentation['key'], { why: string; action: string }> = {
  safe: { why: 'No dependents were found and the graph is well-observed.', action: 'Apply now.' },
  quick_check: {
    why: 'Dependents exist but none look production, the change only adds capacity, or nothing was found but graph coverage is limited.',
    action: 'A glance at the dependents, then apply.',
  },
  plan: {
    why: 'Production callers are in the blast radius, or the blast radius is very large.',
    action: 'Apply with a safeguard — a no-restart apply, a maintenance window, or a heads-up to the owner.',
  },
  irreversible: {
    why: "This change removes the resource and can't be undone.",
    action: 'Take a last look at what still points at it, and snapshot first where the resource supports it.',
  },
  stops: {
    why: 'This stops the workload: its pods go, the manifest stays, so it can be scaled back up.',
    action: 'Take a last look at what still calls it.',
  },
  not_assessed: {
    why: "This resource isn't in the dependency graph yet, so impact isn't assessed.",
    action: 'Apply as you normally would; the reading appears once the graph sees it.',
  },
};

// quietSafetyReason is the backend's reason line, shown under a verdict calm
// enough to need no banner. Most of those reasons restate the summary line word
// for word ("12 dependent(s); none detected as production"), so only the
// additive ones survive: there the reason explains why dependents the summary
// counts do not raise the band, which nothing else on the card says.
export const quietSafetyReason = (
  impact: ImpactSummary | null | undefined,
  tone: 'success' | 'warning' | 'critical',
  changeClass: ChangeClass | null
): string | null => {
  if (!impact?.safety_reason || tone !== 'success') return null;
  return changeClass === 'additive' ? impact.safety_reason : null;
};

// A trait that reads the same on most rows of a list says nothing about any one
// row: "Caller" on all thirty callers, "Workload" under a heading that already
// says workloads, one provenance under a coverage grade that already names it.
// commonTraits names the value each trait settles on so the list can state it
// once in its heading and the rows can show only what differs.
export interface CommonTraits {
  nodeType: string | null;
  role: string | null;
  provenance: string | null;
}

// A value has to cover at least two rows to be worth hoisting — in a list where
// every row differs, the trait is exactly the per-row information we want kept.
const MIN_SHARED_ROWS = 2;

const modalLabel = (deps: DependentRef[], pick: (d: DependentRef) => string | null): string | null => {
  const counts = new Map<string, number>();
  for (const dep of deps) {
    const label = pick(dep);
    if (label) counts.set(label, (counts.get(label) || 0) + 1);
  }
  let best: string | null = null;
  let bestCount = 0;
  for (const [label, count] of counts) {
    if (count > bestCount) {
      best = label;
      bestCount = count;
    }
  }
  return bestCount >= MIN_SHARED_ROWS ? best : null;
};

export const commonTraits = (deps: DependentRef[], direction: 'upstream' | 'downstream' = 'upstream'): CommonTraits => ({
  nodeType: modalLabel(deps, (d) => nodeTypeLabel(d.node_type)),
  role: modalLabel(deps, (d) => dependentRoleLabel(d.relationship, direction)),
  provenance: modalLabel(deps, (d) => provenanceLabel(d.sources)),
});

// sharedTraitText is what the list's heading says instead of repeating the
// hoisted traits on every row. It reports "mostly" unless every row really
// carries all of them — a heading that overstates would make the rows without
// chips read as something they are not.
export const sharedTraitText = (deps: DependentRef[], direction: 'upstream' | 'downstream' = 'upstream'): string | null => {
  const traits = commonTraits(deps, direction);
  const shared = [traits.nodeType, traits.role, traits.provenance].filter(Boolean) as string[];
  if (shared.length === 0) return null;
  const everyRow = deps.every(
    (d) =>
      (!traits.nodeType || nodeTypeLabel(d.node_type) === traits.nodeType) &&
      (!traits.role || dependentRoleLabel(d.relationship, direction) === traits.role) &&
      (!traits.provenance || provenanceLabel(d.sources) === traits.provenance)
  );
  return `${everyRow ? '' : 'mostly '}${shared.join(' · ')}`;
};

// sortByRisk orders dependents most-at-risk first — production, then curated
// criticality — and otherwise keeps the backend order (closest first). Only the
// first few rows show before "Show more", so those must be the ones that decide
// how the change is applied.
const CRITICALITY_RANK: Record<string, number> = { critical: 0, high: 1 };
const riskRank = (d: DependentRef): number => (isProdEnvironment(d.environment) ? 0 : 10) + (CRITICALITY_RANK[d.criticality || ''] ?? 2);

export const sortByRisk = (deps: DependentRef[]): DependentRef[] =>
  deps
    .map((d, i) => ({ d, i }))
    .sort((a, b) => riskRank(a.d) - riskRank(b.d) || a.i - b.i)
    .map((x) => x.d);

// Anything still hanging off the resource in any direction — what it fronts or
// serves, what runs on it, what is attached to it — holds a destructive change
// at Risky, and the backend grades it on exactly that set (deriveDestructiveBand's
// neighborhoodEmpty in recommendation/safety_band.go). So on a destructive
// change these lists open with the card: they are the evidence for the verdict,
// not context. Everywhere else they start collapsed behind the summary.
export const neighbourhoodStartsOpen = (changeClass: ChangeClass | null, impact?: ImpactSummary | null): boolean => {
  if (changeClass !== 'destructive') return false;
  const downstream = impact?.downstream_count ?? impact?.downstream_dependencies?.length ?? 0;
  const hosted = impact?.hosted_workload_count ?? impact?.hosted_workloads?.length ?? 0;
  const attached = impact?.infrastructure_count ?? impact?.infrastructure_dependents?.length ?? 0;
  return downstream + hosted + attached > 0;
};

const plural = (n: number, noun: string): string => `${n} ${noun}${n === 1 ? '' : 's'}`;

// neighbourhoodSummary is the one-line label on the collapsed neighbourhood
// lists — what the resource calls, what runs on it, what is attached to it —
// none of which is a caller that breaks. Null when every list is empty.
export const neighbourhoodSummary = (impact?: ImpactSummary | null): string | null => {
  if (!impact) return null;
  const downstream = impact.downstream_count ?? impact.downstream_dependencies?.length ?? 0;
  const hosted = impact.hosted_workload_count ?? impact.hosted_workloads?.length ?? 0;
  const attached = impact.infrastructure_count ?? impact.infrastructure_dependents?.length ?? 0;
  const parts: string[] = [];
  if (downstream > 0) parts.push(`${downstream} it depends on`);
  if (hosted > 0) parts.push(`${plural(hosted, 'workload')} running here`);
  if (attached > 0) parts.push(`${plural(attached, 'attached resource')}`);
  return parts.length > 0 ? `Also in the neighbourhood: ${parts.join(' · ')}` : null;
};

// safetyChipFor is the one reading of a recommendation's safety for the chip at
// the top of the Details tab: the band presented as the action it calls for,
// with the why, the what-to-do and the change class behind it on hover. Null
// when the graph has not assessed the recommendation, so nothing is claimed.
export const safetyChipFor = (rec: any): { label: string; tone: LabelTone; help: string } | null => {
  if (!rec?.safety_band) return null;
  const presented = presentRecommendation(rec);
  const cls = getChangeClass(rec);
  const help = [
    SAFETY_BAND_COPY[presented.key].why,
    SAFETY_BAND_COPY[presented.key].action,
    cls ? `${changeClassLabel(cls)} change — ${CHANGE_CLASS_HELP[cls]}` : '',
  ]
    .filter(Boolean)
    .join(' ');
  return { label: presented.label, tone: presented.tone, help };
};
