import { useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import { useRouter } from 'next/router';
import dayjs from 'dayjs';
import FilterDropdown from '@ui/FilterDropdown';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import apiHome from '@api1/home';
import { applyFiltersOnRouter } from '@lib/router';
import { useBriefingWindow } from './useBriefingData';

interface Props {
  showRange?: boolean;
}

const BriefingFilters = ({ showRange = true }: Props) => {
  const router = useRouter();
  const { startMs, endMs } = useBriefingWindow();
  const [accounts, setAccounts] = useState<any[]>([]);
  const [accountsLoading, setAccountsLoading] = useState(true);
  // Duration behind the current window when it came from a shortcut click, so
  // the trigger reads "Last 24 Hours" instead of "Aug 17 - Aug 18". Reset below
  // whenever the window is changed by something other than this picker.
  const [shortcutClickTime, setShortcutClickTime] = useState(0);

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

  // A briefing drill-down pins its own window onto the URL; once the window no
  // longer matches the shortcut that produced it, it is a plain absolute range.
  useEffect(() => {
    if (shortcutClickTime && endMs - startMs !== shortcutClickTime) setShortcutClickTime(0);
  }, [startMs, endMs, shortcutClickTime]);

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

  const onAccountsChange = (event: any) => {
    const selected = event?.target?.value;
    const ids = (Array.isArray(selected) ? selected : selected ? [selected] : []).map((option: any) => option?.value ?? option).filter(Boolean);
    applyFiltersOnRouter(router, { accountIds: ids.length ? ids.join(',') : undefined });
  };

  const onRangeChange = ({ selection }: { selection: { startTime: number; endTime: number; shortcutClickTime?: number } }) => {
    if (!Number.isFinite(selection?.startTime) || !Number.isFinite(selection?.endTime)) return;
    setShortcutClickTime(selection.shortcutClickTime ?? 0);
    applyFiltersOnRouter(router, { start_time: String(selection.startTime), end_time: String(selection.endTime) });
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
        <CustomDateTimeRangePicker
          passedSelectedDateTime={{ startTime: startMs, endTime: endMs, shortcutClickTime }}
          minDate={dayjs().subtract(6, 'month')}
          onChange={onRangeChange}
        />
      )}
    </Box>
  );
};

export default BriefingFilters;
