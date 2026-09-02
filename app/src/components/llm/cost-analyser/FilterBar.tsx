/**
 * FilterBar — compact report filter with progressive disclosure (spec §10).
 *
 * Basic row carries the **API-backed** filters (date · source · model · provider);
 * option-sets come live from `ai_get_usage_filters` (passed in as `options`) and
 * fall back to mock lists before the first fetch. "More filters" reveals status
 * (also backed) plus the not-yet-backed dimensions (trigger / assistant / template),
 * which are tagged "sample" because they only scope the illustrative widgets.
 */
import * as React from 'react';
import { Box, Collapse } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import RestartAltOutlinedIcon from '@mui/icons-material/RestartAltOutlined';
import dayjs from 'dayjs';
import FilterDropdown from '@ui/FilterDropdown';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Input } from '@ui/Input';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { ALL_ASSISTANTS, ALL_TEMPLATES } from './mockData';
import { triggerLabel } from './format';
import type { UsageFilters } from '@api1/ai-cost';
import type { CostFilters, Granularity, ModelMatchMode } from './types';

interface FilterBarProps {
  filters: CostFilters;
  onChange: (patch: Partial<CostFilters>) => void;
  onReset: () => void;
  /** Live filter-bar option-sets from the backend (null before first load). */
  options?: UsageFilters | null;
  /** Selected account id ('' = all accounts the tenant can read). */
  accountId?: string;
  /** Change the account scope. */
  onAccountChange?: (accountId: string) => void;
  /**
   * Bumped when the date range is set programmatically (e.g. a cost-over-time bar
   * click). Keys the date picker so it re-seeds its trigger label from the new
   * range — the picker only reads props on mount, so without this it would keep
   * showing a stale shortcut label ("Current Week") after an external change.
   */
  dateResetNonce?: number;
  /** Show the shared Agent filter (hidden on the Agents tab, which has its own). */
  showAgents?: boolean;
  /** Show the User filter (hidden on the Users tab, which IS the per-user breakdown). */
  showUser?: boolean;
  /** Show the Day/Week/Month toggle (hidden on tabs with no over-time chart to drive it). */
  showGranularity?: boolean;
}

const TRIGGER_OPTIONS = (['user_chat', 'user_manual', 'auto_event', 'auto_schedule'] as const).map((v) => ({ label: triggerLabel[v], value: v }));

type FDOption = string | { value?: string };
const toValues = (sel: FDOption[] | undefined): string[] => (sel ?? []).map((o) => (typeof o === 'string' ? o : String(o?.value ?? '')));

// Shortcuts surfaced in the date-range picker — limited to the day/week/month
// windows that suit a daily cost view (the picker computes these natively).
const COST_RANGE_SHORTCUTS = ['Last 24 Hours', 'Current Week', 'Current Month', 'Last Month'];

// Time-bucket granularity for the over-time charts (drives `time_series`).
const GRANULARITY_OPTIONS: { value: Granularity; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
];

/** Label + control cell so each advanced field lines up in the grid. */
function Field({ label, sample, children }: { label: string; sample?: boolean; children: React.ReactNode }) {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px', minWidth: 0 }}>
      <Box
        component='span'
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: '6px',
          fontSize: 'var(--ds-text-small)',
          color: 'var(--ds-gray-600)',
          fontFamily: 'var(--ds-font-display)',
        }}
      >
        {label}
        {sample && (
          <Chip size='2xs' variant='tag' tone='warning'>
            sample
          </Chip>
        )}
      </Box>
      {children}
    </Box>
  );
}

