import { type LabelTone } from '@ui/Label';
import { safeJSONParse } from 'src/utils/common';

// Blast-radius safety band on a recommendation, computed by the knowledge-graph
// impact pipeline (see api-server recommendation/safety_band.go). Mirrors the
// backend string values.
export type SafetyBand = 'safe' | 'review' | 'risky' | 'unknown';

// DependentRef is one blast-radius dependent as persisted by the backend impact
// pipeline (recommendation_impact.go): identity plus risk context. namespace is
// absent for cloud resources; the list is capped server-side at 50 (closest
// first), with dependent_count carrying the true total.
export interface DependentRef {
  namespace?: string;
  name: string;
  node_type?: string;
  environment?: string;
  hops_away?: number;
}

export interface ImpactSummary {
  dependent_count?: number;
  production_dependents?: number;
  coverage_confidence?: 'none' | 'low' | 'high';
  truncated?: boolean;
  safety_reason?: string;
  dependents?: DependentRef[];
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
