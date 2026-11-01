import { ACTIVE_WINDOW_MS, FIRST_EVENT_GRACE_MS, eventBridgeStatus, newestTimestamp } from '@lib/eventBridgeStatus';

// The Real-Time Events column used to say "active" as soon as Connect EventBridge
// was clicked, so an account whose stack was never created read as connected for
// days (#41155). Only a received event may read as active.
describe('eventBridgeStatus', () => {
  const now = Date.parse('2026-10-13T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  it('says nothing for an account that never connected and never sent an event', () => {
    expect(eventBridgeStatus(undefined, now)).toBeNull();
    expect(eventBridgeStatus({}, now)).toBeNull();
  });

  it('is active when an event arrived recently, whether or not Connect was clicked', () => {
    expect(eventBridgeStatus({ last_event_at: ago(60_000) }, now)?.text).toBe('active');
    expect(eventBridgeStatus({ connected_at: ago(30 * 86_400_000), last_event_at: ago(ACTIVE_WINDOW_MS) }, now)?.tone).toBe('success');
  });

  it('warns when the last event is older than the active window', () => {
    const s = eventBridgeStatus({ last_event_at: ago(ACTIVE_WINDOW_MS + 1) }, now);
    expect(s?.text).toBe('no recent events');
    expect(s?.tone).toBe('warning');
  });

  it('waits after Connect is clicked, without claiming the feed works', () => {
    const s = eventBridgeStatus({ connected_at: ago(FIRST_EVENT_GRACE_MS - 1) }, now);
    expect(s?.text).toBe('waiting for events');
    expect(s?.tone).toBe('info');
  });

  it('warns when Connect was clicked a day ago and no event ever came', () => {
    const s = eventBridgeStatus({ connected_at: ago(5 * 86_400_000) }, now);
    expect(s?.text).toBe('no events received');
    expect(s?.tone).toBe('warning');
    expect(s?.tooltip).toMatch(/stack may not have been created/);
  });

  it('ignores unreadable timestamps', () => {
    expect(eventBridgeStatus({ connected_at: 'not a date' }, now)).toBeNull();
  });
});

// An account can have several agent rows; the column uses the newest timestamps
// across them, and an unreadable value on one row must not hide a good one on another.
describe('newestTimestamp', () => {
  const older = '2026-10-12T10:00:00Z';
  const newer = '2026-10-13T10:00:00Z';

  it('picks the later of two readable timestamps, in either order', () => {
    expect(newestTimestamp(older, newer)).toBe(newer);
    expect(newestTimestamp(newer, older)).toBe(newer);
  });

  it('prefers a readable timestamp over a missing or unreadable one', () => {
    expect(newestTimestamp(undefined, older)).toBe(older);
    expect(newestTimestamp(older, undefined)).toBe(older);
    expect(newestTimestamp('not a date', older)).toBe(older);
    expect(newestTimestamp(older, 'not a date')).toBe(older);
  });

  it('is undefined when neither can be read', () => {
    expect(newestTimestamp(undefined, undefined)).toBeUndefined();
    expect(newestTimestamp('not a date', '')).toBeUndefined();
  });
});
