/**
 * buildInterpretation — assembles the InterpretationPanel slots from values the
 * DetailsPanel has already resolved (catalog title/description, the computed
 * insight, savings) plus the parsed recommendation JSONB. No new data
 * fetching — a pure re-frame so the panel leads with a verdict instead of a
 * generic blurb.
 *
 * pod_right_sizing gets a richer treatment via summarizeRightSizing: a
 * container-named verdict with current→recommended values, a CPU-percentile
 * usage strip, and a reclaimed-capacity impact. Everything else uses the
 * lighter title/insight re-frame and degrades gracefully.
 */
import type { InterpretationData } from './InterpretationPanel';
import { summarizeRightSizing, summarizeCloudRightSizing, formatCloudTargetSpec, type RightSizingSummary } from '../rightSizingData';
import { summarizeConfigIssues } from '../evidence/configIssues';

interface BuildArgs {
  category: string;
  ruleName: string;
  /** Resolved title (catalog title → JSONB title → prettified rule name). */
  title: string;
  /** Resolved description (catalog/JSONB). May be empty for K8s-native rules. */
  description: string;
  /** Service/scope name to show as a chip (e.g. "AWSLambda"). */
  serviceName?: string;
  /** Computed "why this matters" prose (getRecommendationInsight). */
  insight: string;
  /** estimated_savings on the recommendation row. */
  savings: number;
  /** Parsed recommendation JSONB (for richer per-type derivation). */
  recData?: any;
  /** Catalog recommendation steps — the richest "why" text when present. */
  recommendations?: string[];
}

// Rule-family caveats. Kept deliberately small for v1 — only rules where the
// operational consequence is well-defined and worth surfacing up-front.
const RISK_BY_RULE: Record<string, string> = {
  pod_right_sizing: "Applying updates the workload's resource requests and triggers a rolling restart. Reversible by reverting the change.",
  replica_right_sizing: 'Reducing replicas lowers spare capacity for traffic spikes and failures — confirm there is enough headroom before applying.',
  pv_rightsize: "Persistent volumes can't be shrunk in place — this requires creating a new PVC and migrating data, with downtime.",
  unused_pvc: "Deleting a PVC can be irreversible if the volume isn't retained. Confirm nothing depends on it first.",
  abandoned_resource: "Scaling the workload to zero is reversible, but verify it's genuinely unused before applying.",
};

// Action-oriented verdicts for the K8s-native RightSizing rules, which have no
// catalog title (it degrades to the prettified rule name, e.g. "Pod Right
// Sizing"). pod_right_sizing is handled richly below via summarizeRightSizing;
// this covers the rest.
const RIGHTSIZING_VERDICTS: Record<string, string> = {
  replica_right_sizing: 'Reduce the replica count to match observed traffic',
  pv_rightsize: 'Shrink the over-provisioned volume',
  unused_pvc: 'Delete this unused volume',
  abandoned_resource: 'Scale down this idle workload',
};

// K8s-native RightSizing rules handled by their own verdict/risk paths — kept out
// of the generic cloud-instance branch below.
const K8S_NATIVE_RS = new Set(['pod_right_sizing', 'replica_right_sizing', 'pv_rightsize', 'unused_pvc', 'abandoned_resource']);

// Reserved-Instance / Savings-Plan purchase rules — a financial commitment, not a
// reversible resource change, so they get their own verdict + lock-in risk.
const COMMITMENT_RE = /^aws_native_(ce_ri|ce_savings_plan|purchase_reserved|purchase_savings)/;

const CLOUD_RS_RISK = 'Resizing a cloud instance needs a stop/start or replacement — schedule a maintenance window. Reversible by resizing back.';
const COMMITMENT_RISK =
  'A 1–3 year financial commitment, billed whether or not the capacity is used. Confirm the usage is steady-state before purchasing — commitments cannot be cancelled.';
const SPOT_RISK =
  'Spot / preemptible capacity can be reclaimed with ~2 minutes notice. Use only for stateless, replicated, interruption-tolerant workloads.';

// Backend numbers sometimes arrive as strings (e.g. "609.86"); coerce safely.
const num = (v: any): number | null => {
  const n = typeof v === 'string' ? parseFloat(v) : v;
  return typeof n === 'number' && Number.isFinite(n) ? n : null;
};

function deriveVerdict(category: string, ruleName: string, title: string): string {
  if (category === 'RightSizing' && RIGHTSIZING_VERDICTS[ruleName]) return RIGHTSIZING_VERDICTS[ruleName];
  // Cloud config / security / spot / infra titles are already descriptive.
  return title;
}

