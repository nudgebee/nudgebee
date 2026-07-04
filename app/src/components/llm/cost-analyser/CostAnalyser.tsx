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
import { Box, CircularProgress } from '@mui/material';
import DashboardOutlinedIcon from '@mui/icons-material/DashboardOutlined';
import ForumOutlinedIcon from '@mui/icons-material/ForumOutlined';
import AutoAwesomeOutlinedIcon from '@mui/icons-material/AutoAwesomeOutlined';
import SmartToyOutlinedIcon from '@mui/icons-material/SmartToyOutlined';
import HandymanOutlinedIcon from '@mui/icons-material/HandymanOutlined';
import CustomTabs from '@shared/CustomTabs';
import { Banner } from '@ui/Banner';
import { Chip } from '@ui/Chip';
import { Modal } from '@ui/Modal';
import FilterBar from './FilterBar';
import OverviewView from './views/OverviewView';
import ConversationsView from './views/ConversationsView';
import ConversationDetailView from './views/ConversationDetailView';
import ModelsView from './views/ModelsView';
import AgentsView from './views/AgentsView';
import ToolsView from './views/ToolsView';
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
    statuses: [],
    minCost: null,
    maxCost: null,
    anomaliesOnly: false,
    modelMatchMode: 'any',
  };
}

type TabId = 'overview' | 'conversations' | 'models' | 'agents' | 'tools';

export function CostAnalyser({ accountId }: CostAnalyserProps) {
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

  const effectiveAccountId = selectedAccountId || accountId;

  const patch = (p: Partial<CostFilters>) => setFilters((f) => ({ ...f, ...p }));
  const reset = () => setFilters(defaultFilters());

  const { loading, error, usageFilters, metrics, prevTotals, conversations, listCap } = useCostData(effectiveAccountId, filters);

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

  // Pass MUI icons as component references (not JSX elements) so SafeIcon
  // renders them with the `.tab-icon` class — this routes them through
  // CustomTabs' built-in icon styling (idle grey, selected color change),
  // matching how every other CustomTabs usage feeds in its icons.
  const tabOptions = [
    { value: 'overview', text: 'Overview', icon: DashboardOutlinedIcon, iconSize: 16 },
    { value: 'conversations', text: 'Conversations', icon: ForumOutlinedIcon, iconSize: 16 },
    { value: 'models', text: 'Models', icon: AutoAwesomeOutlinedIcon, iconSize: 16 },
    { value: 'agents', text: 'Agents', icon: SmartToyOutlinedIcon, iconSize: 16 },
    { value: 'tools', text: 'Tools', icon: HandymanOutlinedIcon, iconSize: 16 },
  ];

  return (
    <Box id='cost-analyser-root' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', pb: 'var(--ds-space-5)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
        <CustomTabs
          options={{ tabOptions }}
          value={tab}
          onChange={(next: string) => setTab(next as TabId)}
          behavior='filter'
          ariaLabel='Cost Analyser screens'
        />
        <Chip size='xs' variant='tag' tone='info'>
          Live data
        </Chip>
      </Box>

      {/* Filter bar sits below the sub-tabs; values persist across tabs. */}
      <FilterBar
        filters={filters}
        onChange={patch}
        onReset={reset}
        options={usageFilters}
        accountId={selectedAccountId}
        onAccountChange={setSelectedAccountId}
        anchorDate={anchorToday()}
      />

      {/* The Agents and Tools tabs fetch their own data (ai_list_agent_costs /
          ai_aggregate_tool_usage) and own their loading/error, so they sit outside
          the shared metrics/list gate. */}
      {tab === 'agents' ? (
        <AgentsView accountId={effectiveAccountId} filters={filters} agentOptions={usageFilters?.agents ?? []} onSelectRun={openRunDirect} />
      ) : tab === 'tools' ? (
        <ToolsView accountId={effectiveAccountId} filters={filters} onSelectRun={openRunDirect} />
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
                  />
                ))}
              {tab === 'conversations' && (
                <ConversationsView
                  loading={loading}
                  runs={runs}
                  total={conversations?.page?.total ?? runs.length}
                  listCap={listCap}
                  accountNameById={accountNameById}
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
