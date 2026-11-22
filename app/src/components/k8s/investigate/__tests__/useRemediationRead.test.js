import { act, renderHook } from '@testing-library/react';
import useRemediationRead, { remediationPanelsFor, remediationReadKeyFor, useStickyLoop } from '../useRemediationRead';

jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { getRemediation: jest.fn() } }));

// A read the test settles by hand.
const deferred = () => {
  let resolve;
  const promise = new Promise((r) => {
    resolve = r;
  });
  return { promise, resolve };
};
const approval = { conversation_id: 'c1', message_id: 'm1', agent_id: 'a1', command: 'kubectl rollout undo deployment/web', origin: 'web' };
const withApproval = { plan_version: 'pv1', actions: [], loop: { plan_status: 'ready', pending_approvals: [approval] } };
const flush = () => act(async () => {});

beforeEach(() => jest.useFakeTimers());
afterEach(() => jest.useRealTimers());

describe('useRemediationRead', () => {
  it('a read slower than the poll gap still lands, and the next one starts only after it settled', async () => {
    const reads = [];
    const read = jest.fn(() => {
      const d = deferred();
      reads.push(d);
      return d.promise;
    });
    const { result } = renderHook(() => useRemediationRead({ accountId: 'acc', eventId: 'ev', readKey: 'ev-0', read, intervalMs: 10000 }));
    act(() => result.current.setPolling(true));
    expect(read).toHaveBeenCalledTimes(1);
    // 25 s pass with the read still out: no second read, nothing thrown away.
    act(() => jest.advanceTimersByTime(25000));
    expect(read).toHaveBeenCalledTimes(1);
    await act(async () => reads[0].resolve(withApproval));
    expect(result.current.res.loop.pending_approvals).toEqual([approval]);
    // The next read is 10 s after it settled.
    act(() => jest.advanceTimersByTime(9000));
    expect(read).toHaveBeenCalledTimes(1);
    act(() => jest.advanceTimersByTime(1000));
    expect(read).toHaveBeenCalledTimes(2);
  });

  it('a reload during a read in flight does not drop it: it reads again once that one settles', async () => {
    const reads = [];
    const read = jest.fn(() => {
      const d = deferred();
      reads.push(d);
      return d.promise;
    });
    const { result } = renderHook(() => useRemediationRead({ accountId: 'acc', eventId: 'ev', readKey: 'ev-0', read }));
    expect(read).toHaveBeenCalledTimes(1);
    act(() => {
      result.current.reload();
      result.current.reload();
    });
    expect(read).toHaveBeenCalledTimes(1);
    await act(async () => reads[0].resolve(withApproval));
    expect(result.current.res.loop.pending_approvals).toEqual([approval]);
    expect(read).toHaveBeenCalledTimes(2);
    await act(async () => reads[1].resolve({ ...withApproval, loop: { plan_status: 'ready' } }));
    expect(result.current.readCount).toBe(2);
  });

  it('pending_approvals_unread keeps the approvals shown; a failed read keeps the last state', async () => {
    const read = jest
      .fn()
      .mockResolvedValueOnce(withApproval)
      .mockResolvedValueOnce({ ...withApproval, loop: { plan_status: 'ready', pending_approvals_unread: true } })
      .mockRejectedValueOnce(new Error('gateway timeout'))
      .mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useRemediationRead({ accountId: 'acc', eventId: 'ev', readKey: 'ev-0', read }));
    await flush();
    expect(result.current.res.loop.pending_approvals).toEqual([approval]);
    await act(async () => result.current.reload());
    expect(result.current.res.loop.pending_approvals).toEqual([approval]);
    expect(result.current.res.loop.pending_approvals_unread).toBe(true);
    await act(async () => result.current.reload());
    expect(result.current.res.loop.pending_approvals).toEqual([approval]);
    await act(async () => result.current.reload());
    expect(result.current.res.loop.pending_approvals).toEqual([approval]);
  });

  it('drops a read only when the key changed while it was out, and reads the new key', async () => {
    const reads = [];
    const read = jest.fn(() => {
      const d = deferred();
      reads.push(d);
      return d.promise;
    });
    const { result, rerender } = renderHook(({ k }) => useRemediationRead({ accountId: 'acc', eventId: 'ev', readKey: k, read }), {
      initialProps: { k: 'probe-ev' },
    });
    rerender({ k: 'ev-1' });
    await act(async () => reads[0].resolve({ actions: [], loop: { plan_status: 'preparing' } }));
    expect(result.current.key).toBe('');
    expect(read).toHaveBeenCalledTimes(2);
    await act(async () => reads[1].resolve(withApproval));
    expect(result.current.key).toBe('ev-1');
  });

  it('a follow-up in the alert’s conversation (from the chat) reads at once; another conversation does not', async () => {
    const read = jest.fn().mockResolvedValue(withApproval);
    const onFollowup = jest.fn();
    renderHook(() => useRemediationRead({ accountId: 'acc', eventId: 'ev', readKey: 'ev-0', read, sessionIds: ['event-fp1'], onFollowup }));
    await flush();
    expect(read).toHaveBeenCalledTimes(1);
    await act(async () => {
      window.dispatchEvent(new CustomEvent('nb:conversation-followup', { detail: { sessionId: 'event-other', kind: 'asked' } }));
    });
    expect(read).toHaveBeenCalledTimes(1);
    await act(async () => {
      window.dispatchEvent(new CustomEvent('nb:conversation-followup', { detail: { sessionId: 'event-fp1', kind: 'asked' } }));
    });
    expect(read).toHaveBeenCalledTimes(2);
    expect(onFollowup).toHaveBeenCalled();
  });
});

