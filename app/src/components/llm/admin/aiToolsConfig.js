import { AgentIconTabBlue, ToolsIconTabBlue, LLMConsumptionIcon, FileOutlineIcon, InMemoryIcon, DocumentationIcon } from '@assets';
import SettingsOutlinedIcon from '@mui/icons-material/SettingsOutlined';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import ShieldOutlinedIcon from '@mui/icons-material/ShieldOutlined';
import LockOutlinedIcon from '@mui/icons-material/LockOutlined';
import SpeedOutlinedIcon from '@mui/icons-material/SpeedOutlined';
import withBrandBlue from '@shared/icons/withBrandBlue';
import { missingPermissionMessage } from '@lib/auth';
import AgentsAdminTab from './AgentsAdminTab';
import ToolsAndMCPAdminTab from './ToolsAndMCPAdminTab';
import ProvidersAdminTab from './ProvidersAdminTab';
import GatewayAdminTab from './GatewayAdminTab';
import SecurityAdminTab from './SecurityAdminTab';
import BudgetsAndLimitsAdminTab from './BudgetsAndLimitsAdminTab';
import MemoryPolicyAdminTab from './MemoryPolicyAdminTab';
import InvestigationDepthAdminTab from './InvestigationDepthAdminTab';
import RCAFormatAdminTab from './RCAFormatAdminTab';
import MemoryLegacyAdminTab from './MemoryLegacyAdminTab';
import AccountContextAdminTab from './AccountContextAdminTab';

// MUI icons default to `currentColor` and would otherwise render in the
// dropdown's gray/black text color instead of matching the hardcoded blue
// of their custom-SVG siblings (Agents, Tools & MCP, …) in the same list.
const ProvidersIcon = withBrandBlue(SettingsOutlinedIcon);
const GatewayIcon = withBrandBlue(HubOutlinedIcon);
const SecurityIcon = withBrandBlue(ShieldOutlinedIcon);
const MemoryPolicyIcon = withBrandBlue(LockOutlinedIcon);
const InvestigationDepthIcon = withBrandBlue(SpeedOutlinedIcon);

