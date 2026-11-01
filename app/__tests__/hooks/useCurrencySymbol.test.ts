import { renderHook, waitFor } from '@testing-library/react';
import { useCurrencySymbol } from '@hooks/useCurrencySymbol';

jest.mock('@api1/cloud-account', () => ({
  __esModule: true,
  default: {
    getAccountCurrency: jest.fn(),
  },
}));

import apiCloudAccount from '@api1/cloud-account';
const mockCurrency = apiCloudAccount.getAccountCurrency as jest.Mock;

describe('useCurrencySymbol', () => {
  beforeEach(() => jest.clearAllMocks());

  it('returns undefined initially (while loading)', () => {
    mockCurrency.mockReturnValue(new Promise(() => {}));
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    expect(result.current).toBeUndefined();
  });

  it('returns undefined when accountId is undefined', () => {
    const { result } = renderHook(() => useCurrencySymbol(undefined));
    expect(result.current).toBeUndefined();
    expect(mockCurrency).not.toHaveBeenCalled();
  });

  it('returns "$" when currency_type is USD', async () => {
    mockCurrency.mockResolvedValue('USD');
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('returns "₹" when currency_type is INR', async () => {
    mockCurrency.mockResolvedValue('INR');
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('₹'));
  });

  it('returns default "$" for unknown currency_type', async () => {
    mockCurrency.mockResolvedValue('EUR');
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('returns default "$" when the account has no currency', async () => {
    mockCurrency.mockResolvedValue(null);
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('returns default "$" when the lookup fails', async () => {
    mockCurrency.mockRejectedValue(new Error('Network error'));
    const { result } = renderHook(() => useCurrencySymbol('acc-1'));
    await waitFor(() => expect(result.current).toBe('$'));
  });

  it('resets to undefined when accountId changes', async () => {
    mockCurrency.mockResolvedValue('USD');
    const { result, rerender } = renderHook(({ id }) => useCurrencySymbol(id), {
      initialProps: { id: 'acc-1' as string | undefined },
    });
    await waitFor(() => expect(result.current).toBe('$'));

    rerender({ id: undefined });
    expect(result.current).toBeUndefined();
  });
});
