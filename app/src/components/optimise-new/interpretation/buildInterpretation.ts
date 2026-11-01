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
import {
  summarizeRightSizing,
  summarizeCloudRightSizing,
  formatCloudTargetSpec,
  splitSavings,
  type RightSizingSummary,
  type SavingsSplit,
} from '../rightSizingData';
import { summarizeConfigIssues } from '../evidence/configIssues';
import { isUnestimatedNativeCostFinding } from '../utils';

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
  /** The live workload facts behind a k8s recommendation, for restart risk. */
  workload?: WorkloadFacts;
}

export interface WorkloadFacts {
  /** Controller kind from discovery meta (Deployment, StatefulSet, ...). */
  kind?: string;
  /** Live pod count from discovery meta — not spec.replicas: 0 mid-rollout. */
  pods?: number | null;
}

// Rule-family caveats. Kept deliberately small for v1 — only rules where the
// operational consequence is well-defined and worth surfacing up-front.
const RISK_BY_RULE: Record<string, string> = {
  pod_right_sizing:
    "Applying updates the workload's resource requests; unless the no-restart (in-place) apply is used, that triggers a rolling restart. Reversible by reverting the change.",
  replica_right_sizing: 'Reducing replicas lowers spare capacity for traffic spikes and failures — confirm there is enough headroom before applying.',
  pv_rightsize: "Persistent volumes can't be shrunk in place — this requires creating a new PVC and migrating data, with downtime.",
  unused_pvc: "Deleting a PVC can be irreversible if the volume isn't retained. Confirm nothing depends on it first.",
  abandoned_resource: "Scaling the workload to zero is reversible, but verify it's genuinely unused before applying.",
};

// singleReplicaOutage: a rolling restart is an outage only when the workload
// cannot keep a pod serving while its one pod is replaced — a StatefulSet
// replaces pods in place. A single-pod Deployment surges a new pod before
// terminating the old one under the default strategy, so it earns no outage
// claim. A live pod count of 0 (mid-rollout, scaled to zero) or an unknown
// count is "unknown", never "single".
export const singleReplicaOutage = (workload?: WorkloadFacts): boolean => workload?.kind === 'StatefulSet' && workload?.pods === 1;

// The single-pod case is a variant of the same caveat, not an addition to it:
// concatenating the two made one paragraph name the in-place apply twice.
const SINGLE_REPLICA_RISK =
  "Applying updates the workload's resource requests. This StatefulSet runs a single pod, so the restart is a brief outage for its callers — prefer the no-restart (in-place) apply where the cluster supports it, or a maintenance window. Reversible by reverting the change.";

