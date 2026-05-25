import type { Node, Edge } from 'reactflow';
import { sanitizeTaskId } from './index';
import type { WorkflowDefinitionTask, WorkflowDefinitionTrigger, WorkflowTaskLayout } from '@api1/workflow/types';

const extractNodeLayout = (node: Node): WorkflowTaskLayout | undefined => {
  if (!node.position || !Number.isFinite(node.position.x) || !Number.isFinite(node.position.y)) {
    return undefined;
  }
  return { x: Math.round(node.position.x), y: Math.round(node.position.y) };
};

/**
 * Extracts triggers from trigger nodes for workflow definition
 */
export const extractTriggersFromNodes = (workflowNodes: Node[]): WorkflowDefinitionTrigger[] => {
  const triggerNodes = workflowNodes.filter((node) => node.type === 'trigger');
  const validTriggerNodes = triggerNodes.filter((node) => node.data.trigger);

  return validTriggerNodes.map((node) => {
    const trigger: WorkflowDefinitionTrigger = {
      type: node.data.trigger.type,
      params: node.data.trigger.params || {},
    };
    const layout = extractNodeLayout(node);
    if (layout) trigger.layout = layout;
    return trigger;
  });
};

// Optional task-config fields that ride along verbatim into the saved task
// when truthy. Driving these from a list keeps the per-node mapper free of a
// long if-ladder.
const OPTIONAL_TASK_FIELDS: Array<keyof WorkflowDefinitionTask> = ['if', 'timeout', 'set_state', 'set_vars', 'matrix', 'hooks', 'failure_policy'];

// Apply switch-only param normalization in place: keep only {value, next} on
// each case (dropping legacy fields), prefer the edge-derived target, and fall
// back to a stored `next` only when it still points at a live task. Default-
// next is set from edges or cleared if it points at a deleted task.
const applySwitchParams = (
  params: any,
  edgeData: { cases: Map<string, string>; defaultNext: string } | undefined,
  validTaskIds: Set<string>
): void => {
  if (Array.isArray(params.cases)) {
    params.cases = params.cases.map((c: any) => {
      const edgeTarget = edgeData?.cases.get(c.value);
      const storedNext = c.next && validTaskIds.has(c.next) ? c.next : '';
      return { value: c.value || '', next: edgeTarget || storedNext };
    });
  }
  if (edgeData?.defaultNext) {
    params.default_next = edgeData.defaultNext;
  } else if (params.default_next && !validTaskIds.has(params.default_next)) {
    params.default_next = '';
  }
  delete params.default;
};

// Filter `dependencyMap` entries for a switch case target so the parent switch
// is not also listed in the child task's depends_on (the routing happens via
// switch params instead).
const filterSwitchSyntheticDeps = (
  rawDeps: string[],
  sanitizedId: string,
  workflowNodes: Node[],
  switchCaseEdges: Map<string, { cases: Map<string, string>; defaultNext: string }>
): string[] => {
  return rawDeps.filter((depId) => {
    const switchNode = workflowNodes.find((n) => n.type === 'switch' && sanitizeTaskId(n.id) === depId);
    if (!switchNode) return true;
    const edgeData = switchCaseEdges.get(switchNode.id);
    if (!edgeData) return true;
    const isTarget = [...edgeData.cases.values()].includes(sanitizedId) || edgeData.defaultNext === sanitizedId;
    return !isTarget;
  });
};

/**
 * Extracts tasks from action and switch nodes for workflow definition.
 * Builds dependency map from edges and creates proper task definitions.
 * Switch-case edges (sourceHandle starts with 'switch-') are converted to
 * switch params (cases[].next, default_next) instead of depends_on.
 */
