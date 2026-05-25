import { renderHook, act } from '@testing-library/react';
import useTriggerAnomaly from '@hooks/useTriggerAnomaly';

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    triggerAnomalyExecute: jest.fn(),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

import apiKubernetes1 from '@api1/kubernetes1';
import { snackbar } from '@components1/common/snackbarService';

const mockTrigger = apiKubernetes1.triggerAnomalyExecute as jest.Mock;

describe('useTriggerAnomaly', () => {
  beforeEach(() => jest.clearAllMocks());

  it('initialises with isLoading=false', () => {
    const { result } = renderHook(() => useTriggerAnomaly('acc-1'));
    expect(result.current.isLoading).toBe(false);
  });

  it('sets isLoading=true during execution then false after', async () => {
    mockTrigger.mockResolvedValue({ data: { data: { trigger_anomaly_execute: { status: 'triggered' } } } });
    const { result } = renderHook(() => useTriggerAnomaly('acc-1'));

    await act(async () => {
      await result.current.triggerAnomaly();
    });
    expect(result.current.isLoading).toBe(false);
  });

  it('shows success snackbar when status is "triggered"', async () => {
    mockTrigger.mockResolvedValue({ data: { data: { trigger_anomaly_execute: { status: 'triggered' } } } });
    const { result } = renderHook(() => useTriggerAnomaly('acc-1'));
    await act(async () => {
      await result.current.triggerAnomaly();
    });
    expect(snackbar.success).toHaveBeenCalledWith('Anomaly detection triggered successfully');
  });

  it('shows error snackbar when response has errors', async () => {
    mockTrigger.mockResolvedValue({ data: { errors: [{ message: 'server error' }] } });
    const { result } = renderHook(() => useTriggerAnomaly('acc-1'));
    await act(async () => {
      await result.current.triggerAnomaly();
    });
    expect(snackbar.error).toHaveBeenCalledWith('Failed to trigger anomaly detection');
  });

  it('shows custom message from response when available', async () => {
    mockTrigger.mockResolvedValue({
      data: { data: { trigger_anomaly_execute: { status: 'scheduled', message: 'Already running' } } },
    });
    const { result } = renderHook(() => useTriggerAnomaly('acc-1'));
    await act(async () => {
      await result.current.triggerAnomaly();
    });
    expect(snackbar.success).toHaveBeenCalledWith('Already running');
  });

  it('shows error snackbar and resets isLoading on exception', async () => {
    mockTrigger.mockRejectedValue(new Error('Network timeout'));
    const { result } = renderHook(() => useTriggerAnomaly('acc-1'));
    await act(async () => {
      await result.current.triggerAnomaly();
    });
    expect(snackbar.error).toHaveBeenCalledWith('Failed to trigger anomaly detection');
    expect(result.current.isLoading).toBe(false);
  });

  it('calls triggerAnomalyExecute with the accountId', async () => {
    mockTrigger.mockResolvedValue({ data: { data: { trigger_anomaly_execute: { status: 'triggered' } } } });
    const { result } = renderHook(() => useTriggerAnomaly('my-account'));
    await act(async () => {
      await result.current.triggerAnomaly();
    });
    expect(mockTrigger).toHaveBeenCalledWith('my-account');
  });
});
