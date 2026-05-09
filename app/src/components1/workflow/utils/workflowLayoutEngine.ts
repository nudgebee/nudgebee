import type { Node, Edge } from 'reactflow';
import { getTaskDescription } from './index';
import { validateTaskData } from '@components1/workflow/hooks/useTaskValidation';
import { parseConditionLabel, hasConditionalStyling } from './conditionParser';
import { createTaskLabel } from '@components1/workflow/constants/nodeCategories';

interface LayoutConfig {
  minHorizontalSpacing?: number;
  minVerticalSpacing?: number;
  minTriggerSpacing?: number;
  minConditionalSpacing?: number;
}

interface ViewportInfo {
  viewport: { x: number; y: number; zoom: number };
  bounds: { minX: number; maxX: number; minY: number; maxY: number; width: number; height: number };
}

interface WorkflowConversionResult {
  nodes: Node[];
  edges: Edge[];
  viewport: { x: number; y: number; zoom: number };
  bounds: { minX: number; maxX: number; minY: number; maxY: number; width: number; height: number };
}

/**
 * Calculates node layers using topological sort for workflow dependency analysis
 * Includes circular dependency detection
 */
export const calculateNodeLayers = (tasks: any[]): Map<string, number> => {
  const layers = new Map<string, number>();
  const visited = new Set<string>();
  const visiting = new Set<string>(); // Track nodes currently being processed
  const taskMap = new Map<string, any>();

  // Create task lookup map
  tasks.forEach((task: any) => {
    taskMap.set(task.id, task);
  });

  const calculateLayer = (taskId: string, path: string[] = []): number => {
    // Check if we've already calculated this layer
    if (visited.has(taskId)) {
      return layers.get(taskId) || 0;
    }

    // Circular dependency detection
    if (visiting.has(taskId)) {
      const cycle = [...path, taskId].join(' -> ');
      console.error(`Circular dependency detected: ${cycle}`);
      // Break the cycle by treating this as a root task
      layers.set(taskId, 0);
      visited.add(taskId);
      return 0;
    }

    visiting.add(taskId);
    const task = taskMap.get(taskId);

    if (!task) {
      console.warn(`Task "${taskId}" not found in task map`);
      layers.set(taskId, 0);
      visited.add(taskId);
      visiting.delete(taskId);
      return 0;
    }

    if (!task.depends_on || task.depends_on.length === 0) {
      // Root task - layer 0
      layers.set(taskId, 0);
      visited.add(taskId);
      visiting.delete(taskId);
      return 0;
    }

    // Calculate layer based on maximum dependency layer + 1
    let maxDependencyLayer = -1;
    task.depends_on.forEach((depId: string) => {
      const depLayer = calculateLayer(depId, [...path, taskId]);
      maxDependencyLayer = Math.max(maxDependencyLayer, depLayer);
    });

    const taskLayer = maxDependencyLayer + 1;
    layers.set(taskId, taskLayer);
    visited.add(taskId);
    visiting.delete(taskId);
    return taskLayer;
  };

  tasks.forEach((task) => {
    if (!visited.has(task.id)) {
      calculateLayer(task.id, []);
    }
  });

  return layers;
};

/**
 * Minimizes edge crossings using barycentric heuristic for better visual layout
 */
