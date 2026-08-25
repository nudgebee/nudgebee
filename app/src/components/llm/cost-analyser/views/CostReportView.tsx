/**
 * Screen — Cost Report (consolidated per-account AI cost report).
 *
 * One row per account: daily cost, month-to-date cost, previous-month cost,
 * average daily spend this month vs last month, the % change between those
 * two averages, and the top-5 cost drivers by model and by source for both
 * the daily and month-to-date windows.
 *
 * Backed by `ai_aggregate_account_cost_report` — the same computation the
 * Slack daily digest uses, so the numbers here always match what's posted
 * to Slack. referenceDate ("as of" day) is owned by CostAnalyser separately
 * from the shared filter bar, via the date picker below.
 */
import * as React from 'react';
import dayjs from 'dayjs';
import { Box } from '@mui/material';
import CustomTable2 from '@shared/tables/CustomTable';
import CustomDateTimePicker from '@shared/widgets/CustomDateTimePicker';
import FilterDropdown from '@ui/FilterDropdown';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { CostCallout } from '@ui/CostCallout';
import { EmptyState } from '@ui/EmptyState';
import HeaderLabel from '../components/HeaderLabel';
import SectionHeader from '../components/Section';
import { fmtCost, fmtPct } from '../format';
import { makeSeverity, SeverityCell, type Severity } from '../components/severity';
import {
  aggregateAccountCostReport,
  type AiCostAccountRow,
  type AiCostDriver,
  type AiCostTopModelRow,
  type AiCostTopSourceRow,
  type UsageFilterOption,
} from '@api1/ai-cost';

interface CostReportViewProps {
  /** Selected account id ('' = every account the tenant can read). */
  accountId?: string;
  /** Change the account scope — the shared FilterBar is hidden on this tab, so this is its only account picker. */
  onAccountChange: (accountId: string) => void;
  accountOptions: UsageFilterOption[];
  /** "As of" date for the report — owned by CostAnalyser independently of the shared filters. */
  referenceDate: string;
  onReferenceDateChange: (date: string) => void;
}

const numCell = { fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums' } as const;

const H = {
  account: <HeaderLabel label='Account' info='The account the cost was incurred on.' />,
  daily: <HeaderLabel label='Daily' info='Total AI cost for the date selected in the picker above.' />,
  mtd: <HeaderLabel label='MTD' info='Total AI cost from the 1st of the month through the selected date.' />,
  prevMonth: <HeaderLabel label='Prev month' info='Total AI cost for the entire previous calendar month.' />,
  avgThisMonth: <HeaderLabel label='Avg/day (this mo)' info='Month-to-date cost divided by days elapsed this month.' />,
  avgPrevMonth: <HeaderLabel label='Avg/day (prev mo)' info='Previous month’s total cost divided by days in that month.' />,
  delta: <HeaderLabel label='%Δ' info='Change in average daily spend, this month vs previous month.' />,
  dailyDrivers: <HeaderLabel label='Top daily drivers' info='Top 5 cost contributors on the selected date, by model and by source.' />,
  mtdDrivers: (
    <HeaderLabel label='Top MTD drivers' info='Top 5 cost contributors month-to-date (through the selected date), by model and by source.' />
  ),
};

const HEADERS = [
  { name: 'Account', width: '13%', sortEnabled: true, component: H.account },
  { name: 'Daily', width: '8%', sortEnabled: true, component: H.daily },
  { name: 'MTD', width: '8%', sortEnabled: true, component: H.mtd },
  { name: 'Prev month', width: '9%', sortEnabled: true, component: H.prevMonth },
  { name: 'Avg/day (this mo)', width: '10%', sortEnabled: true, component: H.avgThisMonth },
  { name: 'Avg/day (prev mo)', width: '10%', sortEnabled: true, component: H.avgPrevMonth },
  { name: '%Δ', width: '6%', sortEnabled: true, component: H.delta },
  { name: 'Top daily drivers', width: '18%', component: H.dailyDrivers },
  { name: 'Top MTD drivers', width: '18%', component: H.mtdDrivers },
];

const SORT_VALUE: Record<string, (r: AiCostAccountRow) => number | string> = {
  Account: (r) => r.account_name,
  Daily: (r) => r.daily_cost_usd,
  MTD: (r) => r.mtd_cost_usd,
  'Prev month': (r) => r.prev_month_cost_usd,
  'Avg/day (this mo)': (r) => r.avg_daily_this_month_usd,
  'Avg/day (prev mo)': (r) => r.avg_daily_prev_month_usd,
  '%Δ': (r) => r.pct_delta_avg_daily,
};

// Each driver renders as a small tag chip (name + cost) instead of a
// comma-separated run-on line — matches ConversationsTable's ModelBadges
// pattern, and wraps far more legibly than plain text does at this width.
function DriverChips({ label, drivers }: { label: string; drivers: AiCostDriver[] }) {
  if (!drivers.length) return null;
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
      <Box component='span' sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-medium)' }}>
        {label}
      </Box>
      <Box sx={{ display: 'flex', gap: '4px', flexWrap: 'wrap' }}>
        {drivers.map((d) => (
          <Chip key={d.key} size='2xs' variant='tag' tone='subtle'>
            {d.key}
            <Box component='span' sx={{ ml: '3px', color: 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-regular)' }}>
              {fmtCost(d.cost_usd)}
            </Box>
          </Chip>
        ))}
      </Box>
    </Box>
  );
}

