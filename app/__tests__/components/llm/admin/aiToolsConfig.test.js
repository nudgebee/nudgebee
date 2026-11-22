import { classifyAction } from '@lib/permissionCatalog';
import { AI_TOOLS_SUB_TABS, subTabAccess } from '@components/llm/admin/aiToolsConfig';
import { AI_TOOLS_MODULES } from '@components/llm/admin/aiToolsModules';

jest.mock('@lib/auth', () => ({ missingPermissionMessage: (p) => `need ${p}` }));

const tab = (id) => AI_TOOLS_SUB_TABS.find((t) => t.id === id);
const asUser = (permissions) => ({ isAdmin: false, permissions });

// The AI & Tools tabs were gated on an `ai` module no action classifies to, so
// no custom role ever saw them. Each now names the modules its actions use.
describe('AI & Tools sub-tab gating', () => {
  it('names only modules some action classifies to', () => {
    const known = new Set([
      'ai_agents',
      'ai_tools',
      'ai_budget',
      'ai_models',
      'ai_rca',
      'ai_memory',
      'ai_guardrails',
      'integrations',
      'egressfilter',
    ]);
    for (const t of AI_TOOLS_SUB_TABS) {
      for (const m of t.modules ?? []) {
        expect(known.has(m)).toBe(true);
      }
    }
    expect(classifyAction('ai_list_agents')?.module).toBe('ai_agents');
    expect(classifyAction('ai_list_tools')?.module).toBe('ai_tools');
    expect(classifyAction('ai_list_budget_config')?.module).toBe('ai_budget');
    expect(classifyAction('ai_list_model_pricing')?.module).toBe('ai_models');
    expect(classifyAction('ai_get_rcaformat')?.module).toBe('ai_rca');
    expect(classifyAction('ai_list_memory')?.module).toBe('ai_memory');
    expect(classifyAction('ai_list_gc')?.module).toBe('ai_guardrails');
    expect(AI_TOOLS_SUB_TABS.some((t) => t.module === 'ai' || t.modules?.includes('ai'))).toBe(false);
  });

  it('keeps the sidebar / Admin-surface module list equal to the tabs', () => {
    const fromTabs = [...new Set(AI_TOOLS_SUB_TABS.flatMap((t) => t.modules ?? []))].sort();
    expect([...AI_TOOLS_MODULES].sort()).toEqual(fromTabs);
  });

  it('opens a tab on Read or Write of any of its modules', () => {
    expect(subTabAccess(tab('agents'), asUser(['ai_agents:Read'])).disabled).toBe(false);
    expect(subTabAccess(tab('agents'), asUser(['ai_agents:Write'])).disabled).toBe(false);
    expect(subTabAccess(tab('agents'), asUser(['ai_tools:Read'])).disabled).toBe(true);
    expect(subTabAccess(tab('tools-mcp'), asUser(['integrations:Read'])).disabled).toBe(false);
    expect(subTabAccess(tab('budgets-limits'), asUser(['ai_models:Read'])).disabled).toBe(false);
    expect(subTabAccess(tab('agents'), asUser([])).tooltip).toBe('need ai_agents:Read');
  });

  it('keeps Gateway and Memory Policy for tenant admins', () => {
    for (const id of ['gateway', 'memory-policy']) {
      expect(subTabAccess(tab(id), asUser(['llm:Read', 'ai_memory:Write'])).disabled).toBe(true);
      expect(subTabAccess(tab(id), { isAdmin: true, permissions: [] }).disabled).toBe(false);
    }
  });

  it('gates a tab declaring a single module (Access & Users) the same way', () => {
    expect(subTabAccess({ module: 'users' }, asUser(['users:Read'])).disabled).toBe(false);
    expect(subTabAccess({ module: 'users' }, asUser([])).disabled).toBe(true);
    expect(subTabAccess({}, asUser([])).disabled).toBe(false);
  });

  it('keeps the legacy Memory and Account Context tabs open to built-in roles', () => {
    for (const id of ['memory', 'account-context']) {
      expect(subTabAccess(tab(id), { isAdmin: false, permissions: [], hasBuiltInRole: true }).disabled).toBe(false);
      expect(subTabAccess(tab(id), { isAdmin: false, permissions: [], hasBuiltInRole: false }).disabled).toBe(true);
    }
    expect(subTabAccess(tab('memory'), { isAdmin: false, permissions: ['ai_memory:Read'] }).disabled).toBe(false);
    expect(subTabAccess(tab('agents'), { isAdmin: false, permissions: [], hasBuiltInRole: true }).disabled).toBe(true);
  });

  it('treats missing permissions (session still loading) as none', () => {
    expect(subTabAccess(tab('agents'), { isAdmin: false }).disabled).toBe(true);
  });
});
