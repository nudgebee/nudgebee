jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { listCloudAccountFirstSync: jest.fn() } }));
jest.mock('@lib/auth', () => ({ getCurrentTenant: (): { id: string } => ({ id: 't1' }) }));

import { act, renderHook } from '@testing-library/react';
import apiKubernetes1 from '@api1/kubernetes1';
import { evaluateCloudFirstSync, useCloudFirstSync } from '@components/integrations/CloudFirstSync';

const listMock = apiKubernetes1.listCloudAccountFirstSync as jest.Mock;

// A fixed instant well in the past — every `spendsStatus()` default below
// (computed at call time, so it reads "now" under both real and faked
// clocks) is always after it, which is the "fresh" case every non-staleness
// test exercises. #41117's own staleness tests set `updated_at` explicitly.
const STARTED_AT = Date.parse('2026-09-16T11:00:00Z');
const spendsStatus = (err = '', updatedAt = new Date().toISOString()): string => JSON.stringify({ spends: { err, updated_at: updatedAt } });

describe('evaluateCloudFirstSync', () => {
  it('waits while an account has no agent row yet', () => {
    expect(evaluateCloudFirstSync(['a1'], [], STARTED_AT)).toEqual({ allReported: false, issues: [] });
  });

  it('waits on a CONNECTED row the events job wrote before the cost sync reported', () => {
    const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: JSON.stringify({ events: { err: '' } }) }];
    expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).allReported).toBe(false);
  });

  it('is done when every account has a clean cost sync', () => {
    const rows = [
      { cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: spendsStatus() },
      { cloud_account_id: 'a2', status: 'CONNECTED', status_message: null, connection_status: { spends: { updated_at: '2026-09-16T11:50:09Z' } } },
    ];
    expect(evaluateCloudFirstSync(['a1', 'a2'], rows, STARTED_AT)).toEqual({ allReported: true, issues: [] });
  });

  it('keeps waiting until every account has reported', () => {
    const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', connection_status: spendsStatus() }];
    expect(evaluateCloudFirstSync(['a1', 'a2'], rows, STARTED_AT).allReported).toBe(false);
  });

  it('reports a failed cost sync', () => {
    const rows = [
      { cloud_account_id: 'a1', status: 'NOT_CONNECTED', status_message: 'Cost Management API: forbidden', connection_status: spendsStatus() },
    ];
    expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT)).toEqual({
      allReported: true,
      issues: [{ accountId: 'a1', failed: true, message: 'Cost Management API: forbidden' }],
    });
  });

  it('reports a warning when the sync finished CONNECTED with a message', () => {
    const rows = [
      { cloud_account_id: 'a1', status: 'CONNECTED', status_message: 'cost reporting is not configured', connection_status: spendsStatus() },
    ];
    expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).issues).toEqual([
      { accountId: 'a1', failed: false, message: 'cost reporting is not configured' },
    ]);
  });

  it('falls back to the spends error when status_message is empty', () => {
    const rows = [{ cloud_account_id: 'a1', status: 'NOT_CONNECTED', status_message: '', connection_status: spendsStatus('bucket missing') }];
    expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).issues[0].message).toBe('bucket missing');
  });

  // #41117: `connection_status` is `agent.connection_status || excluded` on
  // the agent row, which already existed for a reconnected account — a
  // `spends` key alone can be the PREVIOUS sync's, not this wait's.
  describe('stale spends — #41117', () => {
    it('keeps waiting on a spends key written well before this wait started', () => {
      // A sync that finished hours before this wait began — a reconnect's
      // leftover result, not this run's.
      const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: spendsStatus('', '2026-09-16T05:00:00Z') }];
      expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).allReported).toBe(false);
    });

    it('accepts a spends key written just after this wait started', () => {
      const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: spendsStatus('', '2026-09-16T11:00:05Z') }];
      expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).allReported).toBe(true);
    });

    it('tolerates a few seconds of clock skew just before the start time', () => {
      const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: spendsStatus('', '2026-09-16T10:59:55Z') }];
      expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).allReported).toBe(true);
    });

    it('trusts a row with no timestamp at all, rather than waiting on it forever', () => {
      const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: JSON.stringify({ spends: {} }) }];
      expect(evaluateCloudFirstSync(['a1'], rows, STARTED_AT).allReported).toBe(true);
    });
  });
});

describe('useCloudFirstSync', () => {
  beforeEach(() => {
    jest.useFakeTimers();
    listMock.mockReset();
  });
  afterEach(() => {
    jest.useRealTimers();
  });

  const flush = async (ms = 0): Promise<void> => {
    await act(async () => {
      jest.advanceTimersByTime(ms);
      await Promise.resolve();
    });
  };

  it('stays idle and never polls without account ids', async () => {
    const { result } = renderHook(() => useCloudFirstSync([], 'AWS'));
    await flush(60000);
    expect(result.current.phase).toBe('idle');
    expect(listMock).not.toHaveBeenCalled();
  });

  it('polls until the cost sync reports, then is done', async () => {
    listMock
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: spendsStatus() }]);
    const { result } = renderHook(() => useCloudFirstSync(['a1'], 'AWS'));
    await flush();
    expect(result.current.phase).toBe('waiting');
    expect(listMock).toHaveBeenCalledWith(['a1'], 'AWS');
    await flush(10000);
    expect(result.current.phase).toBe('done');
    await flush(30000);
    expect(listMock).toHaveBeenCalledTimes(2);
  });

  it('stops on a failed sync with the issue', async () => {
    listMock.mockResolvedValue([{ cloud_account_id: 'a1', status: 'NOT_CONNECTED', status_message: 'forbidden', connection_status: spendsStatus() }]);
    const { result } = renderHook(() => useCloudFirstSync(['a1'], 'Azure'));
    await flush();
    expect(result.current).toEqual({ phase: 'attention', issues: [{ accountId: 'a1', failed: true, message: 'forbidden' }] });
  });

  it('gives up waiting after three minutes', async () => {
    listMock.mockResolvedValue([]);
    const { result } = renderHook(() => useCloudFirstSync(['a1'], 'GCP'));
    await flush();
    for (let i = 0; i < 18; i += 1) {
      await flush(10000);
    }
    expect(result.current.phase).toBe('timeout');
    const calls = listMock.mock.calls.length;
    await flush(60000);
    expect(listMock).toHaveBeenCalledTimes(calls);
  });
});