function DriverList({ model, source }: { model: AiCostDriver[]; source: AiCostDriver[] }) {
  if (!model.length && !source.length) {
    return (
      <Box component='span' sx={{ color: 'var(--ds-gray-400)' }}>
        —
      </Box>
    );
  }
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
      <DriverChips label='Model' drivers={model} />
      <DriverChips label='Source' drivers={source} />
    </Box>
  );
}

function toRow(r: AiCostAccountRow, costSev: (v: number) => Severity) {
  return [
    {
      data: r.account_name,
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>{r.account_name}</Box>
      ),
    },
    {
      data: r.daily_cost_usd,
      component: (
        <SeverityCell severity={costSev(r.daily_cost_usd)} metric='cost'>
          <CostCallout value={r.daily_cost_usd} size='sm' tone='neutral' fractionDigits={2} />
        </SeverityCell>
      ),
    },
    { data: r.mtd_cost_usd, component: <Box sx={numCell}>{fmtCost(r.mtd_cost_usd)}</Box> },
    { data: r.prev_month_cost_usd, component: <Box sx={numCell}>{fmtCost(r.prev_month_cost_usd)}</Box> },
    { data: r.avg_daily_this_month_usd, component: <Box sx={numCell}>{fmtCost(r.avg_daily_this_month_usd)}</Box> },
    { data: r.avg_daily_prev_month_usd, component: <Box sx={numCell}>{fmtCost(r.avg_daily_prev_month_usd)}</Box> },
    {
      data: r.pct_delta_avg_daily,
      component: (
        <Box
          sx={{
            ...numCell,
            color: r.pct_delta_avg_daily === 0 ? 'var(--ds-gray-500)' : r.pct_delta_avg_daily > 0 ? 'var(--ds-red-600)' : 'var(--ds-green-600)',
          }}
        >
          {r.pct_delta_avg_daily > 0 ? '+' : ''}
          {fmtPct(r.pct_delta_avg_daily / 100, 0)}
        </Box>
      ),
    },
    { data: r.account_id, component: <DriverList model={r.top_daily_by_model} source={r.top_daily_by_source} /> },
    { data: r.account_id, component: <DriverList model={r.top_mtd_by_model} source={r.top_mtd_by_source} /> },
  ];
}

