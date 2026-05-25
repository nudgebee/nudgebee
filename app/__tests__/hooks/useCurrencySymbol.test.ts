import { renderHook, waitFor } from '@testing-library/react';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    listCloudAccountTrend: jest.fn(),
  },
}));

import apiCloudAccount from '@api1/cloud-account';
const mockList = apiCloudAccount.listCloudAccountTrend as jest.Mock;

describe('useCurrencySymbol', () => {
  beforeEach(() => jest.clearAllMocks());

  it('returns undefined initially (while loading)', () => {
    mockList.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    expect(result.current).toBeUndefined();
  });

  it('returns undefined when accountId is undefined', () => {
    const { result } = renderHook(() => useCurrencySymbol(undefined));
    expect(result.current).toBeUndefined();
    expect(mockList).not.toHaveBeenCalled();
  });

  it('returns "$" when currency_type is USD', async () => {
    mockList.mockResolvedValue({ data: { spend_groupings: [{ currency_type: 'USD' }] } });
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('returns "₹" when currency_type is INR', async () => {
    mockList.mockResolvedValue({ data: { spend_groupings: [{ currency_type: 'INR' }] } });
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('₹'));
  });

  it('returns default "$" for unknown currency_type', async () => {
    mockList.mockResolvedValue({ data: { spend_groupings: [{ currency_type: 'EUR' }] } });
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('returns default "$" when no currency_type in response', async () => {
    mockList.mockResolvedValue({ data: { spend_groupings: [{}] } });
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('returns default "$" when API call fails', async () => {
    mockList.mockRejectedValue(new Error('Network error'));
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('resets to undefined when accountId changes', async () => {
    mockList.mockResolvedValue({ data: { spend_groupings: [{ currency_type: 'USD' }] } });
    const { result, rerender } = renderHook(({ id }) => useCurrencySymbol(id), {
      initialProps: { id: 'acc-1' as string | undefined },
    });
    await waitFor(() => expect(result.current).toBe('$'));

    rerender({ id: undefined });
    expect(result.current).toBeUndefined();
  });
});
