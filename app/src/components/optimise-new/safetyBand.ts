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
}

export interface ImpactSummary {
  dependent_count?: number;
  production_dependents?: number;
  coverage_confidence?: 'none' | 'low' | 'observed' | 'high';
  truncated?: boolean;
  safety_reason?: string;
  dependents?: DependentRef[];
  // Reverse direction: what the resource itself calls / publishes to /
  // subscribes to (one hop). Context only — excluded from dependent_count and
  // the safety band.
  downstream_count?: number;
  downstream_dependencies?: DependentRef[];
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
// unknown sources (rule/DNS/IP inference) read as 'Inferred'.
export const provenanceLabel = (sources?: string[]): string | null => {
  if (!sources || sources.length === 0) return null;
  for (const bucket of PROVENANCE_BUCKETS) {
    if (sources.some((s) => bucket.sources.includes(s))) return bucket.label;
  }
  return 'Inferred';
};

// isProdEnvironment mirrors the backend's isProdEnv so prod dependents get the
// critical treatment consistently.
export const isProdEnvironment = (env?: string): boolean => ['prod', 'production', 'prd'].includes((env || '').trim().toLowerCase());

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
