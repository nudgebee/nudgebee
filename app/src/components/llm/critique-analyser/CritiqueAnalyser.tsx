/** Cross-tenant answer-critique-quality dashboard. Gated server-side to tenant_admin+. No account concept. */
import * as React from 'react';
import { Box } from '@mui/material';
import DashboardOutlinedIcon from '@mui/icons-material/DashboardOutlined';
import SmartToyOutlinedIcon from '@mui/icons-material/SmartToyOutlined';
import LabelOutlinedIcon from '@mui/icons-material/LabelOutlined';
import ListAltOutlinedIcon from '@mui/icons-material/ListAltOutlined';
import dayjs from 'dayjs';
import CustomTabs from '@shared/navigation/Tabs';
import { ToggleGroup } from '@ui/ToggleGroup';
import FilterDropdown from '@ui/FilterDropdown';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { useCritiqueData, type CritiqueFilters, type CritiqueGranularity } from './useCritiqueData';
import OverviewView from './views/OverviewView';
import AgentsView from './views/AgentsView';
import ThemesView, { THEME_LABELS } from './views/ThemesView';
import BrowseView from './views/BrowseView';

const DAY_MS = 86_400_000;
const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);

// Default window: last 30 days — critique volume is much lower than raw usage.
function defaultFilters(): CritiqueFilters {
  return {
    startDate: iso(Date.now() - 29 * DAY_MS),
    endDate: iso(Date.now()),
    agents: [],
    granularity: 'day',
  };
}

const GRANULARITY_OPTIONS: { value: CritiqueGranularity; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
];

const DATE_RANGE_SHORTCUTS = ['Last 24 Hours', 'Current Week', 'Current Month', 'Last Month'];

type TabId = 'overview' | 'agents' | 'themes' | 'browse';

export function CritiqueAnalyser() {
  const [filters, setFilters] = React.useState<CritiqueFilters>(() => defaultFilters());
  const [tab, setTab] = React.useState<TabId>('overview');
  // Agents/Themes → Browse drill-in.
  const [browseDecisions, setBrowseDecisions] = React.useState<string[]>(['refine']);
  const [browseTheme, setBrowseTheme] = React.useState<string | undefined>(undefined);

  const { loading, error, summary, trend } = useCritiqueData(filters);

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
    setFilters((f) => ({
      ...f,
      startDate: dayjs(selection.startTime).format('YYYY-MM-DD'),
      endDate: dayjs(selection.endTime).format('YYYY-MM-DD'),
    }));
  };

  // Agent options come from the loaded summary — no separate filters endpoint.
  const agentOptions = React.useMemo(() => (summary?.by_agent ?? []).map((a) => a.agent_name).sort(), [summary]);

  const onSelectAgent = React.useCallback((agentName: string) => {
    setFilters((f) => ({ ...f, agents: [agentName] }));
    setBrowseDecisions(['refine']);
    setBrowseTheme(undefined);
    setTab('browse');
  }, []);

  const onSelectTheme = React.useCallback((theme: string) => {
    setBrowseTheme(theme);
    setTab('browse');
  }, []);
  const onClearTheme = React.useCallback(() => setBrowseTheme(undefined), []);

  const tabOptions = [
    { value: 'overview', text: 'Overview', icon: DashboardOutlinedIcon, iconSize: 16 },
    { value: 'agents', text: 'Agents', icon: SmartToyOutlinedIcon, iconSize: 16 },
    { value: 'themes', text: 'Themes', icon: LabelOutlinedIcon, iconSize: 16 },
    { value: 'browse', text: 'Browse', icon: ListAltOutlinedIcon, iconSize: 16 },
  ];

  return (
    <Box id='critique-analyser-root' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <CustomTabs
          options={{ tabOptions }}
          value={tab}
          onChange={(next: string) => setTab(next as TabId)}
          behavior='filter'
          ariaLabel='Critique Analytics screens'
        />

        <Box id='critique-filter-bar' sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <FilterDropdown
            id='critique-filter-agent'
            label='Agent'
            multiple
            options={agentOptions}
            value={filters.agents}
            onSelect={(_e: unknown, sel: (string | { value?: string })[]) =>
              setFilters((f) => ({ ...f, agents: (sel ?? []).map((o) => (typeof o === 'string' ? o : String(o?.value ?? ''))) }))
            }
          />
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Trend granularity'
            value={filters.granularity}
            onChange={(g) => setFilters((f) => ({ ...f, granularity: g as CritiqueGranularity }))}
            options={GRANULARITY_OPTIONS}
          />
          <CustomDateTimeRangePicker
            passedSelectedDateTime={dateTimeValue}
            onChange={handleDateRangeChange}
            minDate={dayjs().subtract(1, 'year')}
            shortCuts={DATE_RANGE_SHORTCUTS}
          />
        </Box>
      </Box>

      {tab === 'overview' && <OverviewView summary={summary} trend={trend} loading={loading} error={error} />}
      {tab === 'agents' && <AgentsView summary={summary} loading={loading} error={error} onSelectAgent={onSelectAgent} />}
      {tab === 'themes' && <ThemesView summary={summary} loading={loading} error={error} onSelectTheme={onSelectTheme} />}
      {tab === 'browse' && (
        <BrowseView
          filters={filters}
          decisions={browseDecisions}
          onDecisionsChange={setBrowseDecisions}
          theme={browseTheme}
          themeLabel={browseTheme ? THEME_LABELS[browseTheme] ?? browseTheme : undefined}
          onClearTheme={onClearTheme}
        />
      )}
    </Box>
  );
}

export default CritiqueAnalyser;
