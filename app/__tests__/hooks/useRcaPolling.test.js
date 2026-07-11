import { renderHook, act } from '@testing-library/react';
import { useRcaPolling } from '@hooks/useRcaPolling';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    generateRCA: jest.fn(),
  },
}));

import apiKubernetes from '@api1/kubernetes';

const mockGenerateRCA = apiKubernetes.generateRCA;

describe('useRcaPolling', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('initialises with isPolling=false', () => {
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));
    expect(result.current.isPolling).toBe(false);
  });

  it('startPolling sets isPolling=true', async () => {
    mockGenerateRCA.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));
    await act(async () => {
      result.current.startPolling();
    });
    expect(result.current.isPolling).toBe(true);
  });

  it('stopPolling sets isPolling=false', async () => {
    mockGenerateRCA.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));
    await act(async () => {
      result.current.startPolling();
    });
    expect(result.current.isPolling).toBe(true);

    await act(async () => {
      result.current.stopPolling();
    });
    expect(result.current.isPolling).toBe(false);
  });

  it('invokes onStatusChange with COMPLETED and stops polling', async () => {
    const onStatusChange = jest.fn();
    mockGenerateRCA.mockResolvedValue({ status: 'completed' });
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1', onStatusChange));

    await act(async () => {
      result.current.startPolling();
      await jest.advanceTimersByTimeAsync(5000);
    });

    expect(onStatusChange).toHaveBeenCalledWith({ status: 'completed' }, 'COMPLETED');
    expect(result.current.isPolling).toBe(false);
  });

  it('invokes onStatusChange with FAILED and stops polling', async () => {
    const onStatusChange = jest.fn();
    mockGenerateRCA.mockResolvedValue({ status: 'failed' });
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1', onStatusChange));

    await act(async () => {
      result.current.startPolling();
      await jest.advanceTimersByTimeAsync(5000);
    });

    expect(onStatusChange).toHaveBeenCalledWith({ status: 'failed' }, 'FAILED');
    expect(result.current.isPolling).toBe(false);
  });

  it('keeps polling on API exception (retries with backoff)', async () => {
    mockGenerateRCA.mockRejectedValue(new Error('Network error'));
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));

    await act(async () => {
      result.current.startPolling();
      await jest.advanceTimersByTimeAsync(5000);
    });

    expect(errorSpy).toHaveBeenCalledWith('Error polling RCA status:', expect.any(Error));
    expect(result.current.isPolling).toBe(true);
    expect(mockGenerateRCA).toHaveBeenCalled();
    errorSpy.mockRestore();
  });

  it('polls on interval every 5 seconds', async () => {
    mockGenerateRCA.mockResolvedValue({ status: 'IN_PROGRESS' });
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));

    await act(async () => {
      result.current.startPolling();
      await Promise.resolve();
    });

    const callCountAfterFirst = mockGenerateRCA.mock.calls.length;

    await act(async () => {
      jest.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(mockGenerateRCA.mock.calls.length).toBeGreaterThan(callCountAfterFirst);
  });

  it('cleans up polling timeout on unmount', () => {
    const clearTimeoutSpy = jest.spyOn(global, 'clearTimeout');
    mockGenerateRCA.mockReturnValue(new Promise(() => {}));
    const { result, unmount } = renderHook(() => useRcaPolling('event-1', 'acc-1'));
    act(() => result.current.startPolling());
    unmount();
    // After unmount the timeout should be cleared; advancing timers should not cause issues
    act(() => jest.advanceTimersByTime(10000));
    expect(clearTimeoutSpy).toHaveBeenCalled();
    clearTimeoutSpy.mockRestore();
  });
});
