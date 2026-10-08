jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { listCloudAccountFirstSync: jest.fn() } }));
jest.mock('@lib/auth', () => ({ getCurrentTenant: (): { id: string } => ({ id: 't1' }) }));

import { act, renderHook } from '@testing-library/react';
import apiKubernetes1 from '@api1/kubernetes1';
import { evaluateCloudFirstSync, useCloudFirstSync } from '@components/integrations/CloudFirstSync';

const listMock = apiKubernetes1.listCloudAccountFirstSync as jest.Mock;

const spendsStatus = (err = ''): string => JSON.stringify({ spends: { err, updated_at: '2026-09-16T11:50:09Z' } });

describe('evaluateCloudFirstSync', () => {
  it('waits while an account has no agent row yet', () => {
    expect(evaluateCloudFirstSync(['a1'], [])).toEqual({ allReported: false, issues: [] });
  });

  it('waits on a CONNECTED row the events job wrote before the cost sync reported', () => {
    const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: JSON.stringify({ events: { err: '' } }) }];
    expect(evaluateCloudFirstSync(['a1'], rows).allReported).toBe(false);
  });

  it('is done when every account has a clean cost sync', () => {
    const rows = [
      { cloud_account_id: 'a1', status: 'CONNECTED', status_message: '', connection_status: spendsStatus() },
      { cloud_account_id: 'a2', status: 'CONNECTED', status_message: null, connection_status: { spends: {} } },
    ];
    expect(evaluateCloudFirstSync(['a1', 'a2'], rows)).toEqual({ allReported: true, issues: [] });
  });

  it('keeps waiting until every account has reported', () => {
    const rows = [{ cloud_account_id: 'a1', status: 'CONNECTED', connection_status: spendsStatus() }];
    expect(evaluateCloudFirstSync(['a1', 'a2'], rows).allReported).toBe(false);
  });

  it('reports a failed cost sync', () => {
    const rows = [
      { cloud_account_id: 'a1', status: 'NOT_CONNECTED', status_message: 'Cost Management API: forbidden', connection_status: spendsStatus() },
    ];
    expect(evaluateCloudFirstSync(['a1'], rows)).toEqual({
      allReported: true,
      issues: [{ accountId: 'a1', failed: true, message: 'Cost Management API: forbidden' }],
    });
  });

  it('reports a warning when the sync finished CONNECTED with a message', () => {
    const rows = [
      { cloud_account_id: 'a1', status: 'CONNECTED', status_message: 'cost reporting is not configured', connection_status: spendsStatus() },
    ];
    expect(evaluateCloudFirstSync(['a1'], rows).issues).toEqual([{ accountId: 'a1', failed: false, message: 'cost reporting is not configured' }]);
  });

  it('falls back to the spends error when status_message is empty', () => {
    const rows = [{ cloud_account_id: 'a1', status: 'NOT_CONNECTED', status_message: '', connection_status: spendsStatus('bucket missing') }];
    expect(evaluateCloudFirstSync(['a1'], rows).issues[0].message).toBe('bucket missing');
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
