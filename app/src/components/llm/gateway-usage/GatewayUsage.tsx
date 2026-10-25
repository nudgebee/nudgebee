/**
 * GatewayUsage — the body of the AI Gateway page (`/ai-gateway`, its own sidenav
 * section).
 *
 * Where the LLM Analyser reports NB's own agent-conversation spend, this reports all
 * BYO-token traffic forwarded through the gateway — its own dataset and query API.
 *
 * This component is the SHELL: it owns the shared filter state (date window +
 * granularity), fetches the aggregate metrics once via `useGatewayData`, and hosts
 * the sub-tab navigation — mirroring the AI Cost Analyser's `CostAnalyser` shell.
 * Each tab is a self-contained view under `./views`; the Overview / Models / Users
 * views read the shared `metrics`, while Requests owns its own paginated fetch
 * (`useGatewayRequests`).
 *
 * The tabs are split into the page's sections (`GROUPS`: Connect · Monitor ·
 * Activity · Governance). The page's top tab strip and the sidenav pick the
 * section through the hash; this shell shows only the active section's tabs. It
 * stays mounted across sections, so a drill-in that crosses one (Users → Requests)
 * keeps its scope.
 *
 * Users → Requests drill-in: clicking a user on the Users tab scopes the Requests
 * tab to that user and switches to it; the Requests tab's own User filter
 * dropdown shows and edits the same scope.
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
import VpnKeyOutlinedIcon from '@mui/icons-material/VpnKeyOutlined';
import ReceiptLongOutlinedIcon from '@mui/icons-material/ReceiptLongOutlined';
import ForumOutlinedIcon from '@mui/icons-material/ForumOutlined';
import HandymanOutlinedIcon from '@mui/icons-material/HandymanOutlined';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import dayjs from 'dayjs';
import CustomTabs from '@shared/navigation/Tabs';
import { ToggleGroup } from '@ui/ToggleGroup';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { useGatewayData, type GatewayFilters, type GovScope } from './useGatewayData';
import ConnectView from './views/ConnectView';
import OverviewView from './views/OverviewView';
import ModelsView from './views/ModelsView';
import UsersView from './views/UsersView';
import KeysView from './views/KeysView';
import RequestsView from './views/RequestsView';
import SessionsView from './views/SessionsView';
import ToolsView from './views/ToolsView';
import GovernanceView from './views/GovernanceView';
import type { GatewayGranularity } from '@api1/gateway-usage';
import { canViewGatewayUserAccess } from '@lib/auth';

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
const TAB_IDS = ['connect', 'overview', 'models', 'users', 'keys', 'requests', 'sessions', 'tools', 'governance'] as const;

type TabId = (typeof TAB_IDS)[number];

// The page's sections, in order, each with its tabs (first = the section's
// default). The section id is the top-level hash fragment the page's tab strip
// and the sidenav link to (pages/ai-gateway/index.tsx, layout/index.jsx).
export const GROUPS = {
  connect: ['connect'],
  monitor: ['overview', 'models', 'users', 'keys'],
  activity: ['requests', 'sessions', 'tools'],
  governance: ['governance'],
} as const satisfies Record<string, readonly TabId[]>;

export type GroupId = keyof typeof GROUPS;

const GROUP_IDS = Object.keys(GROUPS) as GroupId[];
const DEFAULT_TAB: TabId = 'overview';

const groupOf = (tab: TabId): GroupId => GROUP_IDS.find((g) => (GROUPS[g] as readonly TabId[]).includes(tab)) ?? 'monitor';

// The one hash each tab is written as: `#<section>` for a section's default tab,
// `#<section>/<tab>` otherwise — so the sidenav's bare `#monitor` link and a
// tab's own URL never disagree about which tab is open.
function hashFor(tab: TabId): string {
  const group = groupOf(tab);
  return GROUPS[group][0] === tab ? `#${group}` : `#${group}/${tab}`;
}

const currentHash = () => (typeof window === 'undefined' ? '' : decodeURIComponent(window.location.hash || ''));

// Resolves the current URL hash to the tab it selects. A section with no (or an
// unknown) tab opens its default tab; a tab named under the wrong section opens
// that tab, and the writer moves the hash to its real section. No hash, or a
// foreign one, is Overview.
function tabFromHash(): TabId {
  const [parent, sub] = currentHash().replace('#', '').split('/');
  let tab: TabId = DEFAULT_TAB;
  if (TAB_IDS.includes(sub as TabId)) tab = sub as TabId;
  else if (GROUP_IDS.includes(parent as GroupId)) tab = GROUPS[parent as GroupId][0];
  if (tab === 'users' && !canViewGatewayUserAccess()) return DEFAULT_TAB;
  return tab;
}

export function GatewayUsage({ accountId, gatewayUrl }: GatewayUsageProps) {
  const router = useRouter();
  const [filters, setFilters] = React.useState<GatewayFilters>(() => defaultFilters());
  // Initialize from the URL so a deep link (`#monitor/users`) opens on that
  // sub-tab; fall back to Overview.
  const [tab, setTab] = React.useState<TabId>(() => tabFromHash());
  // The tab the URL last named — tells the writer whether a change crossed sections.
  const urlTab = React.useRef<TabId>(tab);
  // Users → Requests drill-in: when set, the Requests tab is scoped to this user.
  const [selectedUser, setSelectedUser] = React.useState<{ id: string; name: string } | null>(null);
  // Keys → Requests drill-in: when set, the Requests tab is scoped to this API key.
  const [selectedKey, setSelectedKey] = React.useState<{ id: string; name: string } | null>(null);
  // Governance → Requests drill-in: when set, the Requests tab is scoped to the
  // rows behind a governance count (a routing/reject reason, DLP, or error class).
  const [selectedGov, setSelectedGov] = React.useState<GovScope | null>(null);
  // Requests → Requests drill-in: clicking a row's session scopes the list to that
  // one session/conversation (an inferred or client-supplied id).
  const [selectedSession, setSelectedSession] = React.useState<string | null>(null);
  // Bumped whenever the date window is set from somewhere other than the picker
  // itself (i.e. the Overview drill-in), to re-key it — see the picker below.
  const [dateResetNonce, setDateResetNonce] = React.useState(0);
  // Tools → Requests drill-in: when set, the Requests tab is scoped to this tool.
  const [selectedTool, setSelectedTool] = React.useState<string | null>(null);

  // Tenant scoping is server-side: the RPC gateway injects the x-tenant-id header
  // from the session, so the UI sends no tenant in the request.
  const { loading, error, metrics } = useGatewayData(accountId, filters);

  // Tab → URL. Every tab change (click or drill-in) writes the tab's hash, so the
  // current tab is shareable and survives reload. Within a section it's a shallow
  // `replace`; a drill-in that crosses sections (Users → Requests) is a `push`, so
  // browser Back returns to where it started instead of leaving the page.
  //
  // Mount never navigates while the hash already selects `tab` (it was seeded from
  // it); only a hash that names the tab some other way — none, or a tab under the
  // wrong section — is rewritten to its canonical form.
  React.useEffect(() => {
    const crossed = groupOf(urlTab.current) !== groupOf(tab);
    urlTab.current = tab;
    const hashSection = currentHash().replace('#', '').split('/')[0];
    if (tabFromHash() === tab && hashSection === groupOf(tab)) return;
    // A navigation rejects with a `cancelled` error ("Cancel rendering route") when
    // a newer one supersedes it — e.g. a top-level tab click landing mid-flight. The
    // newer URL is the one we want, so a cancellation is expected, not a failure;
    // absorb it and let any real navigation error surface.
    const url = { pathname: router.pathname, query: router.query, hash: hashFor(tab) };
    (crossed ? router.push(url, undefined, { shallow: true }) : router.replace(url, undefined, { shallow: true })).catch(
      (err: Error & { cancelled?: boolean }) => {
        if (!err?.cancelled) throw err;
      }
    );
    // Deliberately keyed on `tab` alone: this effect writes, and the one below reads.
    // Adding `router` (a fresh object each render) would re-fire the write on every
    // navigation and fight that reader. The closed-over router is from this commit,
    // so its pathname/query are current.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab]);

  // URL → tab, for hash changes that don't originate from setTab: browser
  // back/forward, and the sidenav / top tab strip links (a bare `#activity` opens
  // that section's default tab). Next leaves router.asPath unchanged when only the
  // hash changes natively (`location.hash = …`, as the guided tour does), so
  // hashchange re-syncs too.
  const [hashChangeCount, setHashChangeCount] = React.useState(0);
  React.useEffect(() => {
    const onHashChange = () => setHashChangeCount((n) => n + 1);
    window.addEventListener('hashchange', onHashChange);
    return () => window.removeEventListener('hashchange', onHashChange);
  }, []);
  React.useEffect(() => {
    const next = tabFromHash();
    if (next === tab) return;
    // The URL already names `next`, so the writer must not count this as a crossing.
    urlTab.current = next;
    setTab(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.asPath, hashChangeCount]);

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

  // Offered from an empty Requests view: widen the window to the last 30 days so a
  // sparse/inactive gateway isn't a dead end.
  const onExpandRange = React.useCallback(() => {
    setFilters((f) => ({ ...f, startDate: iso(Date.now() - 29 * DAY_MS), endDate: iso(Date.now()) }));
    setDateResetNonce((n) => n + 1);
  }, []);

  // Drill-in from the Users tab: scope Requests to this user and jump to it.
  const onSelectUser = React.useCallback((id: string, name: string) => {
    setSelectedUser({ id, name });
    setTab('requests');
  }, []);
  // The Requests tab's User dropdown reads/writes the same scope.
  const onChangeUser = React.useCallback((user: { id: string; name: string } | null) => setSelectedUser(user), []);

  // Drill-in from the Keys tab: scope Requests to this key and jump to it.
  // A key belongs to one user, so a user scope left over from an earlier drill-in
  // could only narrow this to nothing; clear it.
  const onSelectKey = React.useCallback((id: string, name: string) => {
    setSelectedKey({ id, name });
    setSelectedUser(null);
    setTab('requests');
  }, []);
  const onChangeKey = React.useCallback((key: { id: string; name: string } | null) => setSelectedKey(key), []);

  // Drill-in from the Tools tab: scope Requests to this tool and jump to it.
  const onSelectTool = React.useCallback((tool: string) => {
    setSelectedTool(tool);
    setTab('requests');
  }, []);
  const onClearTool = React.useCallback(() => setSelectedTool(null), []);

  // Drill-in from the Governance tab: scope Requests to a governance count and jump.
  const onDrillGov = React.useCallback((scope: GovScope) => {
    setSelectedGov(scope);
    setTab('requests');
  }, []);
  const onClearGov = React.useCallback(() => setSelectedGov(null), []);

  // Session drill-in stays within the Requests tab (no tab jump — the user is
  // already here): clicking a row's session narrows the list to that session.
  const onSelectSession = React.useCallback((id: string) => setSelectedSession(id), []);
  const onClearSession = React.useCallback(() => setSelectedSession(null), []);
  // Sessions tab → Requests drill-in: scope Requests to the clicked session and jump.
  const onDrillSession = React.useCallback((id: string) => {
    setSelectedSession(id);
    setTab('requests');
  }, []);

  // Pass MUI icons as component references (not JSX elements) so CustomTabs
  // renders them with its built-in `.tab-icon` styling (idle grey, selected
  // colour change) — matching every other CustomTabs usage.
  // `value: TabId` ties each tab to TAB_IDS, so a tab can't be shown here without
  // also being deep-linkable.
  const allTabOptions: { value: TabId; text: string; icon: React.ElementType; iconSize: number }[] = [
    { value: 'connect', text: 'Connect', icon: RocketLaunchOutlinedIcon, iconSize: 16 },
    { value: 'overview', text: 'Overview', icon: DashboardOutlinedIcon, iconSize: 16 },
    { value: 'models', text: 'Models', icon: AutoAwesomeOutlinedIcon, iconSize: 16 },
    { value: 'users', text: 'Users', icon: PeopleAltOutlinedIcon, iconSize: 16 },
    { value: 'keys', text: 'Keys', icon: VpnKeyOutlinedIcon, iconSize: 16 },
    { value: 'requests', text: 'Requests', icon: ReceiptLongOutlinedIcon, iconSize: 16 },
    { value: 'sessions', text: 'Sessions', icon: ForumOutlinedIcon, iconSize: 16 },
    { value: 'tools', text: 'Tools', icon: HandymanOutlinedIcon, iconSize: 16 },
    { value: 'governance', text: 'Governance', icon: ShieldOutlinedIcon, iconSize: 16 },
  ];
  // Only the open section's tabs; a one-tab section shows no strip. Non-admins get
  // no per-user breakdown, so the Users tab is hidden for them.
  const sectionTabs: readonly TabId[] = GROUPS[groupOf(tab)];
  const tabOptions = allTabOptions.filter((t) => sectionTabs.includes(t.value) && (t.value !== 'users' || canViewGatewayUserAccess()));

  return (
    <Box id='gateway-usage-root' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
      {/* Sub-tabs + shared filter controls (date range always visible; the
          granularity toggle only affects the Overview chart). */}
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        {tabOptions.length > 1 && (
          <CustomTabs
            options={{ tabOptions }}
            value={tab}
            onChange={(next: string) => setTab(next as TabId)}
            behavior='filter'
            ariaLabel='AI Gateway screens'
          />
        )}

        {/* The date/granularity controls only apply to the data tabs, not Connect. */}
        {tab !== 'connect' && (
          <Box id='gateway-filter-bar' sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap', ml: 'auto' }}>
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
      {tab === 'keys' && <KeysView metrics={metrics} loading={loading} error={error} onSelectKey={onSelectKey} />}
      {tab === 'requests' && (
        <RequestsView
          filters={filters}
          metrics={metrics}
          metricsLoading={loading}
          userFilter={selectedUser}
          onChangeUser={onChangeUser}
          keyFilter={selectedKey}
          onChangeKey={onChangeKey}
          toolFilter={selectedTool}
          onClearTool={onClearTool}
          govFilter={selectedGov}
          onClearGov={onClearGov}
          sessionFilter={selectedSession}
          onSelectSession={onSelectSession}
          onClearSession={onClearSession}
          onExpandRange={onExpandRange}
        />
      )}
      {tab === 'sessions' && (
        <SessionsView
          filters={filters}
          metrics={metrics}
          metricsLoading={loading}
          userFilter={selectedUser}
          onChangeUser={onChangeUser}
          onDrillSession={onDrillSession}
        />
      )}
      {tab === 'tools' && <ToolsView metrics={metrics} loading={loading} error={error} onSelectTool={onSelectTool} />}
      {tab === 'governance' && <GovernanceView metrics={metrics} filters={filters} loading={loading} error={error} onDrill={onDrillGov} />}
    </Box>
  );
}

export default GatewayUsage;
