import { getCurrentTenant } from '@lib/auth';
import { readPersistedFilters, writePersistedFilters } from '@hooks/usePersistedFilters';

/**
 * The Account filter is page-level on /automation, not tab-level: picking
 * accounts on Automations and finding Executions unfiltered reads as a bug.
 * So it lives under one key both tabs share, while every other filter stays
 * scoped to the tab that owns it.
 *
 * The two tabs also spell the account param differently in the URL
 * (`?account=` repeated vs `?accounts=` csv), so this store — not the query —
 * is what carries the selection across a tab switch.
 *
 * Scoped by tenant: account ids are tenant-specific.
 */
const ACCOUNT_FILTER_STORAGE_KEY = 'automation:accounts:v1';

interface PersistedAccountFilter {
  [key: string]: unknown;
  accounts?: string[];
}

const accountFilterKey = (): string | null => {
  const tenantId = getCurrentTenant()?.id;
  return tenantId ? `${ACCOUNT_FILTER_STORAGE_KEY}:${tenantId}` : null;
};

export const readPersistedAccounts = (): string[] => readPersistedFilters<PersistedAccountFilter>(accountFilterKey()).accounts || [];

export const writePersistedAccounts = (accounts: string[]): void => writePersistedFilters(accountFilterKey(), { accounts });
