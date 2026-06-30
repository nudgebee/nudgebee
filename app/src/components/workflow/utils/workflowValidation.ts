import type { Node } from 'reactflow';

interface WorkflowData {
  id: string | null;
  name: string;
  definition: any;
  tags: Record<string, any>;
}

/**
 * Validates a workflow before saving or execution
 * Returns array of validation error messages
 */
export const validateWorkflowForSave = (
  workflowDataObject: WorkflowData | null,
  nodes: Node[],
  extractTasksFromWorkflowNodes: (nodes: Node[]) => any[]
): string[] => {
  const errors: string[] = [];

  if (!workflowDataObject?.name?.trim()) {
    errors.push('Automation name is required');
  }

  const tasks = extractTasksFromWorkflowNodes(nodes);
  if (tasks.length === 0) {
    errors.push('At least one task is required');
  }

  // Check for invalid task configurations
  const invalidNodes = nodes.filter((node) => node.type === 'action' && node.data.taskConfig && !node.data.taskConfig.valid);
  if (invalidNodes.length > 0) {
    errors.push(`${invalidNodes.length} task(s) have validation errors`);
  }

  return errors;
};

export interface TaskOutputReference {
  nodeId: string;
  taskId: string;
  label: string;
}

/**
 * Scans every action/switch node in the workflow for `Tasks['<targetTaskId>']`
 * template references in its params, if-condition, set_vars, and set_state.
 * Returns the list of nodes whose output (or behavior) depends on the target
 * task running. Self-references are excluded.
 */
export const findTaskOutputReferences = (targetTaskId: string, nodes: Node[]): TaskOutputReference[] => {
  if (!targetTaskId) return [];
  const escaped = targetTaskId.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const re = new RegExp(`Tasks\\[['"]${escaped}['"]\\]`);
  const refs: TaskOutputReference[] = [];

  for (const node of nodes) {
    if (node.type !== 'action' && node.type !== 'switch') continue;
    const tc = node.data?.taskConfig;
    if (!tc) continue;

    const ownId: string = tc.id || node.id;
    if (ownId === targetTaskId) continue;

    const sources: string[] = [];
    if (tc.config != null) sources.push(JSON.stringify(tc.config));
    if (typeof tc.if === 'string' && tc.if) sources.push(tc.if);
    if (tc.set_vars != null) sources.push(typeof tc.set_vars === 'string' ? tc.set_vars : JSON.stringify(tc.set_vars));
    if (tc.set_state != null) sources.push(typeof tc.set_state === 'string' ? tc.set_state : JSON.stringify(tc.set_state));

    if (sources.some((s) => re.test(s))) {
      refs.push({
        nodeId: node.id,
        taskId: ownId,
        label: node.data?.label || ownId,
      });
    }
  }

  return refs;
};

/**
 * Checks if connecting two nodes would create a cycle in the workflow graph
 * Uses DFS to detect cycles in the directed graph
 */
export const wouldCreateCycle = (sourceId: string, targetId: string, edges: any[]): boolean => {
  // Create adjacency list including the potential new edge
  const adjacencyList: { [key: string]: string[] } = {};

  // Add all existing edges to adjacency list
  edges.forEach((edge) => {
    if (!adjacencyList[edge.source]) {
      adjacencyList[edge.source] = [];
    }
    adjacencyList[edge.source].push(edge.target);
  });

  // Add the potential new edge
  if (!adjacencyList[sourceId]) {
    adjacencyList[sourceId] = [];
  }
  adjacencyList[sourceId].push(targetId);

  // DFS to detect cycles starting from target node
  const visited = new Set<string>();
  const recursionStack = new Set<string>();

  const dfs = (nodeId: string): boolean => {
    if (recursionStack.has(nodeId)) {
      return true; // Cycle detected
    }
    if (visited.has(nodeId)) {
      return false; // Already processed this path
    }

    visited.add(nodeId);
    recursionStack.add(nodeId);

    const neighbors = adjacencyList[nodeId] || [];
    for (const neighbor of neighbors) {
      if (dfs(neighbor)) {
        return true;
      }
    }

    recursionStack.delete(nodeId);
    return false;
  };

  return dfs(targetId);
};
