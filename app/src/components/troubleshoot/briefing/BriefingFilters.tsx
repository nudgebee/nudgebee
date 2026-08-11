import { useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import FilterDropdown from '@ui/FilterDropdown';
import apiHome from '@api1/home';
import { applyFiltersOnRouter } from '@lib/router';
import { useBriefingWindow } from './useBriefingData';

export const RANGE_OPTIONS: { label: string; value: string; minutes: number }[] = [
  { label: 'Last 30 mins', value: '30m', minutes: 30 },
  { label: 'Last 1 hr', value: '1h', minutes: 60 },
  { label: 'Last 3 hrs', value: '3h', minutes: 180 },
  { label: 'Last 12 hrs', value: '12h', minutes: 720 },
  { label: 'Last 24 hrs', value: '24h', minutes: 1440 },
  { label: 'Last 7 days', value: '7d', minutes: 10080 },
];

const DEFAULT_RANGE = '24h';

export const matchRange = (startMs: number, endMs: number): string => {
  const minutes = Math.round((endMs - startMs) / 60000);
  const nearest = RANGE_OPTIONS.reduce((best, option) => (Math.abs(option.minutes - minutes) < Math.abs(best.minutes - minutes) ? option : best));
  return Math.abs(nearest.minutes - minutes) <= Math.max(1, nearest.minutes * 0.02) ? nearest.value : '';
};

interface Props {
  showRange?: boolean;
}

const BriefingFilters = ({ showRange = true }: Props) => {
  const router = useRouter();
  const { startMs, endMs } = useBriefingWindow();
  const [accounts, setAccounts] = useState<any[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    apiHome
      .getCloudAccounts()
      .then((response: any) => {
        if (!cancelled) setAccounts(Array.isArray(response) ? response : []);
      })
      .catch(() => {
        if (!cancelled) setAccounts([]);
      })
      .finally(() => {
        if (!cancelled) setAccountsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const accountOptions = useMemo(
    () =>
      accounts.map((account) => ({
        label: account.label || account.account_name,
        value: account.id || account.value,
        group: account.cloud_provider || 'Other',
      })),
    [accounts]
  );

  const selectedAccountIds = useMemo(() => {
    const raw = router.query.accountIds;
    return raw ? String(raw).split(',').filter(Boolean) : [];
  }, [router.query.accountIds]);

  const selectedAccounts = useMemo(
    () => accountOptions.filter((option) => selectedAccountIds.includes(option.value)),
    [accountOptions, selectedAccountIds]
  );

  const activeRange = useMemo(() => matchRange(startMs, endMs) || DEFAULT_RANGE, [startMs, endMs]);

  const onAccountsChange = (event: any) => {
    const selected = event?.target?.value;
    const ids = (Array.isArray(selected) ? selected : selected ? [selected] : []).map((option: any) => option?.value ?? option).filter(Boolean);
    applyFiltersOnRouter(router, { accountIds: ids.length ? ids.join(',') : undefined });
  };

  const onRangeChange = (event: any) => {
    const minutes = RANGE_OPTIONS.find((range) => range.value === event?.target?.value)?.minutes;
    if (!minutes) return;
    const end = Date.now();
    applyFiltersOnRouter(router, { start_time: String(end - minutes * 60000), end_time: String(end) });
  };

  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      <FilterDropdown
        id='briefing-filter-account'
        label='Account'
        placeholder='All accounts'
        multiple
        grouped
        selectionWithinGroup
        size='sm'
        options={accountOptions}
        value={selectedAccounts}
        isOptionsLoading={accountsLoading}
        onSelect={onAccountsChange}
      />
      {showRange && (
        <FilterDropdown
          id='briefing-filter-range'
          label='Range'
          size='sm'
          clearable={false}
          options={RANGE_OPTIONS.map(({ label, value }) => ({ label, value }))}
          value={activeRange}
          onSelect={onRangeChange}
        />
      )}
    </Box>
  );
};

export default BriefingFilters;