// Tenant-wide by default, or scoped to one account when accountId is set
// (see topModelsTitle below) — already sorted by MTD cost desc and capped at
// 10 by the backend query, so this table has no interactive sort of its own.
const TOP_MODELS_HEADERS = [
  { name: 'Model', width: '30%' },
  { name: 'MTD cost', width: '17.5%' },
  { name: '# of calls', width: '17.5%' },
  {
    name: 'p95 cost/call',
    width: '17.5%',
    component: <HeaderLabel label='p95 cost/call' info='95th percentile cost across this model’s individual calls this month.' />,
  },
  {
    name: 'p99 cost/call',
    width: '17.5%',
    component: <HeaderLabel label='p99 cost/call' info='99th percentile cost across this model’s individual calls this month.' />,
  },
];

function toTopModelRow(m: AiCostTopModelRow) {
  return [
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>{m.model}</Box>
      ),
    },
    { component: <Box sx={numCell}>{fmtCost(m.mtd_cost_usd)}</Box> },
    { component: <Box sx={numCell}>{m.call_count}</Box> },
    { component: <Box sx={numCell}>{fmtCost(m.p95_cost_per_call_usd)}</Box> },
    { component: <Box sx={numCell}>{fmtCost(m.p99_cost_per_call_usd)}</Box> },
  ];
}

// Tenant-wide by default, or scoped to one account when accountId is set
// (see topSourcesTitle below) — already sorted by MTD cost desc and capped
// at 5 by the backend query, so this table has no interactive sort of its own.
const TOP_SOURCES_HEADERS = [
  { name: 'Source', width: '30%' },
  { name: 'MTD cost', width: '17.5%' },
  { name: '# of calls', width: '17.5%' },
  {
    name: 'p95 cost/call',
    width: '17.5%',
    component: <HeaderLabel label='p95 cost/call' info='95th percentile cost across this source’s individual calls this month.' />,
  },
  {
    name: 'p99 cost/call',
    width: '17.5%',
    component: <HeaderLabel label='p99 cost/call' info='99th percentile cost across this source’s individual calls this month.' />,
  },
];

function toTopSourceRow(s: AiCostTopSourceRow) {
  return [
    {
      component: (
        <Box sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>{s.source}</Box>
      ),
    },
    { component: <Box sx={numCell}>{fmtCost(s.mtd_cost_usd)}</Box> },
    { component: <Box sx={numCell}>{s.call_count}</Box> },
    { component: <Box sx={numCell}>{fmtCost(s.p95_cost_per_call_usd)}</Box> },
    { component: <Box sx={numCell}>{fmtCost(s.p99_cost_per_call_usd)}</Box> },
  ];
}