export const minimizeEdgeCrossings = (layers: Map<number, string[]>, taskMap: Map<string, any>): Map<number, string[]> => {
  const optimizedLayers = new Map(layers);
  const maxLayer = Math.max(...layers.keys());

  // Iterate multiple times for better optimization
  for (let iteration = 0; iteration < 3; iteration++) {
    // Forward pass: optimize based on previous layer
    for (let layerNum = 1; layerNum <= maxLayer; layerNum++) {
      const currentLayer = optimizedLayers.get(layerNum);
      if (!currentLayer || currentLayer.length <= 1) {
        continue;
      }

      // Calculate barycentric values for each node
      const barycenters = currentLayer.map((taskId) => {
        const task = taskMap.get(taskId);
        if (!task?.depends_on || task.depends_on.length === 0) {
          return { taskId, barycenter: 0 };
        }

        const prevLayer = optimizedLayers.get(layerNum - 1) || [];
        let positionSum = 0;
        let dependencyCount = 0;

        task.depends_on.forEach((depId: string) => {
          const depIndex = prevLayer.indexOf(depId);
          if (depIndex >= 0) {
            positionSum += depIndex;
            dependencyCount++;
          }
        });

        return {
          taskId,
          barycenter: dependencyCount > 0 ? positionSum / dependencyCount : 0,
        };
      });

      // Sort by barycenter to minimize crossings
      barycenters.sort((a, b) => a.barycenter - b.barycenter);
      optimizedLayers.set(
        layerNum,
        barycenters.map((item) => item.taskId)
      );
    }

    // Backward pass: optimize based on next layer
    for (let layerNum = maxLayer - 1; layerNum >= 0; layerNum--) {
      const currentLayer = optimizedLayers.get(layerNum);
      if (!currentLayer || currentLayer.length <= 1) {
        continue;
      }

      const barycenters = currentLayer.map((taskId) => {
        const nextLayer = optimizedLayers.get(layerNum + 1) || [];
        let positionSum = 0;
        let dependentCount = 0;

        // Find tasks in next layer that depend on this task
        nextLayer.forEach((nextTaskId, index) => {
          const nextTask = taskMap.get(nextTaskId);
          if (nextTask?.depends_on?.includes(taskId)) {
            positionSum += index;
            dependentCount++;
          }
        });

        return {
          taskId,
          barycenter: dependentCount > 0 ? positionSum / dependentCount : currentLayer.indexOf(taskId),
        };
      });

      barycenters.sort((a, b) => a.barycenter - b.barycenter);
      optimizedLayers.set(
        layerNum,
        barycenters.map((item) => item.taskId)
      );
    }
  }

  return optimizedLayers;
};

/**
 * Calculates optimal viewport settings for the workflow canvas
 */
export const calculateViewport = (nodes: Node[]): ViewportInfo => {
  if (nodes.length === 0) {
    return {
      viewport: { x: 0, y: 0, zoom: 1 },
      bounds: { minX: 0, maxX: 0, minY: 0, maxY: 0, width: 0, height: 0 },
    };
  }

  // Calculate bounding box of all nodes
  const nodeWidth = 280; // Approximate node width
  const nodeHeight = 120; // Approximate node height
  const padding = 50; // Viewport padding

  let minX = Infinity,
    maxX = -Infinity;
  let minY = Infinity,
    maxY = -Infinity;

  nodes.forEach((node) => {
    const x = node.position.x;
    const y = node.position.y;

    minX = Math.min(minX, x - nodeWidth / 2);
    maxX = Math.max(maxX, x + nodeWidth / 2);
    minY = Math.min(minY, y - nodeHeight / 2);
    maxY = Math.max(maxY, y + nodeHeight / 2);
  });

  // Calculate content dimensions
  const contentWidth = maxX - minX;
  const contentHeight = maxY - minY;

  // Viewport dimensions accounting for sidebar (400px) and header (60px)
  // Typical screen width: 1920px - 400px sidebar = 1520px available
  // Using more conservative estimate for better centering
  const viewportWidth = 1400;
  const viewportHeight = 700;

  // Calculate optimal zoom to fit content with padding
  const zoomX = (viewportWidth - 2 * padding) / Math.max(contentWidth, 1);
  const zoomY = (viewportHeight - 2 * padding) / Math.max(contentHeight, 1);
  const optimalZoom = Math.min(Math.min(zoomX, zoomY), 0.9);
  const finalZoom = Math.max(optimalZoom, 0.75); // Readable zoom floor — tall workflows scroll instead of shrinking below this

  // Calculate center position
  const centerX = (minX + maxX) / 2;
  const centerY = (minY + maxY) / 2;

  // Calculate viewport position to center content
  const viewportX = viewportWidth / 2 - centerX * finalZoom;
  const viewportY = viewportHeight / 2 - centerY * finalZoom;

  return {
    viewport: {
      x: viewportX,
      y: viewportY,
      zoom: finalZoom,
    },
    bounds: {
      minX,
      maxX,
      minY,
      maxY,
      width: contentWidth,
      height: contentHeight,
    },
  };
};

const TRIGGER_START_X = 400;
const TRIGGER_NODE_WIDTH = 280;
const TRIGGER_GAP = 100;
const TRIGGER_SPACING = TRIGGER_NODE_WIDTH + TRIGGER_GAP;
const TRIGGER_DEFAULT_Y = 100;

