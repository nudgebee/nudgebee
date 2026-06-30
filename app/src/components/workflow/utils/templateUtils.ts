import type { Node, Edge } from 'reactflow';
import { sanitizeTaskId } from './taskUtils';

export interface PreviousTask {
  id: string;
  name: string;
  type: string;
  outputSchema?: any;
}

/**
 * Extract previous tasks that come before the selected node in the workflow
 */
export const getPreviousTasksForNode = (selectedNodeId: string, nodes: Node[], edges: Edge[], taskDefinitions: any[]): PreviousTask[] => {
  const previousTasks: PreviousTask[] = [];

  // Find all edges that target the selected node
  // Recursively find all nodes that can reach the selected node
  const findPredecessors = (nodeId: string, visited: Set<string> = new Set()): string[] => {
    if (visited.has(nodeId)) {
      return [];
    }
    visited.add(nodeId);

    const predecessorEdges = edges.filter((edge) => edge.target === nodeId);
    const predecessors: string[] = [];

    predecessorEdges.forEach((edge) => {
      predecessors.push(edge.source);
      predecessors.push(...findPredecessors(edge.source, visited));
    });

    return predecessors;
  };

  const predecessorIds = findPredecessors(selectedNodeId);
  const uniquePredecessorIds = Array.from(new Set(predecessorIds));

  // Convert node IDs to task information
  uniquePredecessorIds.forEach((nodeId) => {
    const node = nodes.find((n) => n.id === nodeId);
    if (node && node.type === 'action' && node.data.taskConfig) {
      const taskConfig = node.data.taskConfig;
      const taskDefinition = taskDefinitions.find((def) => def.name === taskConfig.type);

      previousTasks.push({
        id: taskConfig.id || node.id,
        name: node.data.label || taskConfig.type,
        type: taskConfig.type,
        outputSchema: taskDefinition?.output_schema || {},
      });
    }
  });

  // Sort by execution order (tasks closer to the selected node come first)
  return previousTasks.reverse();
};

/** Direct child node IDs reached via outgoing edges from a switch's case/default handles. */
export const getSwitchChildNodeIds = (nodeId: string, edges: Edge[]): string[] =>
  edges.filter((e) => e.source === nodeId && (e.sourceHandle?.startsWith('switch-') ?? false)).map((e) => e.target);

/**
 * Walk up switch-handle edges to find this node's chain of switch ancestor node IDs (outermost first).
 * Used to reconstruct the renamed runtime task ID, which the executor builds as
 * `sanitize(outer)-...-sanitize(immediateSwitch)-sanitize(thisTask)` for tasks executed inline by a switch.
 */
export const getSwitchAncestorChain = (nodeId: string, nodes: Node[], edges: Edge[]): string[] => {
  const chain: string[] = [];
  const visited = new Set<string>();
  let current = nodeId;
  // eslint-disable-next-line no-constant-condition
  while (true) {
    if (visited.has(current)) break;
    visited.add(current);
    const incoming = edges.find((e) => e.target === current && (e.sourceHandle?.startsWith('switch-') ?? false));
    if (!incoming) break;
    const parent = nodes.find((n) => n.id === incoming.source);
    if (!parent || parent.type !== 'switch') break;
    chain.push(parent.id);
    current = parent.id;
  }
  return chain.reverse();
};

/**
 * Resolve the execution-trace task entry for a workflow editor node. Handles the three cases:
 *  1. Direct match by `taskConfig.id` / sanitized node id.
 *  2. Task renamed by an ancestor switch (`sanitize(outer)-...-sanitize(this)`).
 *  3. Switch nodes themselves: not emitted in the trace; mirror the matched child's entry
 *     (since the switch either routed or not — its user-facing status tracks its child's).
 * Returns both the resolved entry and the runtime ID used so callers can reconcile ghost tasks.
 */
export const findExecutionTaskForNode = <T extends { id?: string }>(
  node: Node,
  nodes: Node[],
  edges: Edge[],
  taskMap: Map<string, T>
): { task: T; matchedId: string } | undefined => {
  const baseId = node.data?.taskConfig?.id || sanitizeTaskId(node.id);
  const direct = taskMap.get(baseId);
  if (direct) return { task: direct, matchedId: baseId };

  const ancestors = getSwitchAncestorChain(node.id, nodes, edges);
  const runtimeId = ancestors.length === 0 ? baseId : [...ancestors.map(sanitizeTaskId), baseId].join('-');
  if (runtimeId !== baseId) {
    const renamed = taskMap.get(runtimeId);
    if (renamed) return { task: renamed, matchedId: runtimeId };
  }

  if (node.type === 'switch') {
    const prefix = runtimeId + '-';
    for (const entry of Array.from(taskMap.entries())) {
      const [id, task] = entry;
      if (id.startsWith(prefix)) return { task, matchedId: id };
    }
  }

  return undefined;
};

