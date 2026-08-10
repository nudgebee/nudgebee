/**
 * CostAnalyser — entry component for the AI Cost Analyser.
 *
 * Mounted as a tab inside the Ask-Nubi Settings modal (SettingsModal.jsx). Owns
 * the global filter state (spec §10) and the sub-screen navigation. Real cost
 * data comes from the five `ai_*` gateway actions via `useCostData`
 * (api-server/nudgebee-ai-cost-analyser-ui-api-contract.md). A handful of widgets
 * the backend can't yet back — cost-over-time, calls-over-time, per-model trend
 * sparklines, "cost by assistant", "compared to similar runs" — keep rendering on
 * mock fixtures and are tagged "sample data" so the gap is explicit.
 */
import * as React from 'react';
import { useRouter } from 'next/router';
import { Box, CircularProgress } from '@mui/material';
import DashboardOutlinedIcon from '@mui/icons-material/DashboardOutlined';
import ForumOutlinedIcon from '@mui/icons-material/ForumOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import SmartToyOutlinedIcon from '@mui/icons-material/SmartToyOutlined';
import HandymanOutlinedIcon from '@mui/icons-material/HandymanOutlined';
import PeopleAltOutlinedIcon from '@mui/icons-material/PeopleAltOutlined';
import RateReviewOutlinedIcon from '@mui/icons-material/RateReviewOutlined';
import AccountBalanceWalletOutlinedIcon from '@mui/icons-material/AccountBalanceWalletOutlined';
import CustomTabs from '@shared/navigation/Tabs';
import { Banner } from '@ui/Banner';
import { Modal } from '@ui/Modal';
import FilterBar from './FilterBar';
import OverviewView from './views/OverviewView';
import ConversationsView from './views/ConversationsView';
import ConversationDetailView from './views/ConversationDetailView';
import ModelsView from './views/ModelsView';
import AgentsView from './views/AgentsView';
import ToolsView from './views/ToolsView';
import UsersView from './views/UsersView';
import AccountsView from './views/AccountsView';
import { hasFeatureAccess, isTenantWideRole } from '@lib/auth';
import CritiqueAnalyser from '@components/llm/critique-analyser/CritiqueAnalyser';
import { rowToRun } from './adapt';
import { useConversationTree, useCostData } from './useCostData';
import type { CostFilters } from './types';

interface CostAnalyserProps {
  /** The selected account; scopes every API call (empty = all accessible accounts). */
  accountId?: string;
}

const DAY_MS = 86_400_000;
const iso = (ms: number) => new Date(ms).toISOString().slice(0, 10);

/** "Today" anchor for the date presets — the real current date. */
function anchorToday(): string {
  return iso(Date.now());
}

// Default window: last 7 days, so data shows without the user widening the range first.
function defaultFilters(): CostFilters {
  return {
    startDate: iso(Date.now() - 6 * DAY_MS),
    endDate: anchorToday(),
    granularity: 'day',
    triggerTypes: [],
    assistants: [],
    templates: [],
    sources: [],
    models: [],
    providers: [],
    agents: [],
    statuses: [],
    userId: '',
    minCost: null,
    maxCost: null,
    anomaliesOnly: false,
    modelMatchMode: 'any',
  };
}

type TabId = 'overview' | 'conversations' | 'models' | 'agents' | 'tools' | 'users' | 'accounts' | 'critiques';

const VALID_TAB_IDS: TabId[] = ['overview', 'conversations', 'models', 'agents', 'tools', 'users', 'accounts', 'critiques'];

