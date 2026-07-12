/**
 * GatewayUsage — the "AI Gateway" tab on the Optimize page (peer to LLM Analyser).
 *
 * Where the LLM Analyser reports NB's own agent-conversation spend, this reports all
 * BYO-token traffic forwarded through the gateway — its own dataset and query API.
 *
 * This component is the SHELL: it owns the shared filter state (date window +
 * granularity), fetches the aggregate metrics once via `useGatewayData`, and hosts
 * the sub-tab navigation (Overview · Models · Users · Requests) — mirroring the AI
 * Cost Analyser's `CostAnalyser` shell. Each tab is a self-contained view under
 * `./views`; the Overview / Models / Users views read the shared `metrics`, while
 * Requests owns its own paginated fetch (`useGatewayRequests`).
 *
 * Users → Requests drill-in: clicking a user on the Users tab scopes the Requests
 * tab to that user and switches to it; a removable chip clears the scope.
 *
 * All aggregate figures come from `llm_gateway_aggregate_usage` (the gateway's
 * `POST /rpc/usage/aggregate`) via `useGatewayData`. Scoped to the current tenant
 * (from session) and the selected account (prop), like the cost-analyser.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import DashboardOutlinedIcon from '@mui/icons-material/DashboardOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import PeopleAltOutlinedIcon from '@mui/icons-material/PeopleAltOutlined';
import ReceiptLongOutlinedIcon from '@mui/icons-material/ReceiptLongOutlined';
import dayjs from 'dayjs';
import CustomTabs from '@shared/navigation/Tabs';
import { ToggleGroup } from '@ui/ToggleGroup';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { useGatewayData, type GatewayFilters } from './useGatewayData';
import OverviewView from './views/OverviewView';
import ModelsView from './views/ModelsView';
import UsersView from './views/UsersView';
import RequestsView from './views/RequestsView';
import type { GatewayGranularity } from '@api1/gateway-usage';

interface GatewayUsageProps {
  /** The selected account; scopes the request (empty = all accessible accounts). */
  accountId?: string;
}

const DAY_MS = 86_400_000;
const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);

// Default window: last 7 days, matching the cost-analyser's default.
function defaultFilters(): GatewayFilters {
  return {
    startDate: iso(Date.now() - 6 * DAY_MS),
    endDate: iso(Date.now()),
    granularity: 'day',
  };
}

const GRANULARITY_OPTIONS: { value: GatewayGranularity; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'hour', label: 'Hour' },
];

const DATE_RANGE_SHORTCUTS = ['Last 24 Hours', 'Current Week', 'Current Month', 'Last Month'];

type TabId = 'overview' | 'models' | 'users' | 'requests';

export function GatewayUsage({ accountId }: GatewayUsageProps) {
  const [filters, setFilters] = React.useState<GatewayFilters>(() => defaultFilters());
  const [tab, setTab] = React.useState<TabId>('overview');
  // Users → Requests drill-in: when set, the Requests tab is scoped to this user.
  const [selectedUser, setSelectedUser] = React.useState<{ id: string; name: string } | null>(null);

  // Tenant scoping is server-side: the RPC gateway injects the x-tenant-id header
  // from the session, so the UI sends no tenant in the request.
  const { loading, error, metrics } = useGatewayData(accountId, filters);

  // Bridge the 'YYYY-MM-DD' strings to the picker's epoch-ms model and back.
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

  // Drill-in from the Users tab: scope Requests to this user and jump to it.
  const onSelectUser = React.useCallback((id: string, name: string) => {
    setSelectedUser({ id, name });
    setTab('requests');
  }, []);
  const onClearUser = React.useCallback(() => setSelectedUser(null), []);

  // Pass MUI icons as component references (not JSX elements) so CustomTabs
  // renders them with its built-in `.tab-icon` styling (idle grey, selected
  // colour change) — matching every other CustomTabs usage.
  const tabOptions = [
    { value: 'overview', text: 'Overview', icon: DashboardOutlinedIcon, iconSize: 16 },
    { value: 'models', text: 'Models', icon: AutoAwesomeOutlinedIcon, iconSize: 16 },
    { value: 'users', text: 'Users', icon: PeopleAltOutlinedIcon, iconSize: 16 },
    { value: 'requests', text: 'Requests', icon: ReceiptLongOutlinedIcon, iconSize: 16 },
  ];

  return (
    <Box id='gateway-usage-root' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
      {/* Sub-tabs + shared filter controls (date range always visible; the
          granularity toggle only affects the Overview chart). */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <CustomTabs
          options={{ tabOptions }}
          value={tab}
          onChange={(next: string) => setTab(next as TabId)}
          behavior='filter'
          ariaLabel='AI Gateway screens'
        />

        <Box id='gateway-filter-bar' sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <ToggleGroup
            selection='single'
            size='sm'
            ariaLabel='Chart granularity'
            value={filters.granularity}
            onChange={(g) => setFilters((f) => ({ ...f, granularity: g as GatewayGranularity }))}
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

      {tab === 'overview' && <OverviewView metrics={metrics} filters={filters} loading={loading} error={error} />}
      {tab === 'models' && <ModelsView metrics={metrics} loading={loading} error={error} />}
      {tab === 'users' && <UsersView metrics={metrics} loading={loading} error={error} onSelectUser={onSelectUser} />}
      {tab === 'requests' && <RequestsView filters={filters} userFilter={selectedUser} onClearUser={onClearUser} />}
    </Box>
  );
}

export default GatewayUsage;
