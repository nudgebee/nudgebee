import { useCallback, useEffect, useState } from 'react';
import homeApi from '@api1/home';
import { useLatestRequest } from '@components/vm/common';

export interface AccountOption {
  label: string;
  value: string;
  cloud_provider: string;
  group: string;
}

// Active cloud accounts of every provider, shaped for the DS Select (grouped by provider).
// Shared by the trigger config sidebar (which uses them as the Cluster picker) and the
// read-only trigger details panel (which only needs the id -> provider lookup, to label the
// overloaded cluster/namespace fields). getCloudAccounts caches for an hour, so mounting
// this in both places costs one request.
export function useAccountOptions(enabled = true) {
  const [options, setOptions] = useState<AccountOption[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const beginRequest = useLatestRequest();

  useEffect(() => {
    if (!enabled) {
      return;
    }
    const isLatest = beginRequest();
    setIsLoading(true);
    homeApi
      .getCloudAccounts('')
      .then((accounts: any) => {
        if (!isLatest()) {
          return;
        }
        setOptions(
          (accounts || [])
            .filter((a: any) => a.id !== 'demo')
            .map((a: any) => ({
              label: a.account_name,
              value: a.id,
              cloud_provider: a.cloud_provider,
              group: a.cloud_provider || 'Other',
            }))
        );
      })
      .catch((error) => {
        console.error('Failed to fetch cloud account options:', error);
        if (isLatest()) {
          setOptions([]);
        }
      })
      .finally(() => {
        if (isLatest()) {
          setIsLoading(false);
        }
      });
  }, [enabled, beginRequest]);

  const providerOf = useCallback(
    (accountId?: string) => (accountId ? options.find((o) => o.value === accountId)?.cloud_provider || '' : ''),
    [options]
  );

  return { options, isLoading, providerOf };
}