/**
 * AI & Tools sub-tabs (docs/ia-consolidation-plan.md, PR 3) — the single
 * source of truth for both mounts the "mirrors" rule calls for: Admin's own
 * page (pages/user-management/index.jsx, via AnchorComponent's tabOptions)
 * and the Nubi rail's compact modal (AIToolsModal.jsx). Same shape Access &
 * Users' tabOptions uses on the Admin page — `module` drives per-tab
 * dynamic-RBAC gating in both places.
 *
 * `modules` lists the dynamic-RBAC modules whose Read grant (or Write, which
 * implies Read) opens the tab: the modules its own actions classify to in
 * permissionCatalog.ts. AI actions are split by refineAiModule into ai_agents,
 * ai_tools, ai_budget, ai_models, ai_rca, ai_memory, ai_guardrails, … — there is
 * no `ai` module, so gating on it hid these tabs from every custom role.
 * Tools & MCP opens on any of its sub-views' modules (Tools: ai_tools, MCP
 * Servers: integrations). Providers reuses `integrations`; Security
 * `egressfilter` (its Tool Approvals subsection is shown only to tenant-wide
 * admin roles and keeps separate enforcement in the RPC and handler).
 *
 * `adminOnly: true` marks a tab no grant can open: Gateway (every llm_gateway_*
 * config action is non-grantable) and Memory Policy (tenant memory consent stays
 * a tenant-admin decision).
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
    icon: AgentIconTabBlue,
    Body: AgentsAdminTab,
    modules: ['ai_agents'],
  },
  {
    id: 'tools-mcp',
    fragment: 'tools-mcp',
    text: 'Tools & MCP',
    icon: ToolsIconTabBlue,
    Body: ToolsAndMCPAdminTab,
    modules: ['ai_tools', 'integrations'],
  },
  {
    id: 'providers',
    fragment: 'providers',
    text: 'Providers',
    icon: ProvidersIcon,
    Body: ProvidersAdminTab,
    modules: ['integrations'],
  },
  {
    id: 'gateway',
    fragment: 'gateway',
    text: 'Gateway',
    icon: GatewayIcon,
    Body: GatewayAdminTab,
    adminOnly: true,
    // Settings additionally gated this on the llmGateway UI feature toggle
    // (isUiFeatureEnabled) — the same flag that shows/hides the sidebar's
    // own AI Gateway entry — on top of the module permission check. Read
    // directly (synchronous, no hook needed) by each caller.
    requiresUiFeature: 'llmGateway',
  },
  {
    id: 'security',
    fragment: 'egress-filter',
    text: 'Security',
    icon: SecurityIcon,
    Body: SecurityAdminTab,
    modules: ['egressfilter'],
    // EE-only body (app/src/ee/), stripped on OSS — callers drop the tab there. enterprise#37063.
    eeOnly: true,
  },
  {
    id: 'budgets-limits',
    fragment: 'budgets-limits',
    text: 'Budgets & Limits',
    icon: LLMConsumptionIcon,
    Body: BudgetsAndLimitsAdminTab,
    modules: ['ai_budget', 'ai_models'],
  },
  {
    id: 'memory-policy',
    fragment: 'memory-policy',
    text: 'Memory Policy',
    icon: MemoryPolicyIcon,
    Body: MemoryPolicyAdminTab,
    adminOnly: true,
    // EE-only body (PrivacyTab, app/src/ee/), stripped on OSS — callers drop the tab there, as for Egress Filter.
    eeOnly: true,
  },
  {
    id: 'investigation-depth',
    fragment: 'investigation-depth',
    text: 'Investigation Depth',
    icon: InvestigationDepthIcon,
    Body: InvestigationDepthAdminTab,
    module: 'ai',
    // EE-only body (app/src/ee/), stripped on OSS — callers drop the tab there, as for Egress Filter.
    eeOnly: true,
  },
  {
    id: 'rca-format',
    fragment: 'rca-format',
    text: 'RCA Format',
    icon: FileOutlineIcon,
    Body: RCAFormatAdminTab,
    modules: ['ai_rca'],
  },
  {
    id: 'memory',
    fragment: 'memory',
    text: 'Memory',
    icon: InMemoryIcon,
    Body: MemoryLegacyAdminTab,
    legacyOnly: true,
    // open to every built-in role, as before they had a module; a custom role
    // needs the module
    openToBuiltInRoles: true,
    modules: ['ai_memory'],
  },
  {
    id: 'account-context',
    fragment: 'account-context',
    text: 'Account Context',
    icon: DocumentationIcon,
    Body: AccountContextAdminTab,
    legacyOnly: true,
    // open to every built-in role, as before they had a module; a custom role
    // needs the module
    openToBuiltInRoles: true,
    modules: ['ai_guardrails'],
  },
];

// subTabAccess decides whether a sub-tab is enabled for the viewer: tenant-wide
// admins see every tab; anyone else needs Read (or Write) on one of its modules
// (`modules`, or a single `module` as the Access & Users tabs declare it), and
// never an adminOnly tab. An `openToBuiltInRoles` tab opens for any built-in
// role too. Shared by the Admin page and the Nubi modal so the
// two show the same tabs.
export function subTabAccess(tab, { isAdmin, permissions = [], hasBuiltInRole = false }) {
  if (isAdmin || (tab.openToBuiltInRoles && hasBuiltInRole)) {
    return { disabled: false };
  }
  if (tab.adminOnly) {
    return { disabled: true, tooltip: 'Only a tenant admin can open this.' };
  }
  const modules = tab.modules ?? (tab.module ? [tab.module] : []);
  if (modules.length === 0 || modules.some((m) => permissions.includes(`${m}:Read`) || permissions.includes(`${m}:Write`))) {
    return { disabled: false };
  }
  return { disabled: true, tooltip: missingPermissionMessage(`${modules[0]}:Read`) };
}
