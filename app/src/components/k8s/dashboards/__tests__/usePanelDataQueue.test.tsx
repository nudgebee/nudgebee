import { act, cleanup, renderHook } from '@testing-library/react';
import { usePanelData } from '../usePanelData';
import { acquirePanelSlot, panelQueueState } from '../panelQueue';
import type { AccountOption, Panel } from '@api1/dashboards';

// Never resolves: the point of these tests is a panel that is still loading.
jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: { executeEntityQuery: jest.fn(() => new Promise(() => {})) },
  isCommandDatasource: () => false,
}));

jest.mock('@api1/observability', () => ({ __esModule: true, default: {} }));

// The panel's scope resolves to one account, so the fetch effect gets past its
// "no account selected" guard and actually starts a request.
jest.mock('../panelAccounts', () => ({
  resolvePanelAccounts: () => [{ value: 'acc-1', label: 'Account 1' }],
  panelQueryAccounts: () => ({ accounts: [{ value: 'acc-1', label: 'Account 1' }] }),
}));

const accounts = [{ value: 'acc-1', label: 'Account 1' }] as AccountOption[];

const panel = {
  id: 1,
  title: 'Events',
  type: 'table',
  datasource: 'nudgebee',
  targets: [{ ref_id: 'A', query: '{"table":"events"}' }],
} as unknown as Panel;

/** Lets the promise callbacks queued by an acquire or a release actually run. */
const settle = () =>
  act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });

describe('usePanelData and the admission queue', () => {
  afterEach(async () => {
    // Unmount first — a hook still mounted holds its slot, and the drain below
    // could never take it. Bounded, so a regression that leaks a slot fails the
    // assertion rather than spinning this loop until the queue's own timeout.
    cleanup();
    await settle();
    for (let i = 0; i < 8 && (panelQueueState().active > 0 || panelQueueState().waiting > 0); i += 1) {
      const release = await acquirePanelSlot();
      release();
    }
  });

  it('gives the slot back when a panel scrolls out of view mid-load', async () => {
    const { rerender } = renderHook(({ enabled }) => usePanelData({ panel, accounts, variables: {}, enabled, startTime: 0, endTime: 1 }), {
      initialProps: { enabled: true },
    });
    await settle();
    // Admitted and fetching: the request never settles, so the slot is held.
    expect(panelQueueState().active).toBe(1);

    rerender({ enabled: false });
    await settle();
    // Before the fix this stayed at 1 until the queue's 15s timeout fired,
    // starving whatever was queued behind a panel nobody is looking at.
    expect(panelQueueState().active).toBe(0);
  });

  it('re-queues the abandoned panel when it comes back into view', async () => {
    const { rerender } = renderHook(({ enabled }) => usePanelData({ panel, accounts, variables: {}, enabled, startTime: 0, endTime: 1 }), {
      initialProps: { enabled: true },
    });
    await settle();
    rerender({ enabled: false });
    await settle();
    expect(panelQueueState().active).toBe(0);

    rerender({ enabled: true });
    await settle();
    // Admission was given back with the slot, so the panel takes its turn again
    // rather than fetching outside the cap.
    expect(panelQueueState().active).toBe(1);
  });
});
