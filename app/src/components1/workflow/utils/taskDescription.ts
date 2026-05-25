import type { TaskDefinition } from '@components1/workflow/types';

const truncate = (text: string, maxLen: number): string => (text.length > maxLen ? `${text.substring(0, maxLen)}...` : text);

const providerLabels: Record<string, string> = {
  slack: 'Slack',
  ms_teams: 'MS Teams',
  google_chat: 'Google Chat',
};

type ParamEnricher = (params: any, baseDescription: string) => string | null;

const paramEnrichers: Record<string, ParamEnricher> = {
  'data.transform': (params) => (params?.expression ? `Transform: ${truncate(params.expression, 40)}` : null),
  'scripting.run_script': (params) => (params?.script ? `Script: ${truncate(params.script.split('\n')[0], 30)}` : null),
  'integrations.http': (params) => (params?.url ? `HTTP: ${params.url}` : null),
  'notifications.im': (params) => {
    if (!params?.provider) return null;
    return `${providerLabels[params.provider] || params.provider.toUpperCase()} notification`;
  },
  'tickets.create': (params) => (params?.title ? `Ticket: ${truncate(params.title, 30)}` : null),
  'llm.summary': (params, base) => (params?.message ? `${base}: ${truncate(params.message, 30)}` : null),
  'llm.investigate': (params, base) => (params?.message ? `${base}: ${truncate(params.message, 30)}` : null),
};

const enrichCliDescription = (params: any, baseDescription: string): string | null => {
  if (!params?.command) return null;
  return `${baseDescription}: ${truncate(params.command, 30)}`;
};

// Helper function to get task description based on type, params, and backend task definitions
export const getTaskDescription = (taskType: string, params?: any, taskDefinitions?: TaskDefinition[]): string => {
  // Build base description from backend task definitions
  let description = 'Execute task';
  if (taskDefinitions && taskDefinitions.length > 0) {
    const taskDef = taskDefinitions.find((td) => td.name === taskType);
    if (taskDef) {
      description = taskDef.display_name || taskDef.description;
    }
  }

  // Enrich with params-based context when available
  const enricher = paramEnrichers[taskType];
  if (enricher) {
    return enricher(params, description) || description;
  }

  // CLI tasks share a common enrichment pattern
  if (taskType.includes('cli')) {
    return enrichCliDescription(params, description) || description;
  }

  return description;
};
