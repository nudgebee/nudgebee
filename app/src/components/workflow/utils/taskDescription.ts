import type { TaskDefinition } from '@components/workflow/types';

const truncate = (text: string, maxLen: number): string => (text.length > maxLen ? `${text.substring(0, maxLen)}...` : text);

const providerLabels: Record<string, string> = {
  slack: 'Slack',
  ms_teams: 'MS Teams',
  google_chat: 'Google Chat',
};

const enrichTaskDescription = (taskType: string, params: any, baseDescription: string): string | null => {
  switch (taskType) {
    case 'data.transform':
      return params?.expression ? `Transform: ${truncate(params.expression, 40)}` : null;
    case 'scripting.run_script':
      return params?.script ? `Script: ${truncate(params.script.split('\n')[0], 30)}` : null;
    case 'integrations.http':
      return params?.url ? `HTTP: ${params.url}` : null;
    case 'notifications.im':
      if (!params?.provider) return null;
      const label = Object.prototype.hasOwnProperty.call(providerLabels, params.provider)
        ? providerLabels[params.provider]
        : String(params.provider).toUpperCase();
      return `${label} notification`;
    case 'tickets.create':
      return params?.title ? `Ticket: ${truncate(params.title, 30)}` : null;
    case 'llm.summary':
    case 'llm.investigate':
      return params?.message ? `${baseDescription}: ${truncate(params.message, 30)}` : null;
    default:
      return null;
  }
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
  const enrichedDescription = enrichTaskDescription(taskType, params, description);
  if (enrichedDescription) {
    return enrichedDescription;
  }

  // CLI tasks share a common enrichment pattern
  if (taskType.includes('cli')) {
    return enrichCliDescription(params, description) || description;
  }

  return description;
};