export function CostAnalyser({ accountId }: CostAnalyserProps) {
  const router = useRouter();
  const [filters, setFilters] = React.useState<CostFilters>(() => defaultFilters());
  const [tab, setTab] = React.useState<TabId>('overview');
  // The open conversation: its session id + its own account (the tree endpoint
  // needs a concrete account_id, which the "all accounts" list scope lacks).
  const [selected, setSelected] = React.useState<{
    sessionId: string;
    accountId?: string;
    agentId?: string;
    initialTab?: 'subtasks' | 'optimize';
    autoRunOptimize?: boolean;
  } | null>(null);
  // Account scope picked in the filter bar ('' = all accounts the tenant can read).
  const [selectedAccountId, setSelectedAccountId] = React.useState<string>('');
  // Bumped only when the date range is set programmatically (a cost-over-time bar
  // click). The filter bar keys its date picker on this so the picker re-derives
  // its trigger label from the new range instead of keeping a stale shortcut
  // label ("Current Week") — the picker only seeds from props on mount, and a
  // click on the picker's own shortcuts leaves this untouched, so their labels
  // stay put.
  const [dateResetNonce, setDateResetNonce] = React.useState(0);
  // Gates the Accounts tab (consolidated per-account cost report) — same
  // AI_COST_REPORT flag the daily Slack digest cron checks per tenant
  // (llm-server's RunAiCostDailyDigest), so the tab only appears once the
  // feature is actually rolled out for this tenant.
  const [hasAiCostReportAccess, setHasAiCostReportAccess] = React.useState(false);
  React.useEffect(() => {
    let cancelled = false;
    hasFeatureAccess('AI_COST_REPORT').then((res) => {
      if (!cancelled) setHasAiCostReportAccess(Boolean(res));
    });
    return () => {
      cancelled = true;
    };
  }, []);

  const canViewAccountsTab = hasAiCostReportAccess && isTenantWideRole();

  // The Accounts tab's "as of" date is deliberately its own state, not part of
  // the shared `filters` — that object also drives every other tab's date
  // range via FilterBar, and routing the Accounts date picker through it
  // would silently move Overview/Conversations/etc.'s range whenever someone
  // picked a date on the Accounts tab.
  const [accountsReferenceDate, setAccountsReferenceDate] = React.useState<string>(() => anchorToday());

  // Deep-link support: a URL like #cost-analyser/accounts (e.g. the Slack
  // digest's "View in Cost Analyser" link) should land directly on that
  // sub-tab instead of always defaulting to Overview. Consumed once the
  // router is ready; after that, tab switches are driven by clicking
  // CustomTabs, not the URL — mirrors the outer Optimise page's own
  // fragment/subFragment handling for its "Auto Optimize" sub-tabs. The
  // Accounts tab is itself feature-flag gated, so a deep link to it must not
  // bypass that gate — canViewAccountsTab is a dependency here so that
  // once the async flag check resolves true, this effect re-runs and honors
  // a link that arrived before it did.
  React.useEffect(() => {
    if (!router.isReady) return;
    const hash = router.asPath.split('#')[1];
    if (!hash) return;
    const [, subFragment] = hash.split('/');
    if (!subFragment || !VALID_TAB_IDS.includes(subFragment as TabId)) return;
    if (subFragment === 'accounts' && !canViewAccountsTab) return;
    setTab(subFragment as TabId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.isReady, canViewAccountsTab]);

  // Pin the report to an exact date via ?asOf=YYYY-MM-DD (e.g. the Slack
  // digest's "View in Cost Analyser" link) instead of defaulting to today.
  // Without this, the Accounts tab shows live/partial-day numbers that can
  // differ from what the digest just reported — the digest always reports
  // on the last fully-completed day, never today's still-in-progress total.
  // Scoped to accountsReferenceDate only — see the note above on why this
  // must not touch the shared `filters`.
  React.useEffect(() => {
    if (!router.isReady) return;
    const asOf = router.query.asOf;
    if (typeof asOf === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(asOf)) {
      setAccountsReferenceDate(asOf);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [router.isReady]);

  const effectiveAccountId = selectedAccountId || accountId;

  const patch = (p: Partial<CostFilters>) => setFilters((f) => ({ ...f, ...p }));
  const reset = () => {
    setFilters(defaultFilters());
    setSelectedAccountId('');
  };

  const { loading, error, usageFilters, metrics, prevTotals, conversations, listCap } = useCostData(effectiveAccountId, filters, tab);

  // Adapt the API rows into the UI's Run shape (already cost-desc from the list call).
  const runs = React.useMemo(() => (conversations?.rows ?? []).map(rowToRun), [conversations]);

  // account_id → name, reused from the already-fetched filter options (no extra
  // query). Lets the conversation list label each row with its account.
  const accountNameById = React.useMemo<Record<string, string>>(
    () => Object.fromEntries((usageFilters?.accounts ?? []).map((a) => [a.id, a.name])),
    [usageFilters]
  );

  // Detail tree is scoped to the opened conversation's own account.
  const detailAccountId = selected?.accountId || effectiveAccountId;
  const detail = useConversationTree(detailAccountId, selected?.sessionId ?? null);

  const openRun = (sessionId: string) => {
    const row = runs.find((r) => r.runId === sessionId);
    setSelected({ sessionId, accountId: row?.accountId });
  };
  // "Analyse" — open straight on the Optimize tab and analyze (cached or fresh).
  const openAnalyse = (sessionId: string) => {
    const row = runs.find((r) => r.runId === sessionId);
    setSelected({ sessionId, accountId: row?.accountId, initialTab: 'optimize', autoRunOptimize: true });
  };
  // Cross-link from the Agents tab: the session + account come from the agent row
  // (the conversation isn't necessarily in the loaded list page). agentId, when
  // given, focuses the detail on that specific agent invocation.
  const openRunDirect = (sessionId: string, acct?: string, agentId?: string) => setSelected({ sessionId, accountId: acct, agentId });
  const closeRun = () => setSelected(null);
  // Drill-in from the Users tab: scope the whole report to this user and jump to
  // their conversations. The active scope shows as the User pill in the filter bar.
  const openUser = (userId: string) => {
    patch({ userId });
    setTab('conversations');
  };

  // Pass MUI icons as component references (not JSX elements) so SafeIcon
  // renders them with the `.tab-icon` class — this routes them through
  // CustomTabs' built-in icon styling (idle grey, selected color change),
  // matching how every other CustomTabs usage feeds in its icons.
  // fragment mirrors value on every entry — CustomTabs' `behavior='router'`
  // mode uses it to build the URL hash (#cost-analyser/{fragment}) so the tab
  // strip actually navigates instead of only updating local state (that's
  // also what the deep-link effect above reads back on load).
  const tabOptions = [
    { value: 'overview', fragment: 'overview', text: 'Overview', icon: DashboardOutlinedIcon, iconSize: 16 },
    { value: 'conversations', fragment: 'conversations', text: 'Conversations', icon: ForumOutlinedIcon, iconSize: 16 },
    { value: 'models', fragment: 'models', text: 'Models', icon: AutoAwesomeOutlinedIcon, iconSize: 16 },
    { value: 'agents', fragment: 'agents', text: 'Agents', icon: SmartToyOutlinedIcon, iconSize: 16 },
    { value: 'tools', fragment: 'tools', text: 'Tools', icon: HandymanOutlinedIcon, iconSize: 16 },
    { value: 'users', fragment: 'users', text: 'Users', icon: PeopleAltOutlinedIcon, iconSize: 16 },
    canViewAccountsTab && {
      value: 'accounts',
      fragment: 'accounts',
      text: 'Accounts',
      icon: AccountBalanceWalletOutlinedIcon,
      iconSize: 16,
    },
    { value: 'critiques', fragment: 'critiques', text: 'Critiques', icon: RateReviewOutlinedIcon, iconSize: 16 },
  ].filter(Boolean);

  return (
    <Box id='cost-analyser-root' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <CustomTabs
          options={{ tabOptions, fragment: 'cost-analyser' }}
          value={tab}
          onChange={(next: string) => setTab(next as TabId)}
          behavior='router'
          ariaLabel='Cost Analyser screens'
        />
      </Box>

      {/* Hidden for Critiques (own cross-tenant filter bar, no account concept) and
          Accounts (an account-level daily/MTD/prev-month rollup with no per-dimension
          breakdown — every filter here would silently no-op). */}
      {tab !== 'critiques' && tab !== 'accounts' && (
        <FilterBar
          filters={filters}
          onChange={patch}
          onReset={reset}
          options={usageFilters}
          accountId={selectedAccountId}
          onAccountChange={setSelectedAccountId}
          dateResetNonce={dateResetNonce}
          // The Agents tab owns local include/exclude agent controls (with a curated
          // default exclude), so the shared Agent filter would be a redundant, no-op
          // third control there — hide it and let that tab's own filters stand.
          showAgents={tab !== 'agents'}
        />
      )}

      {/* Agents, Tools, Users, Critiques fetch their own data, outside the shared metrics/list gate. */}
      {tab === 'critiques' ? (
        <CritiqueAnalyser />
      ) : tab === 'agents' ? (
        <AgentsView accountId={effectiveAccountId} filters={filters} agentOptions={usageFilters?.agents ?? []} onSelectRun={openRunDirect} />
      ) : tab === 'tools' ? (
        <ToolsView accountId={effectiveAccountId} filters={filters} onSelectRun={openRunDirect} />
      ) : tab === 'users' ? (
        <UsersView accountId={effectiveAccountId} filters={filters} userOptions={usageFilters?.users ?? []} onSelectUser={openUser} />
      ) : tab === 'accounts' ? (
        <AccountsView
          accountId={effectiveAccountId}
          onAccountChange={setSelectedAccountId}
          accountOptions={usageFilters?.accounts ?? []}
          referenceDate={accountsReferenceDate}
          onReferenceDateChange={setAccountsReferenceDate}
        />
      ) : (
        <>
          {error && <Banner tone='critical' title='Could not load cost data' message={error} />}

          {!error && (
            <>
              {/* Overview & Models are chart-heavy, so they keep the centered
                  spinner. Conversations owns a CustomTable2, so it renders its
                  own skeleton via the loading flag — no separate spinner. */}
              {tab === 'overview' &&
                (loading ? (
                  <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
                    <CircularProgress size={28} />
                  </Box>
                ) : (
                  <OverviewView
                    metrics={metrics}
                    prevTotals={prevTotals}
                    runs={runs}
                    filters={filters}
                    onSelectRun={openRun}
                    onAnalyse={openAnalyse}
                    accountNameById={accountNameById}
                    onSelectModel={(model) => patch({ models: [model] })}
                    onSelectSource={(source) => patch({ sources: [source] })}
                    onSelectPeriod={(startDate, endDate) => {
                      patch({ startDate, endDate });
                      setDateResetNonce((n) => n + 1);
                    }}
                  />
                ))}
              {tab === 'conversations' && (
                <ConversationsView
                  loading={loading}
                  runs={runs}
                  total={conversations?.page?.total ?? runs.length}
                  listCap={listCap}
                  accountNameById={accountNameById}
                  startDate={filters.startDate}
                  endDate={filters.endDate}
                  accountLabel={effectiveAccountId ? accountNameById[effectiveAccountId] : undefined}
                  onSelectRun={openRun}
                  onAnalyse={openAnalyse}
                />
              )}
              {tab === 'models' &&
                (loading ? (
                  <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: 240 }}>
                    <CircularProgress size={28} />
                  </Box>
                ) : (
                  <ModelsView metrics={metrics} filters={filters} />
                ))}
            </>
          )}
        </>
      )}

      {/* Conversation detail opens in a popup (DS Modal) rather than replacing the list. */}
      <Modal open={!!selected} handleClose={closeRun} width='lg' maxHeight='90vh' title='Conversation details'>
        <ConversationDetailView
          run={detail.run}
          loading={detail.loading}
          error={detail.error}
          usage={detail.usage}
          conversationId={selected?.sessionId}
          accountId={detailAccountId}
          initialAgentId={selected?.agentId}
          initialTab={selected?.initialTab}
          autoRunOptimize={selected?.autoRunOptimize}
          onBack={closeRun}
          hideBackBar
        />
      </Modal>
    </Box>
  );
}

export default CostAnalyser;
