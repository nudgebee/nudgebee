import type { WorkflowData } from '@components1/workflow/types';

/**
 * Type guard to check if a parsed object is a valid WorkflowData structure
 * Supports two formats:
 * 1. Standard format: { name, definition: { tasks: [], triggers: [] } }
 * 2. Flat format from LLM: { name, tasks: [], trigger: {} }
 */
export const isWorkflowJson = (json: unknown): json is WorkflowData => {
  if (!json || typeof json !== 'object') {
    return false;
  }
  const obj = json as any;

  // Check for standard format with nested definition
  const hasStandardFormat =
    typeof obj.name === 'string' &&
    obj.definition &&
    typeof obj.definition === 'object' &&
    Array.isArray(obj.definition.tasks) &&
    Array.isArray(obj.definition.triggers);

  // Check for flat format (LLM may return tasks/trigger at root level)
  const hasFlatFormat = typeof obj.name === 'string' && Array.isArray(obj.tasks) && (obj.trigger || obj.triggers); // LLM may return singular 'trigger' or plural 'triggers'

  return hasStandardFormat || hasFlatFormat;
};

/**
 * Check if response is from workflow builder based on chain_name
 */
export const isFromWorkflowBuilder = (response: any): boolean => {
  return response?.chain_name === 'workflow_builder' || response?.chain_name === 'workflow_generation';
};

/**
 * Detect and validate workflow JSON from LLM response
 * Uses structure validation as primary check, chain_name as optional hint
 *
 * @param responseText - Raw response text from LLM
 * @param chainName - Chain name from response metadata (optional)
 * @returns Formatted JSON string if valid workflow, null otherwise
 */
export const detectWorkflowJson = (responseText: string | undefined, chainName?: string): string | null => {
  if (!responseText) {
    return null;
  }

  // Primary check: validate JSON structure
  try {
    const parsed = JSON.parse(responseText);
    if (isWorkflowJson(parsed)) {
      return responseText;
    }
  } catch {
    // Not valid JSON, continue to chain_name check
  }

  // If structure validation fails, fallback to chain_name check
  // This handles edge cases where structure might be slightly different
  if (isFromWorkflowBuilder({ chain_name: chainName })) {
    try {
      JSON.parse(responseText); // Still needs to be valid JSON
      return responseText;
    } catch {
      return null;
    }
  }

  return null;
};