const getTriggerLabelAndDescription = (trigger: any): { label: string; description: string } => {
  switch (trigger.type) {
    case 'manual':
      return { label: 'Manual Trigger', description: 'Manually triggered automation' };
    case 'webhook':
      return {
        label: trigger.params?.integration_name ? `Webhook: ${trigger.params.integration_name}` : 'Webhook Trigger',
        description: trigger.params?.integration_name ? `Webhook integration: ${trigger.params.integration_name}` : 'Webhook-triggered automation',
      };
    case 'schedule':
      return {
        label: 'Scheduled Trigger',
        description: trigger.params?.cron ? `Scheduled: ${trigger.params.cron}` : 'Time-based scheduled automation',
      };
    case 'event':
      return {
        label: 'Event Trigger',
        description: trigger.params?.event_type ? `Event: ${trigger.params.event_type}` : 'Event-driven automation',
      };
    case 'optimization':
      return {
        label: 'Optimization Trigger',
        description:
          Array.isArray(trigger.params?.categories) && trigger.params.categories.length > 0
            ? `Optimization: ${trigger.params.categories.join(', ')}`
            : 'Triggered by optimization recommendations',
      };
    default:
      return {
        label: `${trigger.type.charAt(0).toUpperCase() + trigger.type.slice(1)} Trigger`,
        description: `${trigger.type}-based workflow trigger`,
      };
  }
};

const computeTriggerPosition = (trigger: any, index: number, total: number): { x: number; y: number } => {
  if (trigger.layout && Number.isFinite(trigger.layout.x) && Number.isFinite(trigger.layout.y)) {
    return { x: trigger.layout.x, y: trigger.layout.y };
  }
  if (total === 1) {
    return { x: TRIGGER_START_X, y: TRIGGER_DEFAULT_Y };
  }
  const spanWidth = (total - 1) * TRIGGER_SPACING;
  const startX = TRIGGER_START_X - spanWidth / 2;
  return { x: startX + index * TRIGGER_SPACING, y: TRIGGER_DEFAULT_Y };
};

const buildTriggerToRootEdges = (triggerNodeId: string, rootTasks: any[], taskIdToNodeId: Map<string, string>): Edge[] => {
  const edges: Edge[] = [];
  rootTasks.forEach((task: any) => {
    const targetNodeId = taskIdToNodeId.get(task.id);
    if (!targetNodeId) return;
    edges.push({
      id: `trigger-edge-${triggerNodeId}-${targetNodeId}`,
      source: triggerNodeId,
      target: targetNodeId,
      type: 'smoothstep',
      style: { stroke: 'rgb(192, 192, 192)', strokeWidth: 1 },
    });
  });
  return edges;
};

/**
 * Creates trigger nodes for workflow definition
 */
export const createTriggerNodes = (triggers: any[], rootTasks: any[], taskIdToNodeId: Map<string, string>): { nodes: Node[]; edges: Edge[] } => {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  triggers.forEach((trigger: any, index: number) => {
    const triggerNodeId = `trigger-${trigger.type}-${index}`;
    const { label, description } = getTriggerLabelAndDescription(trigger);
    const position = computeTriggerPosition(trigger, index, triggers.length);

    nodes.push({
      id: triggerNodeId,
      type: 'trigger',
      position,
      data: {
        label,
        description,
        trigger,
        triggerType: trigger.type,
        triggerParams: trigger.params,
      },
    });

    edges.push(...buildTriggerToRootEdges(triggerNodeId, rootTasks, taskIdToNodeId));
  });

  return { nodes, edges };
};

/**
 * Converts workflow definition to React Flow format with optimized layout
 */