describe('useStickyLoop', () => {
  const loopRes = { plan_version: 'pv3', actions: [], loop: { plan_status: 'ready', pending_approvals: [approval] } };

  it('once a read had a loop, a key change and a null, errored or loop-less read never allow the old panel', () => {
    const { result, rerender } = renderHook((p) => useStickyLoop(p), {
      initialProps: { eventId: 'ev', res: loopRes, planKey: 'ev-0', readKey: 'ev-0' },
    });
    expect(result.current).toMatchObject({ loopOn: true, legacy: false });
    // The investigation card remounts: a new plan key, its read still out.
    rerender({ eventId: 'ev', res: loopRes, planKey: 'ev-1', readKey: 'ev-0' });
    expect(result.current).toMatchObject({ loopOn: true, legacy: false });
    // The read fails (null) …
    rerender({ eventId: 'ev', res: null, planKey: 'ev-1', readKey: 'ev-1' });
    expect(result.current).toMatchObject({ loopOn: true, legacy: false });
    expect(result.current.loopRes).toBe(loopRes);
    // … or comes back without a loop (the flag check failed closed on the server).
    rerender({ eventId: 'ev', res: { plan_version: 'pv3', actions: [] }, planKey: 'ev-1', readKey: 'ev-1' });
    expect(result.current).toMatchObject({ loopOn: true, legacy: false });
    expect(result.current.loopRes.loop.pending_approvals).toEqual([approval]);
  });

  it('allows the old panel only after a read of the plan key affirmatively came back without a loop', () => {
    const { result, rerender } = renderHook((p) => useStickyLoop(p), {
      initialProps: { eventId: 'ev', res: null, planKey: 'ev-0', readKey: '' },
    });
    expect(result.current.legacy).toBe(false);
    rerender({ eventId: 'ev', res: { actions: [] }, planKey: 'ev-0', readKey: 'probe-ev' });
    expect(result.current.legacy).toBe(false);
    rerender({ eventId: 'ev', res: { actions: [] }, planKey: 'ev-0', readKey: 'ev-0' });
    expect(result.current).toMatchObject({ loopOn: false, legacy: true });
  });

  it('denied_approvals_unread keeps the denied approvals shown', async () => {
    const denied = [{ message_id: 'm9', command: 'kubectl scale deployment/web --replicas=0', option: 1, denied_at: '2026-10-23T08:54:00Z' }];
    const read = jest
      .fn()
      .mockResolvedValueOnce({ actions: [], loop: { plan_status: 'ready', denied_approvals: denied } })
      .mockResolvedValueOnce({ actions: [], loop: { plan_status: 'ready', denied_approvals_unread: true } });
    const { result } = renderHook(() => useRemediationRead({ accountId: 'acc', eventId: 'ev', readKey: 'ev-0', read }));
    await flush();
    await act(async () => result.current.reload());
    expect(result.current.res.loop.denied_approvals).toEqual(denied);
  });
});

describe('remediationReadKeyFor', () => {
  it('reads before the analysis completes only with the loop flag on', () => {
    expect(remediationReadKeyFor({ accountId: 'a', eventId: 'e', planKey: '', loopFlag: true })).toBe('probe-e');
    expect(remediationReadKeyFor({ accountId: 'a', eventId: 'e', planKey: '', loopFlag: false })).toBe('');
    expect(remediationReadKeyFor({ accountId: 'a', eventId: 'e', planKey: '', loopFlag: null })).toBe('');
  });
  it('reads once per analysis whatever the flag', () => {
    for (const loopFlag of [true, false, null]) {
      expect(remediationReadKeyFor({ accountId: 'a', eventId: 'e', planKey: 'e-0', loopFlag })).toBe('e-0');
    }
    expect(remediationReadKeyFor({ accountId: '', eventId: 'e', planKey: 'e-0', loopFlag: true })).toBe('');
  });
});

describe('remediationPanelsFor', () => {
  const noRead = { loopOn: false, legacy: false };
  it('flag off: the old panel shows whatever the read returned (failed, null on demo), never the loop panel', () => {
    expect(remediationPanelsFor({ loopFlag: false, sticky: noRead })).toEqual({ loopOn: false, legacy: true });
    expect(remediationPanelsFor({ loopFlag: false, sticky: { loopOn: true, legacy: false } })).toEqual({ loopOn: false, legacy: true });
  });
  it('flag on: the loop panel follows the read, the old panel only after a read without a loop', () => {
    expect(remediationPanelsFor({ loopFlag: true, sticky: { loopOn: true, legacy: false } })).toEqual({ loopOn: true, legacy: false });
    expect(remediationPanelsFor({ loopFlag: true, sticky: noRead })).toEqual({ loopOn: false, legacy: false });
    expect(remediationPanelsFor({ loopFlag: true, sticky: { loopOn: false, legacy: true } })).toEqual({ loopOn: false, legacy: true });
  });
  it('flag still being read: neither panel', () => {
    expect(remediationPanelsFor({ loopFlag: null, sticky: { loopOn: true, legacy: true } })).toEqual({ loopOn: false, legacy: false });
  });
});
