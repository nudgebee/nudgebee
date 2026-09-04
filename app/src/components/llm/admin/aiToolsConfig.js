import { AgentIcon, ToolsIcon, LLMFunctionIcon, LLMConsumptionIcon, FileOutlineIcon, InMemoryIcon, DocumentationIcon } from '@assets';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import AgentsAdminTab from './AgentsAdminTab';
import ToolsAndMCPAdminTab from './ToolsAndMCPAdminTab';
import FunctionsAdminTab from './FunctionsAdminTab';
import ProvidersAdminTab from './ProvidersAdminTab';
import GatewayAdminTab from './GatewayAdminTab';
import EgressFilterAdminTab from './EgressFilterAdminTab';
import BudgetsAndLimitsAdminTab from './BudgetsAndLimitsAdminTab';
import MemoryPolicyAdminTab from './MemoryPolicyAdminTab';
import RCAFormatAdminTab from './RCAFormatAdminTab';
import MemoryLegacyAdminTab from './MemoryLegacyAdminTab';
import AccountContextAdminTab from './AccountContextAdminTab';

/**
 * AI & Tools sub-tabs (docs/ia-consolidation-plan.md, PR 3) — the single
 * source of truth for both mounts the "mirrors" rule calls for: Admin's own
 * page (pages/user-management/index.jsx, via AnchorComponent's tabOptions)
 * and the Nubi rail's compact modal (AIToolsModal.jsx). Same shape Access &
 * Users' tabOptions uses on the Admin page — `module` drives per-tab
 * dynamic-RBAC gating in both places.
 *
 * `module` values reuse the dynamic-RBAC modules the underlying actions
 * already normalize to (permissionCatalog.ts's token-0 heuristic) rather
 * than inventing new ones: every `ai_*` action (agents, tools, functions,
 * budget, rca-format) shares one module, `ai`; Providers reuses
 * `integrations` (same integrations_list/_aggregate actions Admin →
 * Integrations already gates on); Gateway reuses `llm` (llm_gateway_* —
 * same module Settings' own hasPermission('llm', 'Read') check already
 * names); Egress Filter reuses `egressfilter`.
 *
 * `legacyOnly: true` (Memory, Account Context) marks the two sub-tabs that
 * only make sense for a tenant that doesn't have b-Cortex — permanently
 * true for OSS (BCortexModal itself is stripped from the OSS snapshot, not
 * just flag-gated), temporarily true for an EE tenant not yet migrated to
 * MEMORY_MODULE. Both callers (user-management/index.jsx, AIToolsModal.jsx)
 * filter this array by their own `bcortexEnabled === false` before turning
 * it into tabOptions, same condition Settings' old "Global Context"/"Memory"
 * tabs used. Not marked with a `module` — same reasoning SettingsModal never
 * gated them beyond the flag check.
 */
export const AI_TOOLS_SUB_TABS = [
  {
    id: 'agents',
    fragment: '',
    text: 'Agents',
    icon: AgentIcon,
    Body: AgentsAdminTab,
    module: 'ai',
  },
  {
    id: 'tools-mcp',
    fragment: 'tools-mcp',
    text: 'Tools & MCP',
    icon: ToolsIcon,
    Body: ToolsAndMCPAdminTab,
    module: 'ai',
  },
  {
    id: 'functions',
    fragment: 'functions',
    text: 'Functions',
    icon: LLMFunctionIcon,
    Body: FunctionsAdminTab,
    module: 'ai',
    // Settings only ever pushed this tab after an async hasFeatureAccess('LLM_FUNCTION')
    // check resolved true — same gate, now read via useFeatureAccess() by each caller.
    requiresFeature: 'LLM_FUNCTION',
  },
  {
    id: 'providers',
    fragment: 'providers',
    text: 'Providers',
    icon: SettingsOutlinedIcon,
    Body: ProvidersAdminTab,
    module: 'integrations',
  },
  {
    id: 'gateway',
    fragment: 'gateway',
    text: 'Gateway',
    icon: HubOutlinedIcon,
    Body: GatewayAdminTab,
    module: 'llm',
    // Settings additionally gated this on the llmGateway UI feature toggle
    // (isUiFeatureEnabled) — the same flag that shows/hides the sidebar's
    // own AI Gateway entry — on top of the module permission check. Read
    // directly (synchronous, no hook needed) by each caller.
    requiresUiFeature: 'llmGateway',
  },
  {
    id: 'egress-filter',
    fragment: 'egress-filter',
    text: 'Egress Filter',
    icon: ShieldOutlinedIcon,
    Body: EgressFilterAdminTab,
    module: 'egressfilter',
  },
  {
    id: 'budgets-limits',
    fragment: 'budgets-limits',
    text: 'Budgets & Limits',
    icon: LLMConsumptionIcon,
    Body: BudgetsAndLimitsAdminTab,
    module: 'ai',
  },
  {
    id: 'memory-policy',
    fragment: 'memory-policy',
    text: 'Memory Policy',
    icon: LockOutlinedIcon,
    Body: MemoryPolicyAdminTab,
    module: 'ai',
  },
  {
    id: 'rca-format',
    fragment: 'rca-format',
    text: 'RCA Format',
    icon: FileOutlineIcon,
    Body: RCAFormatAdminTab,
    module: 'ai',
  },
  {
    id: 'memory',
    fragment: 'memory',
    text: 'Memory',
    icon: InMemoryIcon,
    Body: MemoryLegacyAdminTab,
    legacyOnly: true,
  },
  {
    id: 'account-context',
    fragment: 'account-context',
    text: 'Account Context',
    icon: DocumentationIcon,
    Body: AccountContextAdminTab,
    legacyOnly: true,
  },
];
