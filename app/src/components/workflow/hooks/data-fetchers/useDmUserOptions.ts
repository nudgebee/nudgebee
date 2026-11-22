import { useCallback, useEffect, useState } from 'react';
import apiAccount from '@api1/account';

export const DM_USERS_LOAD_ERROR = 'Could not load users. Try again in a moment.';
export const DM_USERS_RECONNECT_ERROR = 'Slack access was revoked or lacks the users:read scope. Reconnect Slack in Integrations.';

export type DmUserOption = { label: string; value: string };

type SlackUser = { id: string; display_name?: string; real_name?: string; name?: string };

export type DmUserOptionsState = { options: DmUserOption[]; loading: boolean; error: string | null; retryable: boolean; retry: () => void };

// Recipients for a notifications.dm step. A failed load is a retryable error,
// never an empty list that reads as "nobody to message" (#41063).
export function useDmUserOptions(provider: string): DmUserOptionsState {
  const [options, setOptions] = useState<DmUserOption[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retryable, setRetryable] = useState(true);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    setError(null);
    setRetryable(true);
    if (!provider) {
      setOptions([]);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    apiAccount
      .getNotificationUserList(provider)
      .then((res: { data?: { data?: SlackUser[] } }) => {
        if (cancelled) {
          return;
        }
        setOptions(
          (res?.data?.data || []).map((item) => ({
            label: item.display_name || item.real_name || item.name || item.id,
            value: item.id,
          }))
        );
      })
      .catch((err: unknown) => {
        if (cancelled) {
          return;
        }
        // The gateway passes the server's error body through as the message text.
        const reconnect = String((err as Error)?.message).includes('reconnect_required');
        setOptions([]);
        setRetryable(!reconnect);
        setError(reconnect ? DM_USERS_RECONNECT_ERROR : DM_USERS_LOAD_ERROR);
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [provider, attempt]);

  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  return { options, loading, error, retryable, retry };
}