// podRightSizingRisk picks the restart caveat that fits the workload.
export const podRightSizingRisk = (workload?: WorkloadFacts): string =>
  singleReplicaOutage(workload) ? SINGLE_REPLICA_RISK : RISK_BY_RULE.pod_right_sizing;

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
  // A negative saving is a real cost impact — applying the recommendation raises
  // the bill. impactIsCost stays false: it renders green, which would read as
  // money saved.
  if (Math.round(-savings) >= 1) {
    return { impact: `Costs ~$${Math.round(-savings)}/mo more to apply.`, impactIsCost: false };
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

// " at an average of 3.2 replicas over 14 days (2–10)"; the range is dropped
// when the count never moved.
export function formatReplicaWindow(r: RightSizingSummary['replicas']): string {
  if (!r) return '';
  const avg = Number.isInteger(r.avg) ? String(r.avg) : r.avg.toFixed(1);
  const noun = r.avg === 1 ? 'replica' : 'replicas';
  const days = r.windowHours != null ? r.windowHours / 24 : null;
  const over = days != null ? ` over ${Number.isInteger(days) ? days : days.toFixed(1)} days` : '';
  const range = r.min !== r.max ? ` (${r.min}–${r.max})` : '';
  return ` at an average of ${avg} ${noun}${over}${range}`;
}

// resourceNames says which requests move, in the order the table lists them.
const resourceNames = (changed: RightSizingSummary['changedResources']): string => {
  if (changed.length === 2) return 'CPU and memory requests';
  if (changed[0] === 'cpu') return 'CPU requests';
  if (changed[0] === 'memory') return 'memory requests';
  return 'resource requests';
};

function podRightSizing(
  summary: RightSizingSummary,
  savings: number,
  split: SavingsSplit | null = null
): Pick<InterpretationData, 'verdict' | 'impact' | 'impactIsCost' | 'evidence' | 'confidence' | 'observedWindow'> {
  // The verdict names the action; the current → recommended figures live in the
  // "Recommended Resource Changes" table right below it, which also carries the
  // percentages, so spelling them here made the reader parse the same four
  // numbers twice. The container is named only when there is more than one —
  // with a single container the table's one row already names it.
  const containerLabel = summary.containerCount > 1 ? ` on ${summary.primary} (+${summary.containerCount - 1} more)` : '';
  const verdict = summary.changeText
    ? `${DIRECTION_VERB[summary.direction]} ${resourceNames(summary.changedResources)}${containerLabel}`
    : 'Right-size resource requests to match observed usage';

  let impact: string;
  let impactIsCost = false;
  const monthlyIncrease = Math.round(savings < 0 ? -savings : 0);
  // The dollar figure is per pod times replicas; say which replica count it
  // assumed so a swing on an autoscaled workload reads as a range, not a bug.
  const atReplicas = formatReplicaWindow(summary.replicas);
  // The replica window qualifies the dollar figure, which now lives only in the
  // panel header, so it is stated as the assumption behind it rather than
  // trailing a number this line no longer prints. formatReplicaWindow leads
  // with " at" for the cost line below; here the verb already carries it.
  const assumption = atReplicas ? `; the figure assumes${atReplicas.replace(/^ at /, ' ')}` : '';
  if (split && Math.round(split.gross) >= 1 && Math.round(split.cost) >= 1) {
    // Name containers by the side they contributed to, not by their own net: a
    // container whose cpu saving and memory cost cancel still belongs on both.
    const saver = split.containers.filter((c) => (c.cpu ?? 0) > 0 || (c.memory ?? 0) > 0).map((c) => c.name);
    const payer = split.containers.filter((c) => (c.cpu ?? 0) < 0 || (c.memory ?? 0) < 0).map((c) => c.name);
    const sameContainers = saver.join(', ') === payer.join(', ');
    // The panel header already carries the gross, the cost and the net. What it
    // cannot show is which container is on which side of that split, so that is
    // all this line adds — and with one container there is no side to name, so
    // the slot is dropped rather than restating the header. It is no longer a
    // money statement either way, so it never renders in the savings tone.
    if (summary.containerCount === 1) impact = '';
    else if (sameContainers) impact = `Saves and costs more on the same container, ${saver.join(', ')}${assumption}.`;
    else impact = `Saves on ${saver.join(', ')} · costs more on ${payer.join(', ')}${assumption}.`;
  } else if (Math.round(savings) >= 1) {
    // The header states the saving. Reclaimed capacity is the part it cannot.
    impact = summary.reclaimText ? `Reclaims ${summary.reclaimText}${assumption}.` : '';
  } else if (monthlyIncrease >= 1) {
    // Raising requests buys reliability at a monthly price, and that price is
    // the whole basis for the trade-off, so name it rather than reporting
    // "no direct cost change" for a workload whose bill goes up.
    const reason = summary.direction === 'increase' ? 'prevents CPU throttling and OOM kills' : 'matches requests to observed usage';
    impact = `Costs ~$${monthlyIncrease}/mo more${atReplicas} · ${reason}.`;
  } else if (summary.reclaimText) {
    impact = `Reclaims ~${summary.reclaimText} of guaranteed allocation. No direct cost change.`;
  } else if (summary.direction === 'increase') {
    impact = 'Prevents CPU throttling and OOM kills. No direct cost change.';
  } else {
    impact = 'Reliability & efficiency. No direct cost change.';
  }

  return {
    verdict,
    impact,
    impactIsCost,
    evidence: summary.evidence.length ? summary.evidence : undefined,
    confidence: summary.confidence,
    observedWindow: summary.observedWindow,
  };
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
  workload,
}: BuildArgs): InterpretationData {
  // "Why" source priority: catalog recommendation step (richest) → catalog/JSONB
  // description → computed insight (best for K8s-native rules with no catalog).
  const whyNow = recommendations?.[0]?.trim() || (description?.trim() ? description : insight) || undefined;
  const sharedRisk = ruleName === 'pod_right_sizing' ? podRightSizingRisk(workload) : RISK_BY_RULE[ruleName] || undefined;

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
      const r = podRightSizing(summary, savings || 0, splitSavings(recData));
      return {
        verdict: r.verdict,
        serviceName: serviceName || undefined,
        confidence: r.confidence,
        observedWindow: r.observedWindow,
        whyNow,
        impact: r.impact || undefined,
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

  // The provider called it a cost finding but attached no figure; the generic
  // $0 RightSizing line would claim "no direct cost change", which is false.
  const { impact, impactIsCost } = isUnestimatedNativeCostFinding(category, ruleName, savings || 0)
    ? { impact: 'Cost — the cloud provider flags a saving here but gives no figure.', impactIsCost: false }
    : deriveImpact(category, savings || 0);
  return {
    verdict: deriveVerdict(category, ruleName, title),
    serviceName: serviceName || undefined,
    whyNow,
    impact: impact || undefined,
    impactIsCost,
    risk: sharedRisk,
  };
}