export const convertWorkflowToReactFlow = (definition: any, layoutConfig: LayoutConfig = {}, taskDefinitions?: any[]): WorkflowConversionResult => {
  const nodes: Node[] = [];
  const edges: Edge[] = [];

  // Validate input
  if (!definition) {
    console.error('convertWorkflowToReactFlow: definition is null or undefined');
    return {
      nodes,
      edges,
      viewport: { x: 0, y: 0, zoom: 1 },
      bounds: { minX: 0, maxX: 0, minY: 0, maxY: 0, width: 0, height: 0 },
    };
  }

  if (!definition.tasks || !Array.isArray(definition.tasks)) {
    console.warn('convertWorkflowToReactFlow: definition.tasks is missing or not an array');
    // Still allow processing triggers even if there are no tasks
    definition.tasks = [];
  }

  // Handle empty tasks array
  if (definition.tasks.length === 0) {
    // Still create trigger nodes if they exist
    if (definition.triggers && Array.isArray(definition.triggers) && definition.triggers.length > 0) {
      const { nodes: triggerNodes } = createTriggerNodes(definition.triggers, [], new Map());
      nodes.push(...triggerNodes);
    }

    const { viewport, bounds } = calculateViewport(nodes);
    return {
      nodes,
      edges,
      viewport,
      bounds,
    };
  }

  // Validate and filter tasks
  const validTasks = definition.tasks.filter((task: any) => {
    if (!task || typeof task !== 'object') {
      console.warn('convertWorkflowToReactFlow: Skipping invalid task (not an object):', task);
      return false;
    }
    if (!task.id) {
      console.warn('convertWorkflowToReactFlow: Skipping task without id:', task);
      return false;
    }
    if (!task.type) {
      console.warn('convertWorkflowToReactFlow: Skipping task without type:', task);
      return false;
    }
    return true;
  });

  if (validTasks.length === 0) {
    console.warn('convertWorkflowToReactFlow: No valid tasks after filtering');

    // Still create trigger nodes if they exist
    if (definition.triggers && Array.isArray(definition.triggers) && definition.triggers.length > 0) {
      const { nodes: triggerNodes } = createTriggerNodes(definition.triggers, [], new Map());
      nodes.push(...triggerNodes);
    }

    const { viewport, bounds } = calculateViewport(nodes);
    return {
      nodes,
      edges,
      viewport,
      bounds,
    };
  }

  // Normalize depends_on field: convert string to array if needed
  validTasks.forEach((task: any) => {
    if (task.depends_on) {
      // If depends_on is a string, convert it to an array
      if (typeof task.depends_on === 'string') {
        task.depends_on = [task.depends_on];
      }
      // If depends_on is not an array and not a string, make it an empty array
      else if (!Array.isArray(task.depends_on)) {
        task.depends_on = [];
      }
    }
  });

  // Create a map of task IDs to task objects for dependency analysis
  const taskMap = new Map<string, any>();
  validTasks.forEach((task: any) => {
    taskMap.set(task.id, task);
  });

  // Inject synthetic dependencies: switch case targets depend on the switch node for layout.
  // Track these so we don't create duplicate generic edges (switch-case edges handle them).
  const syntheticSwitchDeps = new Set<string>(); // "sourceTaskId->targetTaskId"
  validTasks.forEach((task: any) => {
    if (task.type === 'core.switch' && task.params) {
      const caseTargets: string[] = [];
      if (Array.isArray(task.params.cases)) {
        task.params.cases.forEach((c: any) => {
          if (c.next && taskMap.has(c.next)) caseTargets.push(c.next);
        });
      }
      if (task.params.default_next && taskMap.has(task.params.default_next)) {
        caseTargets.push(task.params.default_next);
      }
      caseTargets.forEach((targetId) => {
        const targetTask = taskMap.get(targetId);
        if (targetTask) {
          if (!targetTask.depends_on) targetTask.depends_on = [];
          if (!targetTask.depends_on.includes(task.id)) {
            targetTask.depends_on.push(task.id);
          }
          syntheticSwitchDeps.add(`${task.id}->${targetId}`);
        }
      });
    }
  });

  // Calculate layers for all valid tasks
  const taskLayers = calculateNodeLayers(validTasks);

  // Group tasks by layer and detect conditional branches
  const layerGroups = new Map<number, string[]>();
  const conditionalTasks = new Set<string>();

  // Identify conditional tasks
  validTasks.forEach((task: any) => {
    if (task.if) {
      conditionalTasks.add(task.id);
    }
  });

  taskLayers.forEach((layer, taskId) => {
    if (!layerGroups.has(layer)) {
      layerGroups.set(layer, []);
    }
    layerGroups.get(layer)!.push(taskId);
  });

  // Apply edge crossing minimization
  const optimizedLayerGroups = minimizeEdgeCrossings(layerGroups, taskMap);

  // Layout configuration with defaults and overrides
  const LAYER_HEIGHT = layoutConfig?.minVerticalSpacing ?? 180;
  const SWITCH_EXTRA_GAP = 60; // Extra vertical gap after switch nodes (taller due to case footer)
  const ACTUAL_NODE_WIDTH = 280; // Actual rendered node width
  const HORIZONTAL_GAP = 80; // Gap between nodes
  const NODE_SPACING = ACTUAL_NODE_WIDTH + HORIZONTAL_GAP; // Total horizontal spacing (360px)
  const CONDITIONAL_SPREAD = layoutConfig?.minConditionalSpacing ?? 150;
  const START_X = 400;
  const START_Y = 280;

  // Identify layers that contain a switch node (next layer needs extra gap)
  const switchLayers = new Set<number>();
  validTasks.forEach((task: any) => {
    if (task.type === 'core.switch') {
      const layer = taskLayers.get(task.id);
      if (layer !== undefined) switchLayers.add(layer);
    }
  });

  // Compute cumulative Y offset per layer, adding extra gap after switch layers
  const layerYOffset = new Map<number, number>();
  const allLayers = [...new Set(taskLayers.values())].sort((a, b) => a - b);
  let cumulativeY = START_Y;
  allLayers.forEach((layer, i) => {
    layerYOffset.set(layer, cumulativeY);
    // Add extra gap if this layer has a switch node
    const gap = LAYER_HEIGHT + (switchLayers.has(layer) ? SWITCH_EXTRA_GAP : 0);
    if (i < allLayers.length - 1) cumulativeY += gap;
  });

  // Create nodes with calculated positions
  const taskIdToNodeId = new Map<string, string>();

  validTasks.forEach((task: any) => {
    const nodeId = task.id || `task-${Math.random()}`;
    taskIdToNodeId.set(task.id, nodeId);

    const layer = taskLayers.get(task.id) || 0;
    const layerTasks = optimizedLayerGroups.get(layer) || [];
    const indexInLayer = layerTasks.indexOf(task.id);
    const totalInLayer = layerTasks.length;

    let nodeX, nodeY;
    const baseY = layerYOffset.get(layer) ?? START_Y + layer * LAYER_HEIGHT;

    // Determine spacing for this layer: use wider spacing if any task has a condition
    const layerHasConditionals = layerTasks.some((taskId) => conditionalTasks.has(taskId));
    const effectiveSpacing = layerHasConditionals ? NODE_SPACING + CONDITIONAL_SPREAD : NODE_SPACING;

    // Position all tasks in the layer uniformly to prevent overlap
    const totalWidth = (totalInLayer - 1) * effectiveSpacing;
    const layerStartX = START_X - totalWidth / 2;
    nodeX = layerStartX + indexInLayer * effectiveSpacing;
    nodeY = baseY;

    // Persisted layout overrides auto-layout when present.
    if (task.layout && Number.isFinite(task.layout.x) && Number.isFinite(task.layout.y)) {
      nodeX = task.layout.x;
      nodeY = task.layout.y;
    }

    const node: Node = {
      id: nodeId,
      type: task.type === 'core.switch' ? 'switch' : 'action',
      position: {
        x: nodeX,
        y: nodeY,
      },
      data: {
        description: getTaskDescription(task.type, task.params, taskDefinitions),
        label: createTaskLabel(task.type),
        taskConfig: {
          type: task.type,
          config: task.params || {},
          ...(() => {
            // Only validate if taskDefinitions are provided
            if (taskDefinitions && Array.isArray(taskDefinitions) && taskDefinitions.length > 0) {
              const validation = validateTaskData(task.type, task.params || {}, taskDefinitions);
              return {
                valid: validation.isValid,
                errors: validation.errors,
              };
            }
            // Default to valid if no task definitions available
            return {
              valid: true,
              errors: {},
            };
          })(),
          id: task.id,
          depends_on: task.depends_on?.filter((depId: string) => !syntheticSwitchDeps.has(`${depId}->${task.id}`)),
          if: task.if,
          timeout: task.timeout,
          set_state: typeof task.set_state === 'string' ? undefined : task.set_state,
          set_vars: typeof task.set_vars === 'string' ? undefined : task.set_vars,
          matrix: typeof task.matrix === 'string' ? undefined : task.matrix,
          hooks: typeof task.hooks === 'string' ? undefined : task.hooks,
          failure_policy: typeof task.failure_policy === 'string' ? undefined : task.failure_policy,
          disabled: task.disabled === true ? true : undefined,
          // Pass through both stash shapes (flat array or { originals, splices? });
          // toggleTaskDisable.unpackStash handles both.
          _prev_edges: Array.isArray(task._prev_edges) || (task._prev_edges && typeof task._prev_edges === 'object') ? task._prev_edges : undefined,
        },
      },
    };

    nodes.push(node);
  });

  // Create edges based on task dependencies
  validTasks.forEach((task: any) => {
    if (task.depends_on && Array.isArray(task.depends_on)) {
      task.depends_on.forEach((dependencyId: string) => {
        // Skip synthetic switch deps — these are handled by switch-case edges below
        if (syntheticSwitchDeps.has(`${dependencyId}->${task.id}`)) return;

        const sourceNodeId = taskIdToNodeId.get(dependencyId);
        const targetNodeId = taskIdToNodeId.get(task.id);

        if (sourceNodeId && targetNodeId) {
          // Check if the target task has a condition (if/else)
          const hasCondition = hasConditionalStyling(task.if);
          const parsedCondition = hasCondition ? parseConditionLabel(task.if) : null;

          const edge: Edge = {
            id: `edge-${sourceNodeId}-${targetNodeId}`,
            source: sourceNodeId,
            target: targetNodeId,
            type: hasCondition ? 'conditional' : 'smoothstep',
            ...(hasCondition && parsedCondition
              ? {
                  data: {
                    ...parsedCondition,
                    condition: task.if,
                  },
                }
              : {}),
            style: {
              stroke: hasCondition && parsedCondition ? parsedCondition.color : 'rgb(192, 192, 192)',
              strokeWidth: hasCondition ? 2 : 1,
            },
          };

          edges.push(edge);
        } else if (!sourceNodeId) {
          // Warn about missing dependency
          console.warn(`convertWorkflowToReactFlow: Missing source node for dependency "${dependencyId}" in task "${task.id}"`);
        }
      });
    }
  });

  // Create edges for switch case connections
  validTasks.forEach((task: any) => {
    if (task.type === 'core.switch' && task.params) {
      const switchNodeId = taskIdToNodeId.get(task.id);
      if (!switchNodeId) return;

      if (Array.isArray(task.params.cases)) {
        task.params.cases.forEach((c: any) => {
          if (!c.next) return;
          const targetNodeId = taskIdToNodeId.get(c.next);
          if (!targetNodeId) return;
          edges.push({
            id: `switch-edge-${switchNodeId}-${c.value}-${targetNodeId}`,
            source: switchNodeId,
            target: targetNodeId,
            sourceHandle: `switch-case-${c.value}`,
            targetHandle: 'action-input',
            type: 'smoothstep',
            label: c.value,
            style: { stroke: '#8B5CF6', strokeWidth: 2 },
          });
        });
      }

      if (task.params.default_next) {
        const targetNodeId = taskIdToNodeId.get(task.params.default_next);
        if (targetNodeId) {
          edges.push({
            id: `switch-edge-${switchNodeId}-default-${targetNodeId}`,
            source: switchNodeId,
            target: targetNodeId,
            sourceHandle: 'switch-default',
            targetHandle: 'action-input',
            type: 'smoothstep',
            label: 'default',
            style: { stroke: '#9CA3AF', strokeWidth: 2, strokeDasharray: '5 5' },
          });
        }
      }
    }
  });

  // If there are triggers, create trigger nodes
  if (definition.triggers && Array.isArray(definition.triggers) && definition.triggers.length > 0) {
    // Root tasks are tasks with no dependencies. Disabled tasks are intentionally
    // disconnected from the DAG, so the trigger should not auto-connect to them.
    const rootTasks = validTasks.filter((task: any) => (!task.depends_on || task.depends_on.length === 0) && !task.disabled);
    const { nodes: triggerNodes, edges: triggerEdges } = createTriggerNodes(definition.triggers, rootTasks, taskIdToNodeId);

    nodes.unshift(...triggerNodes);
    edges.push(...triggerEdges);
  }

  // Calculate viewport and bounds
  const { viewport, bounds } = calculateViewport(nodes);

  return {
    nodes,
    edges,
    viewport,
    bounds,
  };
};
