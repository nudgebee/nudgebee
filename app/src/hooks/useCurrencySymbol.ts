import { useState, useEffect } from 'react';
import apiCloudAccount from '@api1/cloud-account';

const CURRENCY_MAP: { [key: string]: string } = {
  USD: '$',
  INR: '₹',
};

const DEFAULT_CURRENCY = '$';

/**
 * Custom hook to fetch and manage currency symbol for a cloud account.
 * Returns undefined while loading to allow components to show loading state.
 *
 * @param accountId - The cloud account ID to fetch currency for
 * @returns The currency symbol ('$', '₹', etc.) or undefined while loading
 */
export const useCurrencySymbol = (accountId: string | undefined): string | undefined => {
  const [currencySymbol, setCurrencySymbol] = useState<string | undefined>(undefined);

  useEffect(() => {
    if (!accountId) {
      setCurrencySymbol(undefined);
      return;
    }

    // Reset to undefined when account changes - will show loading state
    setCurrencySymbol(undefined);

    apiCloudAccount
      .listCloudAccountTrend({ accountId: accountId }, new Date(Date.now() - 7 * 24 * 60 * 60 * 1000), new Date(), 'Day')
      .then((res: any) => {
        const firstRecord = res?.data?.spend_groupings?.[0];
        if (firstRecord?.currency_type) {
          setCurrencySymbol(CURRENCY_MAP[firstRecord.currency_type] || DEFAULT_CURRENCY);
        } else {
          setCurrencySymbol(DEFAULT_CURRENCY);
        }
      })
      .catch((error) => {
        console.error('Failed to fetch currency type:', error);
        setCurrencySymbol(DEFAULT_CURRENCY);
      });
  }, [accountId]);

  return currencySymbol;
};

export default useCurrencySymbol;