export const extractTasksFromWorkflowNodes = (workflowNodes: Node[], edges: Edge[]): WorkflowDefinitionTask[] => {
  const taskNodeTypes = new Set(['action', 'switch']);

  // Set of sanitized task ids that still exist in the editor — used to drop stale switch-case
  // `next` / `default_next` references whose target task was deleted from the workflow.
  const validTaskIds = new Set(workflowNodes.filter((n) => taskNodeTypes.has(n.type!)).map((n) => sanitizeTaskId(n.id)));

  // Build dependency map and switch-case map from edges
  const dependencyMap = new Map<string, string[]>();
  const switchCaseEdges = new Map<string, { cases: Map<string, string>; defaultNext: string }>();

  // Initialize all task nodes with empty dependencies
  workflowNodes.forEach((node) => {
    if (taskNodeTypes.has(node.type!)) {
      dependencyMap.set(node.id, []);
    }
  });

  // Process edges to build dependencies and switch-case mappings
  edges.forEach((edge) => {
    const sourceNode = workflowNodes.find((node) => node.id === edge.source);
    const targetNode = workflowNodes.find((node) => node.id === edge.target);

    if (!sourceNode || !targetNode) return;
    if (targetNode.type === 'trigger') return;

    // Handle switch-case edges: extract into switch params, not depends_on
    if (sourceNode.type === 'switch' && edge.sourceHandle?.startsWith('switch-')) {
      if (!switchCaseEdges.has(sourceNode.id)) {
        switchCaseEdges.set(sourceNode.id, { cases: new Map(), defaultNext: '' });
      }
      const switchData = switchCaseEdges.get(sourceNode.id)!;
      const targetTaskId = sanitizeTaskId(targetNode.id);

      if (edge.sourceHandle === 'switch-default') {
        switchData.defaultNext = targetTaskId;
      } else {
        // sourceHandle format: 'switch-case-{value}'
        const caseValue = edge.sourceHandle.replace('switch-case-', '');
        switchData.cases.set(caseValue, targetTaskId);
      }
      return;
    }

    // Handle regular task-to-task edges
    if (taskNodeTypes.has(sourceNode.type!) && taskNodeTypes.has(targetNode.type!)) {
      const sourceTaskId = sanitizeTaskId(sourceNode.id);
      const currentDeps = dependencyMap.get(targetNode.id) || [];
      if (!currentDeps.includes(sourceTaskId)) {
        currentDeps.push(sourceTaskId);
        dependencyMap.set(targetNode.id, currentDeps);
      }
    }
  });

  // Extract tasks from action and switch nodes
  return workflowNodes
    .filter((node) => taskNodeTypes.has(node.type!) && node.data.taskConfig)
    .map((node) => {
      const taskConfig = node.data.taskConfig;
      const sanitizedId = sanitizeTaskId(node.id);
      const params = { ...(taskConfig.config || {}) };

      if (node.type === 'switch') {
        applySwitchParams(params, switchCaseEdges.get(node.id), validTaskIds);
      }

      const task: WorkflowDefinitionTask = {
        id: sanitizedId,
        type: taskConfig.type,
        params,
      };

      const dependencies = filterSwitchSyntheticDeps(dependencyMap.get(node.id) || [], sanitizedId, workflowNodes, switchCaseEdges);
      if (dependencies.length > 0) {
        task.depends_on = dependencies;
      }

      OPTIONAL_TASK_FIELDS.forEach((field) => {
        if (taskConfig[field]) {
          (task as any)[field] = taskConfig[field];
        }
      });

      if (taskConfig.disabled) {
        task.disabled = true;
      }

      const layout = extractNodeLayout(node);
      if (layout) task.layout = layout;
      // _prev_edges supports two shapes: a flat StashedEdge[] (legacy, originals
      // only) and `{ originals, splices? }` (current, used by the splice flow).
      // Persist whichever is present, dropping empty stashes either way.
      const prev = taskConfig._prev_edges;
      if (Array.isArray(prev)) {
        if (prev.length > 0) {
          task._prev_edges = prev;
        }
      } else if (prev && typeof prev === 'object') {
        const originals = Array.isArray(prev.originals) ? prev.originals : [];
        const splices = Array.isArray(prev.splices) ? prev.splices : [];
        if (originals.length > 0 || splices.length > 0) {
          task._prev_edges = prev;
        }
      }

      return task;
    });
};

/**
 * Gets output schemas from all nodes that connect to the target node
 * Used for building input schemas for task configuration
 */
export const getPreviousNodesOutputSchemas = (targetNodeId: string, edges: Edge[], nodes: Node[], taskDefinitions: any[]): any => {
  // Find all edges pointing to this node
  const incomingEdges = edges.filter((edge) => edge.target === targetNodeId);

  if (incomingEdges.length === 0) {
    return {};
  }

  // Collect output schemas from all previous nodes
  const combinedOutputSchema: any = {};

  incomingEdges.forEach((edge) => {
    const sourceNode = nodes.find((node) => node.id === edge.source);
    if (!sourceNode) {
      return;
    }

    // Process action and switch nodes, skip trigger nodes
    if ((sourceNode.type === 'action' || sourceNode.type === 'switch') && sourceNode.data.taskConfig) {
      // Find the task definition for this source node
      const sourceTaskType = sourceNode.data.taskConfig.type;
      const sourceTaskDefinition = taskDefinitions.find((def) => def.name === sourceTaskType);

      if (sourceTaskDefinition?.output_schema) {
        // Add prefix to avoid field name conflicts between different source nodes
        const sourceNodePrefix = sourceNode.data.label || sourceNode.id;
        Object.entries(sourceTaskDefinition.output_schema).forEach(([fieldName, fieldSchema]) => {
          // Use the field name directly, but add a comment about the source
          const enrichedFieldSchema = {
            ...(fieldSchema as any),
            description: `From ${sourceNodePrefix}: ${(fieldSchema as any)?.description || ''}`,
          };
          combinedOutputSchema[fieldName] = enrichedFieldSchema;
        });
      }
    }
    // Skip trigger nodes - they don't provide meaningful output schemas for the next action
  });

  return combinedOutputSchema;
};