export function FilterBar({
  filters,
  onChange,
  onReset,
  options,
  accountId = '',
  onAccountChange,
  dateResetNonce = 0,
  showAgents = true,
  showUser = true,
  showGranularity = true,
}: FilterBarProps) {
  const [open, setOpen] = React.useState(false);

  // Bridge the CostFilters 'YYYY-MM-DD' strings to the picker's epoch-ms model
  // (start-of-day → end-of-day) and back. shortcutClickTime stays 0 — the active
  // shortcut highlight is the picker's own transient state, not persisted here.
  //
  // Clamp end-of-day to "now": when endDate is today, endOf('day') is 23:59 —
  // a future instant that exceeds the picker's maxDateTime (now), rendering the
  // "To" field in an error (red) state. The applied value is unaffected —
  // handleDateRangeChange re-formats to date-granular 'YYYY-MM-DD'.
  const dateTimeValue = React.useMemo(
    () => ({
      startTime: dayjs(filters.startDate).startOf('day').valueOf(),
      endTime: Math.min(dayjs(filters.endDate).endOf('day').valueOf(), dayjs().valueOf()),
      shortcutClickTime: 0,
    }),
    [filters.startDate, filters.endDate]
  );

  const handleDateRangeChange = ({ selection }: { selection: { startTime: number; endTime: number; shortcutClickTime?: number } }) => {
    if (!selection) return;
    onChange({
      startDate: dayjs(selection.startTime).format('YYYY-MM-DD'),
      endDate: dayjs(selection.endTime).format('YYYY-MM-DD'),
    });
  };

  // Accounts group by cloud provider (with its logo on the header and on each
  // row) — same shape every other account picker in the app uses. The backend
  // only returns accounts that have usage in the selected window, so this list
  // narrows with the date range like the model/provider/source lists do.
  const accountOptions = (options?.accounts ?? []).map((a) => {
    // 'Other' (never undefined) so an account whose provider the backend didn't
    // resolve gets the generic cloud glyph — CloudProviderIcon falls back to the
    // AWS logo on a null provider, which would mislabel it.
    const provider = a.cloud_provider || 'Other';
    return { label: a.name, value: a.id, group: provider, icon: <CloudProviderIcon cloud_provider={provider} width='16px' height='16px' /> };
  });
  // Before the first successful ai_get_usage_filters fetch, fall back to an EMPTY
  // list (reads as "loading/none") rather than mock fixtures — never surface
  // fictitious model/provider names as selectable values in a live view.
  const modelOptions = options?.models ?? [];
  const providerOptions = options?.providers ?? [];
  const agentOptions = options?.agents ?? [];
  const sourceOptions = options?.sources ?? [];
  const statusOptions = options?.statuses ?? ['success', 'failure'];
  // User scope is single-select: pill shows the chosen user's name, clear-X resets
  // to "all users". Keyed on user_id (the value the API filters on).
  const userOptions = (options?.users ?? []).map((u) => ({ label: u.name, value: u.id }));

  return (
    <Box
      id='cost-filter-bar'
      sx={{
        padding: 'var(--ds-space-3) var(--ds-space-4)',
        backgroundColor: 'var(--ds-background-100)',
        border: '1px solid var(--ds-gray-200)',
        borderRadius: 'var(--ds-radius-lg)',
        boxShadow: '0 1px 2px rgba(0,0,0,0.03)',
      }}
    >
      {/* ── Basic row: account · date range · model · provider · source (all API-backed) ── */}
      <Box sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
        <FilterDropdown
          id='cost-filter-account'
          label='Account'
          grouped
          groupIcon={(groupKey: string) => <CloudProviderIcon cloud_provider={groupKey} width='14px' height='14px' />}
          searchPlaceholder='Search accounts'
          options={accountOptions}
          value={accountId}
          onSelect={(e: { target: { value: string | null } }) => onAccountChange?.(e?.target?.value ?? '')}
        />
        {/* App-standard range picker (absolute From/To + day/week/month shortcuts).
            Speaks epoch-ms; bridged to the CostFilters date strings above. */}

        <FilterDropdown
          id='cost-filter-model'
          label='Model'
          multiple
          options={modelOptions}
          value={filters.models}
          onSelect={(_e: unknown, sel: FDOption[]) => onChange({ models: toValues(sel) })}
        />
        <FilterDropdown
          id='cost-filter-provider'
          label='Provider'
          multiple
          options={providerOptions}
          value={filters.providers}
          onSelect={(_e: unknown, sel: FDOption[]) => onChange({ providers: toValues(sel) })}
        />
        {showAgents && (
          <FilterDropdown
            id='cost-filter-agent'
            label='Agent'
            multiple
            options={agentOptions}
            value={filters.agents}
            onSelect={(_e: unknown, sel: FDOption[]) => onChange({ agents: toValues(sel) })}
          />
        )}
        <FilterDropdown
          id='cost-filter-source'
          label='Source'
          multiple
          options={sourceOptions}
          value={filters.sources}
          onSelect={(_e: unknown, sel: FDOption[]) => onChange({ sources: toValues(sel) })}
        />
        {showUser && (
          <FilterDropdown
            id='cost-filter-user'
            label='User'
            options={userOptions}
            value={filters.userId}
            onSelect={(e: { target: { value: string | null } }) => onChange({ userId: e?.target?.value ?? '' })}
          />
        )}

        <Box sx={{ flex: 1 }} />
        {showGranularity && (
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Chart granularity'
            value={filters.granularity}
            onChange={(g) => onChange({ granularity: g as Granularity })}
            options={GRANULARITY_OPTIONS}
          />
        )}
        <CustomDateTimeRangePicker
          key={`cost-date-picker-${dateResetNonce}`}
          passedSelectedDateTime={dateTimeValue}
          onChange={handleDateRangeChange}
          minDate={dayjs().subtract(1, 'year')}
          shortCuts={COST_RANGE_SHORTCUTS}
        />
        {/*
        Commented as of now. will be added back once the advanced filters are implemented and we want to hide them under a toggle.
        <Button
          tone='secondary'
          size='sm'
          icon={open ? <ExpandLessIcon sx={{ fontSize: 16 }} /> : <ExpandMoreIcon sx={{ fontSize: 16 }} />}
          onClick={() => setOpen((o) => !o)}
          id='cost-filter-more'
        >
          {open ? 'Collapse filters' : 'More filters'}
          {!open && advancedCount > 0 && <Chip size='2xs' tone='info' variant='count' count={advancedCount} sx={{ ml: 'var(--ds-space-1)' }} />}
        </Button>
        */}
        <Button tone='ghost' size='sm' icon={<RestartAltOutlinedIcon sx={{ fontSize: 16 }} />} onClick={onReset} id='cost-filter-reset'>
          Reset
        </Button>
      </Box>

      {/* ── Advanced grid: status (backed) + sample-only dimensions ──────────── */}
      <Collapse in={open} unmountOnExit>
        <Box sx={{ mt: 'var(--ds-space-4)', pt: 'var(--ds-space-4)', borderTop: '1px solid var(--ds-gray-200)' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 'var(--ds-space-3)' }}>
            <Box
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-semibold)',
                color: 'var(--ds-gray-600)',
                fontFamily: 'var(--ds-font-display)',
              }}
            >
              Advanced filters
            </Box>
            <Button tone='ghost' size='xs' icon={<CloseIcon sx={{ fontSize: 14 }} />} onClick={() => setOpen(false)} id='cost-filter-collapse'>
              Collapse
            </Button>
          </Box>
          <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: 'var(--ds-space-4)', alignItems: 'flex-end' }}>
            <Field label='Status'>
              <FilterDropdown
                id='cost-filter-status'
                label='Status'
                multiple
                options={statusOptions}
                value={filters.statuses}
                onSelect={(_e: unknown, sel: FDOption[]) => onChange({ statuses: toValues(sel) })}
              />
            </Field>
            <Field label='Trigger' sample>
              <FilterDropdown
                id='cost-filter-trigger'
                label='Trigger'
                multiple
                options={TRIGGER_OPTIONS}
                value={filters.triggerTypes}
                onSelect={(_e: unknown, sel: FDOption[]) => onChange({ triggerTypes: toValues(sel) as CostFilters['triggerTypes'] })}
              />
            </Field>
            <Field label='Assistant' sample>
              <FilterDropdown
                id='cost-filter-assistant'
                label='Assistant'
                multiple
                options={ALL_ASSISTANTS}
                value={filters.assistants}
                onSelect={(_e: unknown, sel: FDOption[]) => onChange({ assistants: toValues(sel) as CostFilters['assistants'] })}
              />
            </Field>
            <Field label='Template' sample>
              <FilterDropdown
                id='cost-filter-template'
                label='Template'
                multiple
                options={ALL_TEMPLATES}
                value={filters.templates}
                onSelect={(_e: unknown, sel: FDOption[]) => onChange({ templates: toValues(sel) })}
              />
            </Field>

            <Field label='Cost range ($)'>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
                <Input
                  size='sm'
                  type='number'
                  placeholder='min'
                  value={filters.minCost == null ? '' : String(filters.minCost)}
                  onChange={(v) => onChange({ minCost: v === '' ? null : Number(v) })}
                />
                <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
                  –
                </Box>
                <Input
                  size='sm'
                  type='number'
                  placeholder='max'
                  value={filters.maxCost == null ? '' : String(filters.maxCost)}
                  onChange={(v) => onChange({ maxCost: v === '' ? null : Number(v) })}
                />
              </Box>
            </Field>
            <Field label='Model match (when ≥1 model)'>
              <ToggleGroup
                selection='single'
                size='sm'
                ariaLabel='Model match mode'
                value={filters.modelMatchMode}
                onChange={(m) => onChange({ modelMatchMode: m as ModelMatchMode })}
                options={[
                  { value: 'any', label: 'Any of' },
                  { value: 'all', label: 'All of' },
                ]}
              />
            </Field>
          </Box>
        </Box>
      </Collapse>
    </Box>
  );
}

export default FilterBar;
