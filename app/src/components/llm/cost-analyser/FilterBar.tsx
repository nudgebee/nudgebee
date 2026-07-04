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
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import ExpandLessIcon from '@mui/icons-material/ExpandLess';
import CloseIcon from '@mui/icons-material/Close';
import RestartAltOutlinedIcon from '@mui/icons-material/RestartAltOutlined';
import FilterDropdown from '@ui/FilterDropdown';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Input } from '@ui/Input';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
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
  /** "Today" anchor for the date presets (real today for live; fixture date for demo). */
  anchorDate?: string;
}

const DAY = 86_400_000;
const FIXTURE_ANCHOR = '2026-06-01'; // demo fixtures' "today"
const TRIGGER_OPTIONS = (['user_chat', 'user_manual', 'auto_event', 'auto_schedule'] as const).map((v) => ({ label: triggerLabel[v], value: v }));

type FDOption = string | { value?: string };
const toValues = (sel: FDOption[] | undefined): string[] => (sel ?? []).map((o) => (typeof o === 'string' ? o : String(o?.value ?? '')));

// Date presets (modern toolbar pattern). Today is the default. 'Custom' is the
// label shown when the start/end pickers hold an arbitrary range.
const RANGE_PRESETS = ['Today', 'Yesterday', 'Last 7 days', 'Last 14 days', 'Last 30 days', 'Last 90 days', 'Custom'];

// Time-bucket granularity for the over-time charts (drives `time_series`).
const GRANULARITY_OPTIONS: { value: Granularity; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
];

function presetToRange(label: string, anchorEnd: string): { startDate: string; endDate: string } {
  const anchor = Date.parse(anchorEnd);
  const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);
  if (label === 'Today') return { startDate: anchorEnd, endDate: anchorEnd };
  if (label === 'Yesterday') return { startDate: iso(anchor - DAY), endDate: iso(anchor - DAY) };
  const days = Number(label.replace(/\D/g, '')) || 7;
  return { startDate: iso(anchor - (days - 1) * DAY), endDate: anchorEnd };
}

function rangeLabelFromFilters(f: CostFilters, anchorEnd: string): string {
  const anchor = Date.parse(anchorEnd);
  const startMs = Date.parse(f.startDate);
  const endMs = Date.parse(f.endDate);
  if (startMs === anchor && endMs === anchor) return 'Today';
  if (startMs === anchor - DAY && endMs === anchor - DAY) return 'Yesterday';
  // A range counts as a preset only if it ends today and spans a preset length;
  // anything else (incl. an end date in the past) is Custom — the pickers own it.
  if (endMs === anchor) {
    const span = Math.round((endMs - startMs) / DAY) + 1;
    const preset = RANGE_PRESETS.find((p) => p === `Last ${span} days`);
    if (preset) return preset;
  }
  return 'Custom';
}

/** Native date field, styled to sit alongside the DS dropdowns in the filter row. */
function DateField({ id, value, min, max, onChange }: { id: string; value: string; min?: string; max?: string; onChange: (d: string) => void }) {
  return (
    <Box
      component='input'
      type='date'
      id={id}
      value={value}
      min={min}
      max={max}
      onChange={(e: React.ChangeEvent<HTMLInputElement>) => {
        if (e.target.value) onChange(e.target.value);
      }}
      sx={{
        fontSize: 'var(--ds-text-small)',
        color: 'var(--ds-gray-700)',
        fontFamily: 'inherit',
        border: '1px solid var(--ds-gray-200)',
        borderRadius: 'var(--ds-radius-md)',
        backgroundColor: 'var(--ds-background-100)',
        padding: '4px 8px',
        height: 30,
        cursor: 'pointer',
        colorScheme: 'light',
        '&:focus': { outline: 'none', borderColor: 'var(--ds-blue-400, #5b9bd5)' },
        '&::-webkit-calendar-picker-indicator': { cursor: 'pointer', opacity: 0.6 },
      }}
    />
  );
}

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

export function FilterBar({ filters, onChange, onReset, options, accountId = '', onAccountChange, anchorDate }: FilterBarProps) {
  const [open, setOpen] = React.useState(false);
  const anchorEnd = anchorDate || FIXTURE_ANCHOR;

  const accountOptions = (options?.accounts ?? []).map((a) => ({ label: a.name, value: a.id }));
  // Before the first successful ai_get_usage_filters fetch, fall back to an EMPTY
  // list (reads as "loading/none") rather than mock fixtures — never surface
  // fictitious model/provider names as selectable values in a live view.
  const modelOptions = options?.models ?? [];
  const providerOptions = options?.providers ?? [];
  const sourceOptions = options?.sources ?? [];
  const statusOptions = options?.statuses ?? ['success', 'failure'];

  const advancedCount =
    filters.statuses.length +
    filters.triggerTypes.length +
    filters.assistants.length +
    filters.templates.length +
    (filters.minCost != null ? 1 : 0) +
    (filters.maxCost != null ? 1 : 0);

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
          options={accountOptions}
          value={accountId}
          onSelect={(e: { target: { value: string | null } }) => onAccountChange?.(e?.target?.value ?? '')}
        />
        <FilterDropdown
          id='cost-filter-range'
          label='Date range'
          options={RANGE_PRESETS}
          value={rangeLabelFromFilters(filters, anchorEnd)}
          onSelect={(e: { target: { value: string } }) => {
            // 'Custom' is owned by the date pickers — selecting it keeps the current range.
            if (e?.target?.value === 'Custom') return;
            onChange(presetToRange(e?.target?.value, anchorEnd));
          }}
        />
        {/* Explicit start/end pickers — pick any range; end can't precede start
            and can't run past the anchor (today). Editing these flips the preset
            label to 'Custom'. */}
        <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
          <DateField id='cost-filter-start' value={filters.startDate} max={filters.endDate} onChange={(d) => onChange({ startDate: d })} />
          <Box component='span' sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)' }}>
            →
          </Box>
          <DateField
            id='cost-filter-end'
            value={filters.endDate}
            min={filters.startDate}
            max={anchorEnd}
            onChange={(d) => onChange({ endDate: d })}
          />
        </Box>
        <ToggleGroup
          selection='single'
          size='sm'
          ariaLabel='Chart granularity'
          value={filters.granularity}
          onChange={(g) => onChange({ granularity: g as Granularity })}
          options={GRANULARITY_OPTIONS}
        />
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
        <FilterDropdown
          id='cost-filter-source'
          label='Source'
          multiple
          options={sourceOptions}
          value={filters.sources}
          onSelect={(_e: unknown, sel: FDOption[]) => onChange({ sources: toValues(sel) })}
        />

        <Box sx={{ flex: 1 }} />

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
