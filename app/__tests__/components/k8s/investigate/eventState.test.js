import { describeEventState, describeOccurrence, eventWhen, formatDuration } from '@components/k8s/investigate/eventState';

const MIN = 60 * 1000;
const HOUR = 60 * MIN;
const NOW = new Date('2026-09-14T09:23:00Z').getTime();

describe('formatDuration', () => {
  it.each([
    [30 * 1000, '30s'],
    [12 * MIN, '12m'],
    [2 * HOUR + 46 * MIN, '2h 46m'],
    [3 * HOUR, '3h'],
    [(24 * 3 + 4) * HOUR, '3d 4h'],
  ])('%d ms → %s', (ms, text) => {
    expect(formatDuration(ms)).toBe(text);
  });
});

describe('eventWhen', () => {
  it('is the start time when the source gave one', () => {
    expect(eventWhen({ starts_at: '2026-09-14T06:37:13Z', created_at: '2026-09-14T06:39:00Z' })).toBe('2026-09-14T06:37:13Z');
  });

  // An automation's Store Event step with Starts At left empty stores starts_at as null.
  it('falls back to when the event was recorded', () => {
    expect(eventWhen({ starts_at: null, created_at: '2026-10-01T10:15:00Z' })).toBe('2026-10-01T10:15:00Z');
  });

  it('is null when the event has neither, or has not loaded', () => {
    expect(eventWhen({})).toBeNull();
    expect(eventWhen(undefined)).toBeNull();
  });
});

describe('describeEventState', () => {
  // The event that motivated this: an OOM kill still FIRING with no ends_at. It must not claim the
  // problem is still happening, only that no end was reported.
  it('describes FIRING as open with no end reported, not as still happening', () => {
    const s = describeEventState({ status: 'FIRING', startsAt: '2026-09-14 06:37:13', endsAt: null, now: NOW });
    expect(s.state).toBe('open');
    expect(s.label).toBe('Open');
    expect(s.detail).toBe('no end reported');
    expect(s.hint).toContain('OOM kill');
  });

  // DB timestamps carry no timezone and are UTC; a local-time parse would shift the duration.
  it('treats timezone-less timestamps as UTC', () => {
    const s = describeEventState({ status: 'CLOSED', startsAt: '2026-09-14 09:00:00', endsAt: '2026-09-14 09:13:00', now: NOW });
    expect(s.endedAgo).toBe('10m ago');
  });

  it('describes CLOSED with when it ended and how long it lasted', () => {
    const s = describeEventState({ status: 'CLOSED', startsAt: '2026-09-13T08:32:22Z', endsAt: '2026-09-13T08:33:23Z', now: NOW });
    expect(s.state).toBe('ended');
    expect(s.detail).toBe('lasted 1m');
    expect(s.hint).toMatch(/^Started .+, ended .+$/);
    expect(s.endedAgo).toBe('1d ago');
  });

  it('treats RESOLVED as ended', () => {
    expect(describeEventState({ status: 'RESOLVED', startsAt: '2026-09-14T09:00:00Z', endsAt: '2026-09-14T09:20:00Z', now: NOW }).state).toBe(
      'ended'
    );
  });

  // One-time signals are stored with ends_at == starts_at.
  it('omits the duration for a zero-length event', () => {
    const s = describeEventState({ status: 'CLOSED', startsAt: '2026-09-14T09:00:00Z', endsAt: '2026-09-14T09:00:00Z', now: NOW });
    expect(s.detail).toBe('');
    expect(s.endedAgo).toBe('23m ago');
  });

  // 9.7k PagerDuty events on dev are CLOSED with no ends_at.
  it('says the end time is not reported when an ended event has no ends_at', () => {
    const s = describeEventState({ status: 'CLOSED', startsAt: '2026-09-14T09:00:00Z', endsAt: null, now: NOW });
    expect(s.state).toBe('ended');
    expect(s.detail).toBe('end time unknown');
    expect(s.endedAgo).toBeUndefined();
  });

  it('says nothing for a missing or unknown status', () => {
    expect(describeEventState({ status: undefined, now: NOW })).toBeNull();
    expect(describeEventState({ status: 'WHATEVER', now: NOW })).toBeNull();
  });
});

describe('describeOccurrence', () => {
  const first = { id: 'e0', starts_at: '2026-09-13 02:03:00' };
  const latest = { id: 'e2', status: 'FIRING', starts_at: '2026-09-14 09:03:00' };

  it('says nothing when this is the only firing', () => {
    expect(describeOccurrence({ before: 1, after: 0, first, now: NOW })).toBeNull();
  });

  // The count before and after come from one population — same fingerprint — so
  // position and total are comparable. The old row mixed two definitions.
  it('places the event in the run of firings', () => {
    const r = describeOccurrence({ before: 3, after: 3, first, latest, now: NOW });
    expect(r.text).toBe('3rd of 6 since 13 Sep');
    expect(r.lastSeen).toBe('20m ago');
    expect(r.firstId).toBe('e0');
    expect(r.latestId).toBe('e2');
    expect(r.total).toBe(6);
  });

  it('uses the right ordinal', () => {
    expect(describeOccurrence({ before: 1, after: 1, first, now: NOW }).text).toContain('1st of 2');
    expect(describeOccurrence({ before: 2, after: 1, first, now: NOW }).text).toContain('2nd of 3');
    expect(describeOccurrence({ before: 11, after: 1, first, now: NOW }).text).toContain('11th of 12');
    expect(describeOccurrence({ before: 21, after: 1, first, now: NOW }).text).toContain('21st of 22');
  });

  // A failed lookup leaves the count at 0; saying "0th of 3" would be worse than
  // saying nothing.
  it('says nothing when the backward lookup produced no count', () => {
    expect(describeOccurrence({ before: 0, after: 5, first, latest, now: NOW })).toBeNull();
  });

  it('omits the last-seen part when the newest occurrence is the first one', () => {
    const r = describeOccurrence({ before: 1, after: 1, first, latest: first, now: NOW });
    expect(r.lastSeen).toBe('');
  });
});