function deriveImpact(category: string, savings: number): { impact: string; impactIsCost: boolean } {
  if (savings && savings > 0) {
    return { impact: `~$${Math.round(savings)}/mo estimated savings.`, impactIsCost: true };
  }
  switch (category) {
    case 'Security':
      return { impact: 'Security — reduces attack surface. No direct cost.', impactIsCost: false };
    case 'Configuration':
      return { impact: 'Reliability / best-practice alignment. No direct cost.', impactIsCost: false };
    case 'K8sSpotRecommendation':
      return { impact: 'Cost — spot / preemptible capacity is typically 60–90% cheaper than on-demand.', impactIsCost: false };
    case 'RightSizing':
      return { impact: 'Reliability & efficiency. No direct cost change.', impactIsCost: false };
    default:
      return { impact: '', impactIsCost: false };
  }
}

const DIRECTION_VERB: Record<RightSizingSummary['direction'], string> = {
  reduce: 'Over-provisioned — reduce',
  increase: 'Under-provisioned — increase',
  set: 'No requests set — set',
  mixed: 'Right-size',
  none: 'Right-size',
};

function podRightSizing(summary: RightSizingSummary, savings: number): Pick<InterpretationData, 'verdict' | 'impact' | 'impactIsCost' | 'evidence'> {
  const containerLabel = summary.containerCount > 1 ? `${summary.primary} (+${summary.containerCount - 1} more)` : summary.primary;
  const verdict = summary.changeText
    ? `${DIRECTION_VERB[summary.direction]} ${containerLabel}: ${summary.changeText}`
    : 'Right-size resource requests to match observed usage';

  let impact: string;
  let impactIsCost = false;
  if (Math.round(savings) >= 1) {
    impact = `~$${Math.round(savings)}/mo savings${summary.reclaimText ? ` · reclaims ${summary.reclaimText}` : ''}.`;
    impactIsCost = true;
  } else if (summary.reclaimText) {
    impact = `Reclaims ~${summary.reclaimText} of guaranteed allocation. No direct cost change.`;
  } else if (summary.direction === 'increase') {
    impact = 'Prevents CPU throttling and OOM kills. No direct cost change.';
  } else {
    impact = 'Reliability & efficiency. No direct cost change.';
  }

  return { verdict, impact, impactIsCost, evidence: summary.evidence.length ? summary.evidence : undefined };
}

function configVerdict(items: any[]): string {
  const s = summarizeConfigIssues(items);
  const tail = s.hasSecurity ? ' Review the security findings first.' : '';
  if (s.hasLevels) {
    const parts: string[] = [];
    if (s.levelCounts[3]) parts.push(`${s.levelCounts[3]} Critical`);
    if (s.levelCounts[2]) parts.push(`${s.levelCounts[2]} Medium`);
    if (s.levelCounts[1]) parts.push(`${s.levelCounts[1]} Info`);
    return `${s.total} configuration findings — ${parts.join(', ')}.${tail}`;
  }
  // Category shape: list categories with Security first.
  const cats = Object.keys(s.categoryCounts).sort((a, b) => (a === 'Security' ? -1 : b === 'Security' ? 1 : a.localeCompare(b)));
  const uniqueNote = s.unique < s.total ? ` (${s.unique} unique checks)` : '';
  return `${s.total} configuration findings${uniqueNote} across ${cats.join(', ')}.${tail}`;
}

// Cloud instance right-sizing (EC2/RDS underutilized, alternate, generation).
function cloudRightSizing(
  recData: any,
  savings: number
): Pick<InterpretationData, 'verdict' | 'impact' | 'impactIsCost' | 'evidence' | 'risk'> | null {
  const cs = summarizeCloudRightSizing(recData);
  if (!cs || (!cs.targetInstance && cs.avgCpuPct == null && cs.vcpu == null)) return null;

  const spec = formatCloudTargetSpec(cs);
  const target = cs.targetInstance ? `${cs.targetInstance}${spec ? ` (${spec})` : ''}` : spec || 'a smaller instance';
  const cpuLead = cs.avgCpuPct != null ? `CPU averages ${cs.avgCpuPct.toFixed(1)}% — ` : '';
  const verdict = `Underutilized — ${cpuLead}downsize to ${target}`;

  const evidence: { label: string; value: string }[] = [];
  if (cs.avgCpuPct != null) evidence.push({ label: 'CPU avg', value: `${cs.avgCpuPct.toFixed(1)}%` });
  if (cs.maxCpuPct != null) evidence.push({ label: 'CPU max', value: `${cs.maxCpuPct.toFixed(1)}%` });

  const { impact, impactIsCost } = deriveImpact('RightSizing', savings);
  return { verdict, impact, impactIsCost, evidence: evidence.length ? evidence : undefined, risk: CLOUD_RS_RISK };
}

