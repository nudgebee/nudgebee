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

    let active = true;
    setCurrencySymbol(undefined);
    apiCloudAccount
      .getAccountCurrency(accountId)
      .then((currencyType: string | null) => {
        if (active) {
          setCurrencySymbol(currencyType ? CURRENCY_MAP[currencyType] ?? DEFAULT_CURRENCY : DEFAULT_CURRENCY);
        }
      })
      .catch(() => {
        if (active) {
          setCurrencySymbol(DEFAULT_CURRENCY);
        }
      });

    return () => {
      active = false;
    };
  }, [accountId]);

  return currencySymbol;
};

export default useCurrencySymbol;
