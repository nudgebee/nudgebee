import {
  addPendingAgentInstall,
  clearPendingAgentInstalls,
  isPendingAgentInstallSnoozed,
  readPendingAgentInstalls,
  SNOOZE_MS,
  snoozePendingAgentInstalls,
} from '@components/integrations/pendingAgentInstall';

jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 'tenant-1' }) }));

describe('pendingAgentInstall snooze', () => {
  beforeEach(() => {
    localStorage.clear();
    jest.useFakeTimers().setSystemTime(new Date('2026-09-28T10:00:00Z'));
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('keeps a snoozed entry in the store but marks it snoozed for SNOOZE_MS', () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    snoozePendingAgentInstalls(['acc-1']);

    const [entry] = readPendingAgentInstalls();
    expect(entry.id).toBe('acc-1');
    expect(entry.snoozedUntil).toBe(Date.now() + SNOOZE_MS);
    expect(isPendingAgentInstallSnoozed(entry)).toBe(true);

    jest.setSystemTime(Date.now() + SNOOZE_MS);
    expect(isPendingAgentInstallSnoozed(readPendingAgentInstalls()[0])).toBe(false);
  });

  it('only snoozes the given ids, so a later account is shown immediately', () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    snoozePendingAgentInstalls(['acc-1']);
    addPendingAgentInstall('acc-2', 'Cluster B');

    const entries = readPendingAgentInstalls();
    expect(entries.map((e) => [e.id, isPendingAgentInstallSnoozed(e)])).toEqual([
      ['acc-1', true],
      ['acc-2', false],
    ]);
  });

  it('clears a snoozed entry for good once its agent connects', () => {
    addPendingAgentInstall('acc-1', 'Cluster A');
    snoozePendingAgentInstalls(['acc-1']);
    clearPendingAgentInstalls(['acc-1']);

    expect(readPendingAgentInstalls()).toEqual([]);
  });
});