// Reserved Instance / Savings Plan purchase — surface the real $ and the lock-in.
function commitment(ruleName: string, recData: any): Pick<InterpretationData, 'verdict' | 'impact' | 'impactIsCost' | 'risk'> {
  const monthly = num(recData?.estimated_monthly_savings);
  const pct = num(recData?.estimated_savings_percentage);
  const upfront = num(recData?.upfront_cost);
  const count = recData?.recommended_instance_count;
  const inst = recData?.instance_type || recData?.instance_class || recData?.node_type || '';
  const isRI = /ce_ri|purchase_reserved/.test(ruleName);
  const kind = isRI ? 'Reserved Instance' : 'Savings Plan';
  const target = count && inst ? `${count}× ${inst}` : inst;
  const verdict = target ? `Commit to a ${kind} — ${target}` : `Purchase a ${kind} for steady-state usage`;

  let impact: string;
  let impactIsCost = false;
  if (monthly != null && monthly >= 1) {
    const pctPart = pct != null ? ` (${Math.round(pct)}%)` : '';
    const upPart = upfront != null && upfront > 0 ? ` · $${Math.round(upfront).toLocaleString()} upfront` : '';
    impact = `~$${Math.round(monthly).toLocaleString()}/mo savings${pctPart}${upPart}.`;
    impactIsCost = true;
  } else {
    impact = 'Commitment-based pricing reduces the rate on steady-state usage.';
  }
  return { verdict, impact, impactIsCost, risk: COMMITMENT_RISK };
}

export function buildInterpretation({
  category,
  ruleName,
  title,
  description,
  serviceName,
  insight,
  savings,
  recData,
  recommendations,
}: BuildArgs): InterpretationData {
  // "Why" source priority: catalog recommendation step (richest) → catalog/JSONB
  // description → computed insight (best for K8s-native rules with no catalog).
  const whyNow = recommendations?.[0]?.trim() || (description?.trim() ? description : insight) || undefined;
  const sharedRisk = RISK_BY_RULE[ruleName] || undefined;

  // Rich treatment for K8s misconfiguration "array of findings" recs — the
  // findings are the recommendation, so lead with their breakdown.
  if (category === 'Configuration' && Array.isArray(recData)) {
    return {
      verdict: configVerdict(recData),
      serviceName: serviceName || undefined,
      whyNow,
      impact: 'Reliability / security — no direct cost. Fix the highest-severity findings first.',
      impactIsCost: false,
      risk: undefined,
    };
  }

  // Rich treatment for pod right-sizing.
  if (ruleName === 'pod_right_sizing') {
    const summary = recData ? summarizeRightSizing(recData) : null;
    if (summary) {
      const r = podRightSizing(summary, savings || 0);
      return {
        verdict: r.verdict,
        serviceName: serviceName || undefined,
        whyNow,
        impact: r.impact,
        impactIsCost: r.impactIsCost,
        risk: sharedRisk,
        evidence: r.evidence,
      };
    }
  }

  // Reserved Instance / Savings Plan — a financial commitment (must precede the
  // generic cloud-instance branch since both are RightSizing + non-array).
  if (category === 'RightSizing' && COMMITMENT_RE.test(ruleName) && recData && !Array.isArray(recData)) {
    const c = commitment(ruleName, recData);
    return { verdict: c.verdict, serviceName: serviceName || undefined, whyNow, impact: c.impact, impactIsCost: c.impactIsCost, risk: c.risk };
  }

  // Cloud instance right-sizing (EC2/RDS underutilized, alternate, generation).
  if (category === 'RightSizing' && !Array.isArray(recData) && !K8S_NATIVE_RS.has(ruleName)) {
    const c = cloudRightSizing(recData, savings || 0);
    if (c) {
      return {
        verdict: c.verdict,
        serviceName: serviceName || undefined,
        whyNow,
        impact: c.impact,
        impactIsCost: c.impactIsCost,
        risk: c.risk,
        evidence: c.evidence,
      };
    }
  }

  // Spot / preemptible eligibility — lead with the workload and the interruption caveat.
  if (category === 'K8sSpotRecommendation') {
    const controller = recData?.controller_name || '';
    const verdict = controller ? `Move ${controller} to spot / preemptible capacity` : 'Run this workload on spot / preemptible capacity';
    const spotSavings = savings || num(recData?.estimated_saving) || 0;
    const spotImpact = deriveImpact('K8sSpotRecommendation', spotSavings);
    return {
      verdict,
      serviceName: serviceName || undefined,
      whyNow,
      impact: spotImpact.impact || undefined,
      impactIsCost: spotImpact.impactIsCost,
      risk: SPOT_RISK,
    };
  }

  const { impact, impactIsCost } = deriveImpact(category, savings || 0);
  return {
    verdict: deriveVerdict(category, ruleName, title),
    serviceName: serviceName || undefined,
    whyNow,
    impact: impact || undefined,
    impactIsCost,
    risk: sharedRisk,
  };
}
