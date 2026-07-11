import type { Node, Edge } from 'reactflow';
import { sanitizeTaskId } from './taskUtils';

export interface UpstreamSuggestedValuesResult {
  isComplexExpression: boolean;
  taskId?: string;
  taskName?: string;
  taskType?: string;
  outputField?: string;
  values?: string[];
}

/**
 * Parses a switch task's expression string to extract referenced upstream task outputs.
 * If the expression references exactly one upstream task whose output has a known finite
 * set of values (e.g. core.approval -> status), returns those suggested values.
 * If the expression references multiple tasks or complex multi-task outputs, flags it as complex.
 */
export const getUpstreamSuggestedValues = (expression: string, nodes: Node[], _edges?: Edge[]): UpstreamSuggestedValuesResult | null => {
  if (!expression || typeof expression !== 'string') {
    return null;
  }

  // Regex matches Tasks['id'].output.field or Tasks["id"].output.field
  const taskRefRegex = /Tasks\[['"]([^'"]+)['"]\]\.output\.([a-zA-Z0-9_]+)/g;
  const matches = Array.from(expression.matchAll(taskRefRegex));

  if (matches.length === 0) {
    return null;
  }

  // Suggestions are only sound when the expression is *solely* one task ref
  // Strip all matched {{ Tasks['id'].output.field (| filter)? }} blocks;
  // if any non-whitespace remains (literal prefix/suffix text, other namespaces
  // like {{ Inputs.env }}, or multiple distinct task refs), treat as complex.
  const fullTemplateBlockRegex = /\{\{\s*Tasks\[['"][^'"]+['"]\]\.output\.[a-zA-Z0-9_]+(?:\s*\|\s*\w+)*\s*\}\}/g;
  const stripped = expression.replace(fullTemplateBlockRegex, '').trim();
  if (stripped.length > 0 || matches.length > 1) {
    return { isComplexExpression: true };
  }

  const [, taskId, outputField] = matches[0];

  // Locate the referenced upstream task in nodes
  const matchingNode = nodes.find((n) => {
    if (n.type !== 'action' && n.type !== 'switch') return false;
    const configId = n.data?.taskConfig?.id;
    if (configId === taskId) return true;
    if (n.id === taskId) return true;
    if (sanitizeTaskId(n.id) === taskId) return true;
    return false;
  });

  if (!matchingNode || !matchingNode.data?.taskConfig) {
    return null;
  }

  const taskConfig = matchingNode.data.taskConfig;
  const taskType = taskConfig.type;
  const taskName = matchingNode.data.label || taskConfig.id || taskId;

  let rawValues: string[] | null = null;

  // Map known task types to their finite output values
  if (taskType === 'core.approval' && outputField === 'status') {
    const configuredOptions = taskConfig.config?.approval_options;
    if (Array.isArray(configuredOptions) && configuredOptions.length > 0) {
      rawValues = configuredOptions.map((o: any) => String(o)).filter((o: string) => o.trim().length > 0);
    } else {
      rawValues = ['approve', 'reject'];
    }
  }

  if (!rawValues || rawValues.length === 0) {
    return null;
  }

  // Apply basic filter transformations if piped in the expression (e.g. | lower, | upper)
  let values = [...rawValues];
  if (/\|\s*lower\b/.test(expression)) {
    values = values.map((v) => v.toLowerCase());
  } else if (/\|\s*upper\b/.test(expression)) {
    values = values.map((v) => v.toUpperCase());
  }

  // Ensure unique values
  values = Array.from(new Set(values));

  return {
    isComplexExpression: false,
    taskId,
    taskName,
    taskType,
    outputField,
    values,
  };
};
