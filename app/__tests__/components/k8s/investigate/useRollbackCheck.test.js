import { act, renderHook, waitFor } from '@testing-library/react';

jest.mock('@api1/recommendation', () => ({ __esModule: true, default: { checkEventRollback: jest.fn() } }));

import apiRecommendations from '@api1/recommendation';
import { ROLLBACK_CHECK_WAIT_MS, useRollbackCheck } from '@components/k8s/investigate/useRollbackCheck';

const target = (changeEventId, enabled = true) => ({ accountId: 'acc', eventId: 'alert-1', changeEventId, enabled });

describe('useRollbackCheck', () => {
  beforeEach(() => jest.clearAllMocks());
  afterEach(() => jest.useRealTimers());

  it('asks nothing while it is not enabled', () => {
    const { result } = renderHook((props) => useRollbackCheck(props), { initialProps: target('c1', false) });
    expect(result.current.status).toBe('checking');
    expect(apiRecommendations.checkEventRollback).not.toHaveBeenCalled();
  });

  it('strips the subsystem prefix from the reason', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: false, reason: 'event resolution: "spec.replicas" was changed again' });
    const { result } = renderHook((props) => useRollbackCheck(props), { initialProps: target('c1') });
    await waitFor(() => expect(result.current.status).toBe('blocked'));
    expect(result.current.reason).toBe('"spec.replicas" was changed again');
  });

  // The dialog enables Roll back on "allowed". Picking another change must not leave the first
  // change's answer standing in for it, not even for the one render before the new question is asked.
  it('never reads one change’s answer as another’s', async () => {
    const statuses = [];
    apiRecommendations.checkEventRollback.mockImplementation((_account, _event, changeEventId) =>
      changeEventId === 'c1' ? Promise.resolve({ allowed: true, reason: '' }) : new Promise(() => {})
    );
    const { result, rerender } = renderHook(
      (props) => {
        const check = useRollbackCheck(props);
        statuses.push(`${props.changeEventId}:${check.status}`);
        return check;
      },
      { initialProps: target('c1') }
    );
    await waitFor(() => expect(result.current.status).toBe('allowed'));

    rerender(target('c2'));
    expect(result.current.status).toBe('checking');
    expect(statuses).not.toContain('c2:allowed');

    // Back to the first change: asked again, not remembered.
    rerender(target('c1'));
    expect(result.current.status).toBe('checking');
    await waitFor(() => expect(result.current.status).toBe('allowed'));
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledTimes(3);
  });

  it('forgets the answer when it is switched off, and asks again when switched back on', async () => {
    apiRecommendations.checkEventRollback.mockResolvedValue({ allowed: true, reason: '' });
    const { result, rerender } = renderHook((props) => useRollbackCheck(props), { initialProps: target('c1') });
    await waitFor(() => expect(result.current.status).toBe('allowed'));

    rerender(target('c1', false));
    expect(result.current.status).toBe('checking');
    apiRecommendations.checkEventRollback.mockReturnValue(new Promise(() => {}));
    rerender(target('c1'));
    expect(result.current.status).toBe('checking');
    expect(apiRecommendations.checkEventRollback).toHaveBeenCalledTimes(2);
  });

  // No answer must not hold the rollback back for good: the apply checks again on the click.
  it('stops waiting for an answer that does not come, and still takes it if it arrives late', async () => {
    jest.useFakeTimers();
    let answer;
    apiRecommendations.checkEventRollback.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    const { result } = renderHook((props) => useRollbackCheck(props), { initialProps: target('c1') });
    expect(result.current.status).toBe('checking');

    act(() => jest.advanceTimersByTime(ROLLBACK_CHECK_WAIT_MS));
    expect(result.current.status).toBe('unknown');

    await act(async () => answer({ allowed: false, reason: 'event resolution: changed again' }));
    expect(result.current.status).toBe('blocked');
  });
});
