import {
  appliedIsCurrent,
  changesWait,
  firingPattern,
  formatWait,
  offersNumber,
  recommendationDisplay,
  recommendationTypeOf,
  waitChange,
} from '../thresholdVerdict';

// Every recommendation type the server emits (api-server/services/triage/threshold_suggestion_verdict.go).
const SERVER_TYPES = [
  'tune_threshold',
  'tune_both',
  'increase_duration',
  'disable',
  'none',
  'review_alert',
  'investigate_signal',
  'insufficient_data',
  'not_eligible',
];

describe('thresholdVerdict', () => {
  it('has its own wording for every type the server emits', () => {
    SERVER_TYPES.forEach((type) => {
      expect(recommendationDisplay(type).label).not.toBe('Review');
    });
  });

  it('never shows an unknown or missing type as a tuning', () => {
    ['something_new', '', undefined, null].forEach((type) => {
      const display = recommendationDisplay(type);
      expect(display.label).toBe('Review');
      expect(display.variant).toBe('grey');
      expect(offersNumber(type)).toBe(false);
    });
  });

  it('labels an "investigate, do not retune" row as Investigate, not Tune Threshold', () => {
    expect(recommendationDisplay('investigate_signal').label).toBe('Investigate');
    expect(recommendationDisplay('insufficient_data').label).toBe('Not Enough Data');
  });

  it('shows the server wording when the response carries one', () => {
    expect(recommendationDisplay('tune_threshold', 'Tune Threshold').label).toBe('Tune Threshold');
    expect(recommendationDisplay('not_eligible', 'Nothing to Tune').label).toBe('Nothing to Tune');
    // The tone still comes from the type, so a server label cannot turn a review into a tuning chip.
    expect(recommendationDisplay('something_new', 'Brand New').variant).toBe('grey');
  });

  it('offers a new value only for a tuning', () => {
    expect(SERVER_TYPES.filter((type) => offersNumber(type))).toEqual(['tune_threshold', 'tune_both']);
  });

  it('reads the type from a list row or a card suggestion', () => {
    expect(recommendationTypeOf({ metric_stats: { recommendation_type: 'investigate_signal' }, recommendation_type: 'tune_threshold' })).toBe(
      'investigate_signal'
    );
    expect(recommendationTypeOf({ recommendation_type: 'review_alert' })).toBe('review_alert');
    expect(recommendationTypeOf({})).toBe('');
    expect(recommendationTypeOf(null)).toBe('');
  });

  it('words the firing pattern as an observation, with no chip when there is no pattern', () => {
    expect(firingPattern({ classification: 'false_positive', instant_rate: 0 })?.label).toBe('Short firings');
    expect(firingPattern({ classification: 'false_positive', instant_rate: 0.96 })?.label).toBe('Fires with no end time');
    expect(firingPattern({ classification: 'noisy_ignored' })?.label).toBe('Long firings, not acted on');
    expect(firingPattern({ classification: 'broken' })?.label).toBe('Never clears');
    expect(firingPattern({ classification: 'healthy' })).toBeNull();
    expect(firingPattern(null)).toBeNull();
  });

  describe('wait time', () => {
    it('formats a wait the way a rule writes it', () => {
      expect(formatWait(30)).toBe('30s');
      expect(formatWait(120)).toBe('2m');
      expect(formatWait(90)).toBe('1m30s');
      expect(formatWait(3600)).toBe('1h');
      expect(formatWait(0)).toBe('0s');
      expect(formatWait(undefined)).toBe('0s');
    });

    it('only a longer-wait or tune-both verdict changes the wait', () => {
      expect(SERVER_TYPES.filter((type) => changesWait(type))).toEqual(['tune_both', 'increase_duration']);
    });

    it('shows the change from the current wait to the suggested one', () => {
      expect(waitChange('increase_duration', { suggested_duration: 2, current_duration_seconds: 30 })).toBe('30s → 2m');
      // A rule that fires at once today.
      expect(waitChange('increase_duration', { suggested_duration: 1, current_duration_seconds: 0 })).toBe('0s → 1m');
      // A row stored before the current wait was recorded shows only the wait to set.
      expect(waitChange('increase_duration', { suggested_duration: 15 })).toBe('15m');
    });

    it('shows nothing when the verdict does not change the wait or carries none', () => {
      expect(waitChange('tune_threshold', { suggested_duration: 2, current_duration_seconds: 30 })).toBe('');
      expect(waitChange('increase_duration', {})).toBe('');
      expect(waitChange('increase_duration', null)).toBe('');
    });
  });

  describe('appliedIsCurrent', () => {
    it('is false for a row that was never applied, is pending, or was reverted', () => {
      expect(appliedIsCurrent({})).toBe(false);
      expect(appliedIsCurrent({ apply_status: 'pending', applied_at: '2026-07-10T16:14:05Z', computed_at: '2026-10-08T06:00:21Z' })).toBe(false);
      expect(appliedIsCurrent({ apply_status: 'reverted', applied_at: '2026-06-25T11:41:04Z', computed_at: '2026-10-08T06:00:21Z' })).toBe(false);
    });

    it('is true while the advice has not been recomputed since the apply', () => {
      expect(appliedIsCurrent({ apply_status: 'applied', applied_at: '2026-10-08T09:00:00Z', computed_at: '2026-10-08T06:00:22Z' })).toBe(true);
    });

    it('is false once the advice was recomputed after the apply', () => {
      // A threshold change applied in July; the row now carries different advice computed in October.
      expect(appliedIsCurrent({ apply_status: 'applied', applied_at: '2026-07-10T16:12:43Z', computed_at: '2026-10-08T06:00:22Z' })).toBe(false);
    });

    it('keeps blocking when a timestamp is missing', () => {
      expect(appliedIsCurrent({ apply_status: 'applied', applied_at: '2026-07-10T16:12:43Z' })).toBe(true);
    });
  });
});
