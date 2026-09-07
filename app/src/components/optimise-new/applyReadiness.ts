import { getImpactSummary, getChangeClass, deriveVerdict, businessCriticalCount } from './safetyBand';

export interface ApplyReadiness {
  tone: 'success' | 'info' | 'warning' | 'critical';
  title: string;
  message: string;
  /** Deploy pauses for an acknowledging "Deploy Fix" before it runs. */
  needsAck: boolean;
}

const sentence = (s?: string): string => (s ? s.charAt(0).toUpperCase() + s.slice(1).replace(/\.?$/, '.') : '');

// restartSafeguard names the one thing that makes a restart fine for callers.
// The in-place path is a Kubernetes 1.35+ feature; older clusters fall back to
// a rolling restart, so the copy never promises "no restart" outright.
export const restartSafeguard = (inPlace: boolean): string =>
  inPlace
    ? 'No-restart is on — on Kubernetes 1.35+ pods resize in place; older clusters roll pods one at a time.'
    : 'Turn on No-restart (in-place), or apply in a maintenance window.';

// A removal has no restart to soften; the safeguard is a last look at what
// still points at the resource.
const REMOVAL_SAFEGUARD = "Removal can't be undone — confirm nothing still depends on it, and snapshot first where the resource supports it.";

// applyReadiness is the safety verdict re-presented at the moment of applying:
// the same band, reason and tone the details panel shows, plus the safeguard
// that makes applying fine. Only an irreversible change is red; it also asks
// for the acknowledgement on its own, whatever the band — the one human glance
// the backend grades it Review for. Null when the recommendation row simply
// wasn't loaded with its safety columns (the summary projection omits them);
// that is "not fetched", not "unknown", so nothing is claimed.
export const applyReadiness = (rec: any, inPlace: boolean): ApplyReadiness | null => {
  if (!rec || (rec.safety_band === undefined && rec.finops_score_breakdown === undefined)) return null;
  const band: string | undefined = rec.safety_band || undefined;
  const impact = getImpactSummary(rec);
  if (!band || band === 'unknown') {
    return {
      tone: 'info',
      title: 'Not in the dependency graph yet',
      message: "Impact isn't assessed for this workload — apply as you normally would.",
      needsAck: false,
    };
  }
  const changeClass = getChangeClass(rec);
  const verdict = deriveVerdict(band, impact?.production_dependents, impact?.dependent_count, impact?.truncated, changeClass);
  const reason = sentence(impact?.safety_reason);
  if (verdict.tone === 'success') {
    return {
      tone: 'success',
      title: band === 'safe' ? 'No known dependents — safe to apply' : verdict.title,
      message: reason || 'Blast radius assessed from the dependency graph.',
      needsAck: false,
    };
  }
  const prod = impact?.production_dependents ?? 0;
  const critical = businessCriticalCount(impact?.dependents);
  const facts = [reason, critical > 0 ? `${critical} business-critical.` : ''].filter(Boolean).join(' ');
  const destructive = changeClass === 'destructive';
  return {
    tone: verdict.tone === 'critical' ? 'critical' : 'warning',
    title: verdict.title,
    message: `${facts} ${destructive ? REMOVAL_SAFEGUARD : restartSafeguard(inPlace)}`.trim(),
    needsAck: band === 'risky' || prod > 0 || destructive,
  };
};

// autoOptimizeNotice is the one-line heads-up shown when an Auto Optimize rule
// is created from a recommendation whose callers are production. Informational
// only: the rule's own approval and notification settings are the safeguard.
export const autoOptimizeNotice = (rec: any): string | null => {
  const prod = getImpactSummary(rec)?.production_dependents ?? 0;
  if (prod <= 0) return null;
  return `${prod} production service${prod === 1 ? '' : 's'} depend${
    prod === 1 ? 's' : ''
  } on this workload — Auto Optimize will apply future right-sizes here automatically. Approval and notification settings below keep you in the loop.`;
};
