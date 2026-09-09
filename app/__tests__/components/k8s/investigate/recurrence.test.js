import { describeRecurrence, RECURRENCE_BASELINE_WINDOW_MS } from '@components/k8s/investigate/recurrence';

const HOUR = 60 * 60 * 1000;
const NOW = new Date('2026-08-28T12:00:00Z').getTime();

// 7-day window with 24 occurrences ≈ one every 7 hours — the real cadence of the OOM event this
// was built against.
const baseline = { baselineCount: 24, baselineWindowMs: RECURRENCE_BASELINE_WINDOW_MS };

const at = (hoursAgo) => NOW - hoursAgo * HOUR;

describe('describeRecurrence', () => {
  // The one unambiguous signal: it ran, and the problem came back anyway.
  it('reports recurrence as the action not having stopped it', () => {
    const v = describeRecurrence({ ...baseline, recurrencesSince: 4, actedAtMs: at(10), now: NOW });
    expect(v.tone).toBe('warning');
    expect(v.text).toContain('Recurred 4 times since');
    expect(v.text).toContain('did not stop it');
  });

  it('uses the singular for one recurrence', () => {
    const v = describeRecurrence({ ...baseline, recurrencesSince: 1, actedAtMs: at(10), now: NOW });
    expect(v.text).toContain('Recurred 1 time since');
  });

  // Silence shorter than the problem's own quiet period proves nothing, and must not be dressed up
  // as success — this is the case that would otherwise put a green tick on an unfixed problem.
  it('refuses to call it fixed while silence is within the normal interval', () => {
    const v = describeRecurrence({ ...baseline, recurrencesSince: 0, actedAtMs: at(3), now: NOW });
    expect(v.tone).toBe('neutral');
    expect(v.text).toContain('too early to tell');
    expect(v.text).not.toContain('was firing');
  });

  it('calls it held once silence outlasts twice the interval', () => {
    const v = describeRecurrence({ ...baseline, recurrencesSince: 0, actedAtMs: at(72), now: NOW });
    expect(v.tone).toBe('success');
    expect(v.text).toContain('No recurrence in 3d');
    expect(v.text).toContain('was firing about every');
  });

  // Never claims the action caused the silence — something else may have fixed it.
  it('never asserts the action was the cause', () => {
    const v = describeRecurrence({ ...baseline, recurrencesSince: 0, actedAtMs: at(72), now: NOW });
    expect(v.text).not.toMatch(/fixed|resolved|worked/i);
  });

  describe('without a usable baseline', () => {
    it('says so rather than implying success', () => {
      const v = describeRecurrence({
        recurrencesSince: 0,
        baselineCount: 1,
        baselineWindowMs: RECURRENCE_BASELINE_WINDOW_MS,
        actedAtMs: at(50),
        now: NOW,
      });
      expect(v.tone).toBe('neutral');
      expect(v.text).toContain('too little history to compare');
    });

    it('treats a first occurrence the same way', () => {
      const v = describeRecurrence({
        recurrencesSince: 0,
        baselineCount: 0,
        baselineWindowMs: RECURRENCE_BASELINE_WINDOW_MS,
        actedAtMs: at(50),
        now: NOW,
      });
      expect(v.text).toContain('too little history to compare');
    });
  });

  it('renders nothing when the inputs cannot answer the question', () => {
    expect(describeRecurrence({ recurrencesSince: undefined, actedAtMs: at(1), now: NOW })).toBeNull();
    expect(describeRecurrence({ recurrencesSince: 0, actedAtMs: NaN, now: NOW })).toBeNull();
  });

  it('scales the elapsed time to a readable unit', () => {
    expect(describeRecurrence({ ...baseline, recurrencesSince: 0, actedAtMs: at(0.5), now: NOW }).text).toContain('30m');
    expect(describeRecurrence({ ...baseline, recurrencesSince: 0, actedAtMs: at(5), now: NOW }).text).toContain('5h');
    expect(describeRecurrence({ ...baseline, recurrencesSince: 0, actedAtMs: at(96), now: NOW }).text).toContain('4d');
  });
});
