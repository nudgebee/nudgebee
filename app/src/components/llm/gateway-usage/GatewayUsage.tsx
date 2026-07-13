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
import { useRouter } from 'next/router';
import RocketLaunchOutlinedIcon from '@mui/icons-material/RocketLaunchOutlined';
import DashboardOutlinedIcon from '@mui/icons-material/DashboardOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import PeopleAltOutlinedIcon from '@mui/icons-material/PeopleAltOutlined';
import ReceiptLongOutlinedIcon from '@mui/icons-material/ReceiptLongOutlined';
import HandymanOutlinedIcon from '@mui/icons-material/HandymanOutlined';
import dayjs from 'dayjs';
import CustomTabs from '@shared/navigation/Tabs';
import { ToggleGroup } from '@ui/ToggleGroup';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { useGatewayData, type GatewayFilters } from './useGatewayData';
import ConnectView from './views/ConnectView';
import OverviewView from './views/OverviewView';
import ModelsView from './views/ModelsView';
import UsersView from './views/UsersView';
import RequestsView from './views/RequestsView';
import ToolsView from './views/ToolsView';
import type { GatewayGranularity } from '@api1/gateway-usage';

interface GatewayUsageProps {
  /** The selected account; scopes the request (empty = all accessible accounts). */
  accountId?: string;
  /** The gateway's public base URL, for the Connect tab's snippets (empty when unconfigured). */
  gatewayUrl?: string;
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

// Every sub-tab, and the source of truth for TabId — a new tab added here is
// automatically deep-linkable, and one added only to `tabOptions` below fails to
// type-check rather than silently resolving to the default.
const TAB_IDS = ['connect', 'overview', 'models', 'users', 'requests', 'tools'] as const;

type TabId = (typeof TAB_IDS)[number];

// The parent's top-level fragment for this tab (optimise page `filterOptions`).
// The sub-tab is encoded as `#ai-gateway/<sub-tab>` so a shared link lands on the
// exact sub-tab. TabId doubles as the URL fragment — no separate mapping needed.
const PARENT_FRAGMENT = 'ai-gateway';
const DEFAULT_TAB: TabId = 'overview';

// Resolves the current URL hash to the sub-tab it selects.
//   null            — the hash isn't this tab's (we're heading to another top-level
//                     tab); leave our state alone.
//   DEFAULT_TAB     — the hash is ours but names no valid sub-tab. A bare `#ai-gateway`
//                     is what the top-level tab's own link points at, so it must resolve
//                     to the default rather than "no opinion" — otherwise clicking that
//                     tab strips the sub-fragment and the URL stops matching the screen.
//                     Mirrors Auto Optimize, whose parent tab resets to its first sub-tab.
//   TabId           — the sub-tab named in `#ai-gateway/<sub-tab>`.
function subTabFromHash(): TabId | null {
  if (typeof window === 'undefined') return null;
  const hash = decodeURIComponent((window.location.hash || '').replace('#', ''));
  const [parent, sub] = hash.split('/');
  if (parent !== PARENT_FRAGMENT) return null;
  return TAB_IDS.includes(sub as TabId) ? (sub as TabId) : DEFAULT_TAB;
}

export function GatewayUsage({ accountId, gatewayUrl }: GatewayUsageProps) {
  const router = useRouter();
  const [filters, setFilters] = React.useState<GatewayFilters>(() => defaultFilters());
  // Initialize from the URL so a deep link (`#ai-gateway/users`) opens on that
  // sub-tab; fall back to Overview.
  const [tab, setTab] = React.useState<TabId>(() => subTabFromHash() ?? DEFAULT_TAB);
  // Users → Requests drill-in: when set, the Requests tab is scoped to this user.
  const [selectedUser, setSelectedUser] = React.useState<{ id: string; name: string } | null>(null);
  // Bumped whenever the date window is set from somewhere other than the picker
  // itself (i.e. the Overview drill-in), to re-key it — see the picker below.
  const [dateResetNonce, setDateResetNonce] = React.useState(0);
  // Tools → Requests drill-in: when set, the Requests tab is scoped to this tool.
  const [selectedTool, setSelectedTool] = React.useState<string | null>(null);

  // Tenant scoping is server-side: the RPC gateway injects the x-tenant-id header
  // from the session, so the UI sends no tenant in the request.
  const { loading, error, metrics } = useGatewayData(accountId, filters);

  // Tab → URL. Writing `#ai-gateway/<tab>` on every tab change (click or drill-in)
  // makes the current sub-tab shareable and survives reload. A shallow `replace`
  // keeps this out of the history stack and off the data-fetch path; the parent
  // optimise page matches on the `ai-gateway` prefix only, so it stays on this tab
  // and ignores the sub-fragment (it has no `tabOptions`).
  //
  // The guards mean mount never navigates: `tab` is seeded from the hash, so they
  // already agree. Only a real tab change writes.
  React.useEffect(() => {
    const sub = subTabFromHash();
    // A foreign hash means another top-level tab owns the URL — either we're being
    // switched away from, or a click on our own tab hasn't landed its navigation
    // yet (AnchorComponent selects the tab synchronously, ahead of the Link push).
    // Writing here would clobber that hash and race the navigation already in
    // flight. Same rule as the reader below: not our hash, not our business.
    if (sub === null || sub === tab) return;
    // `replace` rejects with a `cancelled` error ("Cancel rendering route") when a
    // newer navigation supersedes this one — e.g. a top-level tab click landing
    // mid-flight. The newer URL is the one we want, so a cancellation is expected,
    // not a failure; absorb it and let any real navigation error surface.
    router
      .replace({ pathname: router.pathname, query: router.query, hash: `#${PARENT_FRAGMENT}/${tab}` }, undefined, { shallow: true })
      .catch((err: Error & { cancelled?: boolean }) => {
        if (!err?.cancelled) throw err;
      });
    // Deliberately keyed on `tab` alone: this effect writes, and the one below reads.
    // Adding `router` (a fresh object each render) would re-fire the write on every
    // navigation and fight that reader. The closed-over router is from this commit,
    // so its pathname/query are current.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  // URL → tab, for hash changes that don't originate from setTab: browser
  // back/forward, and the top-level AI Gateway tab's own link (which points at a
  // bare `#ai-gateway`, and so resolves to the default sub-tab — without this the
  // screen would keep showing the old sub-tab that the URL no longer names).
  // A null means the hash belongs to another top-level tab and isn't ours to act on.
  React.useEffect(() => {
    const sub = subTabFromHash();
    if (sub && sub !== tab) setTab(sub);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.asPath]);

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

  // Drill-in from the Overview chart: clicking a column narrows the window to that
  // single day. Every view reads the same `filters`, so the whole report — KPIs,
  // chart, breakdowns — re-scopes with it.
  const onSelectDay = React.useCallback((date: string) => {
    setFilters((f) => ({ ...f, startDate: date, endDate: date }));
    setDateResetNonce((n) => n + 1);
  }, []);

  // Drill-in from the Users tab: scope Requests to this user and jump to it.
  const onSelectUser = React.useCallback((id: string, name: string) => {
    setSelectedUser({ id, name });
    setTab('requests');
  }, []);
  const onClearUser = React.useCallback(() => setSelectedUser(null), []);

  // Drill-in from the Tools tab: scope Requests to this tool and jump to it.
  const onSelectTool = React.useCallback((tool: string) => {
    setSelectedTool(tool);
    setTab('requests');
  }, []);
  const onClearTool = React.useCallback(() => setSelectedTool(null), []);

  // Pass MUI icons as component references (not JSX elements) so CustomTabs
  // renders them with its built-in `.tab-icon` styling (idle grey, selected
  // colour change) — matching every other CustomTabs usage.
  // `value: TabId` ties each tab to TAB_IDS, so a tab can't be shown here without
  // also being deep-linkable.
  const tabOptions: { value: TabId; text: string; icon: React.ElementType; iconSize: number }[] = [
    { value: 'connect', text: 'Connect', icon: RocketLaunchOutlinedIcon, iconSize: 16 },
    { value: 'overview', text: 'Overview', icon: DashboardOutlinedIcon, iconSize: 16 },
    { value: 'models', text: 'Models', icon: AutoAwesomeOutlinedIcon, iconSize: 16 },
    { value: 'users', text: 'Users', icon: PeopleAltOutlinedIcon, iconSize: 16 },
    { value: 'requests', text: 'Requests', icon: ReceiptLongOutlinedIcon, iconSize: 16 },
    { value: 'tools', text: 'Tools', icon: HandymanOutlinedIcon, iconSize: 16 },
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

        {/* The date/granularity controls only apply to the data tabs, not Connect. */}
        {tab !== 'connect' && (
          <Box id='gateway-filter-bar' sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
            <ToggleGroup
              selection='single'
              size='sm'
              ariaLabel='Chart granularity'
              value={filters.granularity}
              onChange={(g) => setFilters((f) => ({ ...f, granularity: g as GatewayGranularity }))}
              options={GRANULARITY_OPTIONS}
            />
            {/* The picker snapshots `passedSelectedDateTime` into its own state and
                only re-reads it when `resetDateTime` is set, so a window changed
                from outside it (the Overview drill-in) would leave its label showing
                the old range while the data below refreshed. Re-keying remounts it
                against the new value — same approach as the cost analyser's
                FilterBar. The nonce only moves on a drill-in, so the picker isn't
                remounted while the user is driving it. */}
            <CustomDateTimeRangePicker
              key={`gateway-date-picker-${dateResetNonce}`}
              passedSelectedDateTime={dateTimeValue}
              onChange={handleDateRangeChange}
              minDate={dayjs().subtract(1, 'year')}
              shortCuts={DATE_RANGE_SHORTCUTS}
            />
          </Box>
        )}
      </Box>

      {tab === 'connect' && <ConnectView gatewayUrl={gatewayUrl} />}
      {tab === 'overview' && <OverviewView metrics={metrics} filters={filters} loading={loading} error={error} onSelectDay={onSelectDay} />}
      {tab === 'models' && <ModelsView metrics={metrics} loading={loading} error={error} />}
      {tab === 'users' && <UsersView metrics={metrics} loading={loading} error={error} onSelectUser={onSelectUser} />}
      {tab === 'requests' && (
        <RequestsView filters={filters} userFilter={selectedUser} onClearUser={onClearUser} toolFilter={selectedTool} onClearTool={onClearTool} />
      )}
      {tab === 'tools' && <ToolsView metrics={metrics} loading={loading} error={error} onSelectTool={onSelectTool} />}
    </Box>
  );
}

export default GatewayUsage;