/**
 * After task node(s) have been deleted, blank any switch-case `next` / `default_next` entries
 * that still point at the deleted task ids. Returns the same array reference when nothing
 * changes, so callers passing this through `setNodes` don't trigger unnecessary rerenders.
 * Kept separate from the ReactFlow delete primitive because the three delete paths
 * (onNodesChange, ActionNode handler, SwitchNode handler) each bypass one another.
 */
export const cleanupSwitchReferencesAfterDelete = (nodes: Node[], deletedTaskIds: Set<string>): Node[] => {
  if (deletedTaskIds.size === 0) return nodes;
  let changed = false;
  const next = nodes.map((n) => {
    if (n.type !== 'switch' || !n.data?.taskConfig?.config) return n;
    const config = n.data.taskConfig.config;
    const cases = Array.isArray(config.cases) ? config.cases : [];
    const newCases = cases.map((c: any) => (c?.next && deletedTaskIds.has(c.next) ? { ...c, next: '' } : c));
    const casesChanged = newCases.some((c: any, i: number) => c !== cases[i]);
    const defaultChanged = !!config.default_next && deletedTaskIds.has(config.default_next);
    if (!casesChanged && !defaultChanged) return n;
    changed = true;
    return {
      ...n,
      data: {
        ...n.data,
        taskConfig: {
          ...n.data.taskConfig,
          config: {
            ...config,
            cases: casesChanged ? newCases : cases,
            default_next: defaultChanged ? '' : config.default_next,
          },
        },
      },
    };
  });
  return changed ? next : nodes;
};

/**
 * After a task is renamed, rewrite references to its old id across the workflow:
 *   - `Tasks['<oldId>']` / `Tasks["<oldId>"]` in config / if / set_vars / set_state.
 *   - Switch `cases[].next` / `default_next` that point at the old id.
 * Matches are case-exact (task ids are case-sensitive). The renamed node itself
 * is skipped so a self-referential template (rare, but valid) isn't double-renamed
 * after node.id + taskConfig.id were already updated by the caller.
 */
export const renameTaskReferencesInNodes = (nodes: Node[], oldId: string, newId: string, renamedNodeId: string): Node[] => {
  if (!oldId || !newId || oldId === newId) return nodes;
  const escaped = oldId.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const taskRefRe = new RegExp(`Tasks\\[(['"])${escaped}\\1\\]`, 'g');
  const replaceInString = (s: string): string => s.replace(taskRefRe, (_m, quote) => `Tasks[${quote}${newId}${quote}]`);
  // Recursive traversal avoids the JSON.stringify escape trap where double-quoted
  // `Tasks["old"]` becomes `Tasks[\"old\"]` and the single-char-quote regex misses it.
  // Also preserves non-JSON values (undefined, Date, etc.) and reference identity
  // for unchanged subtrees.
  const replaceInValue = (val: any): any => {
    if (val == null) return val;
    if (typeof val === 'string') return replaceInString(val);
    if (Array.isArray(val)) {
      let arrChanged = false;
      const next = val.map((item) => {
        const updated = replaceInValue(item);
        if (updated !== item) arrChanged = true;
        return updated;
      });
      return arrChanged ? next : val;
    }
    if (typeof val === 'object') {
      const proto = Object.getPrototypeOf(val);
      if (proto !== null && proto !== Object.prototype) return val;
      let objChanged = false;
      const next: Record<string, any> = {};
      for (const key of Object.keys(val)) {
        const item = val[key];
        const updated = replaceInValue(item);
        if (updated !== item) objChanged = true;
        next[key] = updated;
      }
      return objChanged ? next : val;
    }
    return val;
  };

  let changed = false;
  const newNodes = nodes.map((n) => {
    if (n.id === renamedNodeId) return n;
    if (n.type !== 'action' && n.type !== 'switch') return n;
    const tc = n.data?.taskConfig;
    if (!tc) return n;

    const newConfig = tc.config != null ? replaceInValue(tc.config) : tc.config;
    const newIf = typeof tc.if === 'string' ? replaceInString(tc.if) : tc.if;
    const newSetVars = tc.set_vars != null ? replaceInValue(tc.set_vars) : tc.set_vars;
    const newSetState = tc.set_state != null ? replaceInValue(tc.set_state) : tc.set_state;

    let switchConfig = newConfig;
    let switchChanged = false;
    if (n.type === 'switch' && newConfig && typeof newConfig === 'object') {
      const cases = Array.isArray(newConfig.cases) ? newConfig.cases : [];
      const updatedCases = cases.map((c: any) => (c?.next === oldId ? { ...c, next: newId } : c));
      const casesChanged = updatedCases.some((c: any, i: number) => c !== cases[i]);
      const defaultChanged = newConfig.default_next === oldId;
      if (casesChanged || defaultChanged) {
        switchConfig = {
          ...newConfig,
          cases: casesChanged ? updatedCases : cases,
          default_next: defaultChanged ? newId : newConfig.default_next,
        };
        switchChanged = true;
      }
    }

    const tcChanged = switchConfig !== tc.config || newIf !== tc.if || newSetVars !== tc.set_vars || newSetState !== tc.set_state || switchChanged;
    if (!tcChanged) return n;
    changed = true;
    return {
      ...n,
      data: {
        ...n.data,
        taskConfig: {
          ...tc,
          config: switchConfig,
          if: newIf,
          set_vars: newSetVars,
          set_state: newSetState,
        },
      },
    };
  });
  return changed ? newNodes : nodes;
};

