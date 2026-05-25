import { renderHook, act } from '@testing-library/react';
import { useRcaPolling } from '@hooks/useRcaPolling';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    generateRCA: jest.fn(),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

import apiKubernetes from '@api1/kubernetes';
import { snackbar } from '@components1/common/snackbarService';

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

  it('shows success snackbar and stops polling when status is COMPLETED', async () => {
    mockGenerateRCA.mockResolvedValue({ status: 'completed' });
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));

    await act(async () => {
      result.current.startPolling();
      jest.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(snackbar.success).toHaveBeenCalledWith('RCA generation completed successfully. Please refresh the page to see the results.');
    expect(result.current.isPolling).toBe(false);
  });

  it('shows error snackbar and stops polling when status is FAILED', async () => {
    mockGenerateRCA.mockResolvedValue({ status: 'failed' });
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));

    await act(async () => {
      result.current.startPolling();
      jest.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(snackbar.error).toHaveBeenCalledWith('RCA generation failed. Please try again later.');
    expect(result.current.isPolling).toBe(false);
  });

  it('shows error snackbar and stops polling on API exception', async () => {
    mockGenerateRCA.mockRejectedValue(new Error('Network error'));
    const { result } = renderHook(() => useRcaPolling('event-1', 'acc-1'));

    await act(async () => {
      result.current.startPolling();
      jest.advanceTimersByTime(5000);
      await Promise.resolve();
    });

    expect(snackbar.error).toHaveBeenCalledWith('Failed to check RCA status. Please refresh the page.');
    expect(result.current.isPolling).toBe(false);
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

  it('cleans up polling interval on unmount', () => {
    const clearIntervalSpy = jest.spyOn(global, 'clearInterval');
    mockGenerateRCA.mockReturnValue(new Promise(() => {}));
    const { result, unmount } = renderHook(() => useRcaPolling('event-1', 'acc-1'));
    act(() => result.current.startPolling());
    unmount();
    // After unmount the interval should be cleared; advancing timers should not cause issues
    act(() => jest.advanceTimersByTime(10000));
    expect(clearIntervalSpy).toHaveBeenCalled();
    clearIntervalSpy.mockRestore();
  });
});
