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
