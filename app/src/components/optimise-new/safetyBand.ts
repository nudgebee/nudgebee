import { type LabelTone } from '@ui/Label';
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
}

export interface ImpactSummary {
  dependent_count?: number;
  production_dependents?: number;
  // Regime marker: true when the backend resolved dependent environments
  // against the per-account tiers (cloud_accounts.account_env). Summaries
  // persisted before that existed lack the key — for those, a zero prod count
  // means "environment never resolved", not "verified no production impact".
  environment_resolved?: boolean;
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

const BAND_TONE: Record<SafetyBand, LabelTone> = {
  safe: 'success',
  review: 'warning',
  risky: 'critical',
  unknown: 'neutral',
};

// safetyBandTone maps a band to a DS status tone (safe→success, review→warning,
// risky→critical, unknown/missing→neutral).
export const safetyBandTone = (band?: string): LabelTone => BAND_TONE[(band || 'unknown') as SafetyBand] ?? 'neutral';

// safetyBandLabel renders the band as a capitalised word ("Safe", "Risky", …).
export const safetyBandLabel = (band?: string): string => (band ? band.charAt(0).toUpperCase() + band.slice(1) : '');

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

const CHANGE_CLASS_PRESENTATION: Record<ChangeClass, { label: string; tone: LabelTone }> = {
  additive: { label: 'Additive', tone: 'success' },
  reductive: { label: 'Reductive', tone: 'warning' },
  destructive: { label: 'Destructive', tone: 'critical' },
};

export const changeClassLabel = (cls?: ChangeClass | null): string | null => (cls ? CHANGE_CLASS_PRESENTATION[cls].label : null);
export const changeClassTone = (cls?: ChangeClass | null): LabelTone => (cls ? CHANGE_CLASS_PRESENTATION[cls].tone : 'neutral');

export const CHANGE_CLASS_HELP: Record<ChangeClass, string> = {
  additive:
    'This change only adds capacity or commitments — dependents cannot be starved by it, so production callers cap the verdict at Review instead of Risky. The remaining risk is apply mechanics (e.g. a rolling restart).',
  reductive: 'This change shrinks or reshapes something callers rely on. Production dependents make it Risky.',
  destructive:
    'This change removes the resource and cannot be undone. The verdict floors at Risky; a well-observed, dependent-free neighbourhood earns Review — never Safe.',
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

// proximityLabel — 'Direct' for a one-hop dependent, 'N hops' when reached
// through intermediaries.
export const proximityLabel = (hops?: number): string | null => {
  if (hops == null || hops <= 0) return null;
  return hops === 1 ? 'Direct' : `${hops} hops`;
};

// Provenance buckets, strongest human-readable claim first: what the dependency
// edge is backed by. Buckets mirror the backend discovery source names
// (flow_sources/edge_priority.go).
const PROVENANCE_BUCKETS: Array<{ sources: string[]; label: string }> = [
  { sources: ['manual'], label: 'User-declared' },
  { sources: ['traces', 'gcp-cloud-traces'], label: 'Observed in traces' },
  { sources: ['ebpf'], label: 'Observed traffic' },
  { sources: ['datadog-apm', 'newrelic-apm'], label: 'Observed via APM' },
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

// isProdEnvironment mirrors the backend's isProdEnv so prod dependents get the
// critical treatment consistently.
export const isProdEnvironment = (env?: string): boolean => ['prod', 'production', 'prd'].includes((env || '').trim().toLowerCase());

// formatEnvironment prettifies the account-tier spellings for chips; anything
// else (a free-form workload label like "staging") renders as written.
export const formatEnvironment = (env: string): string => {
  const normalized = env.trim().toLowerCase();
  if (normalized === 'prod') return 'Production';
  if (normalized === 'non_prod' || normalized === 'non-prod') return 'Non-production';
  return env;
};

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

// nodeTypeLabel renders a dependent's kind chip; ExternalService nodes are
// unresolved IPs and say so instead of masquerading as a known service.
export const nodeTypeLabel = (nodeType?: string): string | null => {
  if (!nodeType) return null;
  return nodeType === 'ExternalService' ? 'External (unresolved)' : nodeType;
};

// Graph-coverage presentation — tone, chip subtitle, ⓘ copy, and the
// section-level explainer. Mirrors backend CoverageConfidence tiers
// (knowledge_graph core impact.go): none / low / observed / high.
export const coverageTone = (cov?: string): LabelTone =>
  cov === 'high' ? 'success' : cov === 'observed' ? 'info' : cov === 'low' ? 'warning' : 'neutral';

const COVERAGE_SUBTITLE: Record<string, string> = {
  high: 'Corroborated by 2+ sources',
  observed: 'Single signal, actively watching',
  low: 'Based on a single source',
  none: 'Not found in the graph',
};
export const coverageSubtitle = (cov?: string): string | null => (cov && COVERAGE_SUBTITLE[cov]) || null;

export const COVERAGE_HELP =
  'How many independent signals confirm these dependencies. High = corroborated by 2+ sources; Observed = an active traffic signal (eBPF, traces, or APM) watches this scope; Low = single-source and unverified; None = the resource was not found in the graph.';

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
