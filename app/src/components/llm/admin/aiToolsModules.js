// Every module that opens some AI & Tools sub-tab (the union of the `modules`
// in aiToolsConfig.js; aiToolsModules.test pins the two together). Kept apart
// from aiToolsConfig so the sidebar and the Admin-surface check can use it
// without importing every AI admin tab.
export const AI_TOOLS_MODULES = [
  'ai_agents',
  'ai_tools',
  'integrations',
  'egressfilter',
  'ai_budget',
  'ai_models',
  'ai_rca',
  'ai_memory',
  'ai_guardrails',
];
