import type {
  WorkflowDefinition,
  WorkflowDefinitionTrigger,
  WorkflowRequest,
  WorkflowCreateRequest,
  WorkflowUpdateRequest,
  WorkflowViewport,
} from '@api1/workflow/types';
import type { WorkflowSettings } from '@components1/workflow/types';

/**
 * Creates a workflow definition object from nodes, edges, and settings
 */
export const createWorkflowDefinition = (
  tasks: any[],
  triggers: WorkflowDefinitionTrigger[],
  workflowSettings: WorkflowSettings,
  existingDefinition?: any,
  viewport?: WorkflowViewport
): WorkflowDefinition => {
  const triggersToUse: WorkflowDefinitionTrigger[] = triggers.length > 0 ? triggers : [{ type: 'manual', params: {} }];

  const definition: WorkflowDefinition = {
    version: existingDefinition?.version !== '1.0' ? existingDefinition?.version || 'v1' : 'v1',
    timeout: workflowSettings.timeout,
    inputs: workflowSettings.inputs,
    output: workflowSettings.outputs,
    tasks: tasks,
    triggers: triggersToUse,
    retry_policy: {
      maximum_attempts: workflowSettings.retries,
      initial_interval: '1s',
      maximum_interval: workflowSettings.maxInterval,
      backoff_coefficient: 2.0,
    },
  };

  if (viewport && Number.isFinite(viewport.x) && Number.isFinite(viewport.y) && Number.isFinite(viewport.zoom)) {
    definition.layout = {
      viewport: {
        x: viewport.x,
        y: viewport.y,
        zoom: viewport.zoom,
      },
    };
  }

  return definition;
};

/**
 * Creates a workflow request object (common between create and update).
 * `aiSessionId` is stamped onto `created_from_session_id` so workflows saved
 * via the UI after an AI chat preserve the originating conversation —
 * matching the LLM agent's auto-save path (agent_workflow_builder.go:1109).
 * The runbook server ignores this field on update; pass it only on create.
 */
export const createWorkflowRequest = (
  name: string,
  definition: WorkflowDefinition,
  workflowSettings: WorkflowSettings,
  aiSessionId?: string
): WorkflowRequest => {
  return {
    name: name,
    definition: definition,
    tags: workflowSettings.tags.reduce((acc, tag) => {
      // Parse tags that contain colon to separate key and value
      const colonIndex = tag.indexOf(':');
      if (colonIndex > 0) {
        const key = tag.substring(0, colonIndex).trim();
        const value = tag.substring(colonIndex + 1).trim();
        return { ...acc, [key]: value };
      }
      return { ...acc, [tag]: '' };
    }, {} as Record<string, any>),
    status: workflowSettings.status,
    ...(aiSessionId ? { created_from_session_id: aiSessionId } : {}),
  };
};

/**
 * Creates a complete create workflow request
 */
export const createWorkflowCreateRequest = (
  accountId: string,
  name: string,
  definition: WorkflowDefinition,
  workflowSettings: WorkflowSettings,
  aiSessionId?: string
): WorkflowCreateRequest => {
  const workflowRequest = createWorkflowRequest(name, definition, workflowSettings, aiSessionId);

  return {
    account_id: accountId,
    workflow: workflowRequest,
  };
};

/**
 * Creates a complete update workflow request
 */
export const createWorkflowUpdateRequest = (
  accountId: string,
  workflowId: string,
  name: string,
  definition: WorkflowDefinition,
  workflowSettings: WorkflowSettings
): WorkflowUpdateRequest => {
  const workflowRequest = createWorkflowRequest(name, definition, workflowSettings);

  return {
    account_id: accountId,
    id: workflowId,
    workflow: workflowRequest,
  };
};

/**
 * Extracts and prepares workflow data for API calls
 * Combines task extraction, trigger extraction, and workflow definition creation
 */
export const prepareWorkflowForSave = (
  nodes: any[],
  edges: any[],
  extractTasksFromWorkflowNodes: (nodes: any[]) => any[],
  extractTriggersFromNodes: (nodes: any[]) => WorkflowDefinitionTrigger[],
  workflowSettings: WorkflowSettings,
  existingDefinition?: any,
  viewport?: WorkflowViewport
) => {
  // Extract tasks and triggers from nodes
  const tasks = extractTasksFromWorkflowNodes(nodes);
  const triggersFromNodes = extractTriggersFromNodes(nodes);

  // Create workflow definition
  const workflowDefinition = createWorkflowDefinition(tasks, triggersFromNodes, workflowSettings, existingDefinition, viewport);

  return {
    tasks,
    triggers: triggersFromNodes,
    definition: workflowDefinition,
  };
};