/** Eligibility + reason + child IDs for dry-running a switch node up to its direct children. */
export const getSwitchDryRunEligibility = (
  nodeId: string,
  nodes: Node[],
  edges: Edge[]
): { allowed: boolean; reason: string; childNodeIds: string[] } => {
  const childNodeIds = getSwitchChildNodeIds(nodeId, edges);
  if (childNodeIds.length === 0) {
    return { allowed: false, reason: 'Connect at least one child to dry-run a switch', childNodeIds: [] };
  }
  const hasSwitchChild = childNodeIds.some((cid) => nodes.find((n) => n.id === cid)?.type === 'switch');
  if (hasSwitchChild) {
    return { allowed: false, reason: 'Dry run is not supported when a child is another switch', childNodeIds };
  }
  return { allowed: true, reason: '', childNodeIds };
};

/**
 * Validate a template expression
 */
export const validateTemplateExpression = (expression: string, previousTasks: PreviousTask[]): { isValid: boolean; error?: string } => {
  const templatePattern = /\{\{\s*Tasks\[['"]([^'"]*)['"]\]\.output\.([^}\s]*)\s*\}\}/;
  const match = templatePattern.exec(expression);

  if (!match) {
    return { isValid: false, error: 'Invalid template syntax' };
  }

  const [, taskId, fieldPath] = match;

  // Check if task exists
  const task = previousTasks.find((t) => t.id === taskId);
  if (!task) {
    return { isValid: false, error: `Task '${taskId}' not found in previous tasks` };
  }

  // Check if field exists in output schema
  if (task.outputSchema && Object.keys(task.outputSchema).length > 0) {
    const fieldExists = fieldPath.split('.').reduce((schema, field) => {
      return schema?.[field];
    }, task.outputSchema);

    if (!fieldExists) {
      return { isValid: false, error: `Field '${fieldPath}' not found in ${task.name} output schema` };
    }
  }

  return { isValid: true };
};

/**
 * Extract all template expressions from a text
 */
export const extractTemplateExpressions = (text: string): string[] => {
  const templatePattern = /\{\{\s*Tasks\[['"]([^'"]*)['"]\]\.output\.([^}\s]*)\s*\}\}/g;
  const expressions = [];
  let match;

  while ((match = templatePattern.exec(text)) !== null) {
    expressions.push(match[0]);
  }

  return expressions;
};

/**
 * Generate example template expressions for documentation
 */
export const generateTemplateExamples = (previousTasks: PreviousTask[]): string[] => {
  const examples: string[] = [];

  previousTasks.slice(0, 3).forEach((task) => {
    if (task.outputSchema && Object.keys(task.outputSchema).length > 0) {
      const firstField = Object.keys(task.outputSchema)[0];
      examples.push(`{{ Tasks['${task.id}'].output.${firstField} }}`);
    } else {
      examples.push(`{{ Tasks['${task.id}'].output.data }}`);
    }
  });

  return examples;
};
