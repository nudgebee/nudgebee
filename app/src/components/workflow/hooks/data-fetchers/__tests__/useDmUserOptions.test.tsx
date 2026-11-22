import { act, renderHook, waitFor } from '@testing-library/react';
import apiAccount from '@api1/account';
import { DM_USERS_LOAD_ERROR, DM_USERS_RECONNECT_ERROR, useDmUserOptions } from '@components/workflow/hooks/data-fetchers/useDmUserOptions';

jest.mock('@api1/account', () => ({ __esModule: true, default: { getNotificationUserList: jest.fn() } }));

const mockUserList = apiAccount.getNotificationUserList as jest.Mock;

describe('useDmUserOptions', () => {
  beforeEach(() => mockUserList.mockReset());

  it('reports a failed load as an error, then loads the users on retry', async () => {
    mockUserList.mockRejectedValueOnce(new Error('rate limited')).mockResolvedValueOnce({ data: { data: [{ id: 'U1', display_name: 'Alice' }] } });
    const { result } = renderHook(() => useDmUserOptions('slack'));

    await waitFor(() => expect(result.current.error).toBe(DM_USERS_LOAD_ERROR));
    expect(result.current.options).toEqual([]);

    act(() => result.current.retry());
    await waitFor(() => expect(result.current.options).toEqual([{ label: 'Alice', value: 'U1' }]));
    expect(result.current.error).toBeNull();
    expect(mockUserList).toHaveBeenCalledTimes(2);
  });

  it('asks for a reconnect instead of offering Retry when Slack auth fails', async () => {
    mockUserList.mockRejectedValueOnce(new Error('{"error":{"code":"reconnect_required","message":"m"}}'));
    const { result } = renderHook(() => useDmUserOptions('slack'));

    await waitFor(() => expect(result.current.error).toBe(DM_USERS_RECONNECT_ERROR));
    expect(result.current.retryable).toBe(false);
  });

  it('does not fetch without a provider', () => {
    const { result } = renderHook(() => useDmUserOptions(''));
    expect(mockUserList).not.toHaveBeenCalled();
    expect(result.current).toMatchObject({ options: [], loading: false, error: null });
  });
});