export function CostReportView({ accountId, onAccountChange, accountOptions, referenceDate, onReferenceDateChange }: CostReportViewProps) {
  const [sort, setSort] = React.useState<{ name: string; order: 'asc' | 'desc' }>({ name: '', order: 'asc' });
  const [state, setState] = React.useState<{
    loading: boolean;
    error: string | null;
    rows: AiCostAccountRow[];
    topModels: AiCostTopModelRow[];
    topSources: AiCostTopSourceRow[];
  }>({
    loading: false,
    error: null,
    rows: [],
    topModels: [],
    topSources: [],
  });

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setState((s) => ({ ...s, loading: true, error: null }));
    aggregateAccountCostReport({ accountIds: accountId ? [accountId] : [], referenceDate }, controller.signal)
      .then((r) => {
        if (!cancelled)
          setState({
            loading: false,
            error: null,
            rows: r?.accounts ?? [],
            topModels: r?.top_models ?? [],
            topSources: r?.top_sources ?? [],
          });
      })
      .catch((e) => {
        if (!cancelled)
          setState({
            loading: false,
            error: e instanceof Error ? e.message : 'Failed to load accounts',
            rows: [],
            topModels: [],
            topSources: [],
          });
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [accountId, referenceDate]);

  const sortedRows = React.useMemo(() => {
    const get = sort.name ? SORT_VALUE[sort.name] : undefined;
    if (!get) return state.rows;
    const arr = [...state.rows].sort((a, b) => {
      const av = get(a);
      const bv = get(b);
      const comparison = typeof av === 'string' ? String(av).localeCompare(String(bv)) : (av as number) - (bv as number);
      return sort.order === 'desc' ? -comparison : comparison;
    });
    return arr;
  }, [state.rows, sort]);

  const costSev = React.useMemo(() => makeSeverity(sortedRows.map((r) => r.daily_cost_usd)), [sortedRows]);
  const tableData = React.useMemo(() => sortedRows.map((r) => toRow(r, costSev)), [sortedRows, costSev]);
  const topModelsTableData = React.useMemo(() => state.topModels.map(toTopModelRow), [state.topModels]);
  const topSourcesTableData = React.useMemo(() => state.topSources.map(toTopSourceRow), [state.topSources]);
  const showEmpty = !state.loading && !state.error && state.rows.length === 0;

  // The backend scopes top_models/top_sources to whatever accountIds the
  // request carried (HandleAiCostAccountReportApi -> resolveAccessibleAccounts):
  // tenant-wide when accountId is unset, but narrowed to just that one account
  // when it's selected in the picker above — the label must say which, or
  // "tenant-wide" would misdescribe a single-account result.
  const scopedAccountName = accountId ? state.rows.find((r) => r.account_id === accountId)?.account_name : undefined;
  const topModelsTitle = accountId ? `Top models — ${scopedAccountName ?? 'selected account'} (MTD)` : 'Top models (tenant-wide MTD)';
  const topModelsSubtitle = accountId
    ? 'Top 10 models by month-to-date cost for this account'
    : 'Top 10 models by month-to-date cost across every account';
  const topSourcesTitle = accountId ? `Top sources — ${scopedAccountName ?? 'selected account'} (MTD)` : 'Top sources (tenant-wide MTD)';
  const topSourcesSubtitle = accountId
    ? 'Top 5 sources by month-to-date cost for this account'
    : 'Top 5 sources by month-to-date cost across every account';

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-5)' }}>
      <Card>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)', flexWrap: 'wrap' }}>
            <Box sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>
              accounts with zero cost on the selected date, month-to-date, and last month are omitted
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <FilterDropdown
                id='accounts-account-filter'
                label='Account'
                options={accountOptions.map((a) => ({ label: a.name, value: a.id }))}
                value={accountId ?? ''}
                onSelect={(e: { target: { value: string | null } }) => onAccountChange(e?.target?.value ?? '')}
                clearable
                size='sm'
              />
              <CustomDateTimePicker
                id='accounts-as-of-date'
                value={dayjs(referenceDate)}
                onChange={(d) => d && onReferenceDateChange(d.format('YYYY-MM-DD'))}
                views={['day']}
                format='MM/DD/YYYY'
                maxDateTime={dayjs()}
                size='sm'
                width='160px'
              />
            </Box>
          </Box>

          {state.error && <Banner tone='critical' title='Could not load account cost report' message={state.error} />}

          {showEmpty ? (
            <EmptyState size='section' illustration='no-results' title='No AI cost activity' description='No account had AI spend as of this date.' />
          ) : (
            !state.error && (
              <CustomTable2
                id='accounts-cost-report'
                headers={HEADERS}
                tableData={tableData}
                loading={state.loading}
                sort={sort}
                onSortChange={(s: { name: string; order: 'asc' | 'desc' }) => setSort(s)}
              />
            )
          )}
        </Box>
      </Card>

      {!state.error && state.topModels.length > 0 && (
        <Card>
          <SectionHeader title={topModelsTitle} subtitle={topModelsSubtitle} />
          <CustomTable2 id='top-models-cost-report' headers={TOP_MODELS_HEADERS} tableData={topModelsTableData} loading={state.loading} />
        </Card>
      )}

      {!state.error && state.topSources.length > 0 && (
        <Card>
          <SectionHeader title={topSourcesTitle} subtitle={topSourcesSubtitle} />
          <CustomTable2 id='top-sources-cost-report' headers={TOP_SOURCES_HEADERS} tableData={topSourcesTableData} loading={state.loading} />
        </Card>
      )}
    </Box>
  );
}

export default CostReportView;
