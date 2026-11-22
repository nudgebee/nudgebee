// What Alert Tuning shows for a suggestion. The server words each recommendation
// (recommendation_label) and decides whether a row carries a new value; this module is the one
// place the app turns that into chips, so the list, the expanded row, the investigation card and
// the briefing cannot drift apart. A recommendation type this module does not know is shown as a
// neutral "Review" with no value — never as a tuning.

export type LabelVariant = 'blue' | 'orange' | 'red' | 'green' | 'grey';
export type VerdictTone = 'neutral' | 'info' | 'success' | 'warning' | 'critical';
export type VerdictHue = 'blue' | 'amber' | 'violet' | 'red' | 'green' | 'slate';

export interface RecommendationDisplay {
  label: string;
  variant: LabelVariant; // <Label variant>
  tone: VerdictTone; // <Chip tone>
  hue: VerdictHue; // <Chip hue>
}

// Wording here is the fallback for a response that carries no recommendation_label; it matches
// the server's (api-server/services/triage/threshold_suggestion_verdict.go).
const RECOMMENDATIONS: Record<string, RecommendationDisplay> = {
  tune_threshold: { label: 'Tune Threshold', variant: 'blue', tone: 'info', hue: 'blue' },
  tune_both: { label: 'Tune Both', variant: 'blue', tone: 'info', hue: 'violet' },
  increase_duration: { label: 'Increase Duration', variant: 'orange', tone: 'warning', hue: 'amber' },
  disable: { label: 'Disable Alert', variant: 'red', tone: 'critical', hue: 'red' },
  none: { label: 'No Change', variant: 'green', tone: 'success', hue: 'green' },
  review_alert: { label: 'Review Alert', variant: 'orange', tone: 'warning', hue: 'amber' },
  investigate_signal: { label: 'Investigate', variant: 'orange', tone: 'warning', hue: 'amber' },
  insufficient_data: { label: 'Not Enough Data', variant: 'grey', tone: 'neutral', hue: 'slate' },
  not_eligible: { label: 'Nothing to Tune', variant: 'grey', tone: 'neutral', hue: 'slate' },
};

const UNKNOWN_RECOMMENDATION: RecommendationDisplay = { label: 'Review', variant: 'grey', tone: 'neutral', hue: 'slate' };

interface RecommendationCarrier {
  recommendation_type?: string | null;
  metric_stats?: { recommendation_type?: string | null } | null;
}

/** The recommendation type of a list row or a card suggestion; '' when the row carries none. */
export const recommendationTypeOf = (item?: RecommendationCarrier | null): string =>
  item?.metric_stats?.recommendation_type || item?.recommendation_type || '';

/** How a recommendation is shown. The server's label wins; an unknown type reads "Review". */
export const recommendationDisplay = (recType?: string | null, serverLabel?: string | null): RecommendationDisplay => {
  const known = (recType && RECOMMENDATIONS[recType]) || UNKNOWN_RECOMMENDATION;
  return serverLabel ? { ...known, label: serverLabel } : known;
};

/**
 * Whether a recommendation carries a new threshold value. Only a tuning does: "investigate",
 * "review" and "not enough data" are verdicts about the alert, not values to apply.
 */
export const offersNumber = (recType?: string | null): boolean => recType === 'tune_threshold' || recType === 'tune_both';

export interface FiringPattern {
  label: string;
  description: string;
  tone: VerdictTone;
  hue: VerdictHue;
}

interface AlertQualityLike {
  classification?: string | null;
  instant_rate?: number | null;
}

/**
 * The firing pattern behind a rule, worded as what was observed. It describes how the rule fires;
 * the recommendation is the verdict, so this must never read as a second one. A rule whose history
 * shows no pattern gets no chip.
 */
export const firingPattern = (quality?: AlertQualityLike | null): FiringPattern | null => {
  switch (quality?.classification) {
    case 'false_positive':
      if ((quality.instant_rate ?? 0) >= 0.5) {
        return {
          label: 'Fires with no end time',
          description: 'Fires several times a day as events that carry no end time, and nobody changes their status here.',
          tone: 'warning',
          hue: 'amber',
        };
      }
      return {
        label: 'Short firings',
        description: 'Most firings clear on their own within about 10 minutes, and nobody changes their status here.',
        tone: 'warning',
        hue: 'amber',
      };
    case 'noisy_ignored':
      return {
        label: 'Long firings, not acted on',
        description: 'Fires regularly and stays open, and nobody changes its status here.',
        tone: 'warning',
        hue: 'amber',
      };
    case 'noisy_but_real':
      return { label: 'Fires often, acted on', description: 'Fires often, and people act on it here.', tone: 'info', hue: 'blue' };
    case 'broken':
      return { label: 'Never clears', description: 'Almost none of its firings have cleared.', tone: 'critical', hue: 'red' };
    default:
      return null;
  }
};

interface ApplyStateCarrier {
  apply_status?: string | null;
  applied_at?: string | null;
  computed_at?: string | null;
}

/**
 * Whether an "applied" state describes the advice on the row. Advice is recomputed against the
 * rule as it stands; once that has happened after an apply, the apply is history — the row may
 * carry new advice, and it must not be blocked by a change made to an earlier one. Revert stays
 * available either way.
 */
export const appliedIsCurrent = (item?: ApplyStateCarrier | null): boolean => {
  if (item?.apply_status !== 'applied') {
    return false;
  }
  const appliedAt = Date.parse(item.applied_at || '');
  const computedAt = Date.parse(item.computed_at || '');
  if (Number.isNaN(appliedAt) || Number.isNaN(computedAt)) {
    return true;
  }
  return computedAt <= appliedAt;
};

/** Whether a recommendation changes how long the rule waits before it fires. */
export const changesWait = (recType?: string | null): boolean => recType === 'increase_duration' || recType === 'tune_both';

/** A wait in the units a rule is written in: "30s", "2m", "1m30s", "1h". */
export const formatWait = (seconds?: number | null): string => {
  if (seconds == null || Number.isNaN(seconds) || seconds <= 0) {
    return '0s';
  }
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  return `${h ? `${h}h` : ''}${m ? `${m}m` : ''}${s ? `${s}s` : ''}`;
};

interface WaitCarrier {
  suggested_duration?: number | null; // minutes
  current_duration_seconds?: number | null;
}

/**
 * The wait change a row advises, as "30s → 2m", or '' when it advises none. A row stored before
 * the current wait was recorded shows only the wait to set.
 */
export const waitChange = (recType?: string | null, wait?: WaitCarrier | null): string => {
  const suggestedMinutes = wait?.suggested_duration;
  if (!changesWait(recType) || !suggestedMinutes || suggestedMinutes <= 0) {
    return '';
  }
  const suggested = formatWait(suggestedMinutes * 60);
  if (wait?.current_duration_seconds == null) {
    return suggested;
  }
  return `${formatWait(wait.current_duration_seconds)} → ${suggested}`;
};
