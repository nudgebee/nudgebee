import type { Node, Edge } from 'reactflow';

// Id prefix of the trigger → root-task edges the canvas derives on its own.
const AUTO_TRIGGER_EDGE_ID_PREFIX = 'trigger-edge-';

const isTaskNode = (node: Node | undefined): boolean => node?.type === 'action' || node?.type === 'switch';

export const buildTriggerEdge = (triggerNodeId: string, targetNodeId: string): Edge => ({
  id: `${AUTO_TRIGGER_EDGE_ID_PREFIX}${triggerNodeId}-${targetNodeId}`,
  source: triggerNodeId,
  target: targetNodeId,
  type: 'smoothstep',
  style: { stroke: 'var(--ds-gray-400)', strokeWidth: 1 },
});

/**
 * Keeps the canvas honest about which steps start with the automation.
 *
 * A task with no task/switch predecessor is saved without `depends_on`, and the
 * engine runs every such task as soon as a trigger fires. The layout engine
 * draws those tasks attached to each trigger on load; this applies the same
 * rule to a canvas that is being edited, so a step never sits unconnected
 * while it would still run (deleting its incoming edge, deleting the step
 * above it, adding or duplicating a step, ...).
 *
 * - Adds a trigger → task edge for every trigger × enabled root task that has none.
 * - Removes every trigger edge whose target is not a root (it has a task
 *   predecessor or is disabled), however it was drawn: a trigger edge is never
 *   saved, so one into a step that waits for another step would only mislead.
 *
 * Disabled tasks stay detached, as the layout engine leaves them. Returns the
 * same array when nothing changes, so it is safe to feed straight to setState.
 */
export const reconcileTriggerEdges = (nodes: Node[], edges: Edge[]): Edge[] => {
  const nodeById = new Map(nodes.map((node) => [node.id, node]));
  const triggerIds = nodes.filter((node) => node.type === 'trigger').map((node) => node.id);

  const targetsWithTaskParent = new Set<string>();
  edges.forEach((edge) => {
    if (isTaskNode(nodeById.get(edge.source))) targetsWithTaskParent.add(edge.target);
  });

  const rootIds = nodes
    .filter((node) => isTaskNode(node) && node.data?.taskConfig?.disabled !== true && !targetsWithTaskParent.has(node.id))
    .map((node) => node.id);
  const rootIdSet = new Set(rootIds);

  const kept = edges.filter((edge) => nodeById.get(edge.source)?.type !== 'trigger' || rootIdSet.has(edge.target));

  const connected = new Set(kept.map((edge) => `${edge.source}->${edge.target}`));
  const usedIds = new Set(kept.map((edge) => edge.id));
  const added: Edge[] = [];
  triggerIds.forEach((triggerId) => {
    rootIds.forEach((rootId) => {
      if (connected.has(`${triggerId}->${rootId}`)) return;
      const edge = buildTriggerEdge(triggerId, rootId);
      // A renamed node keeps its old edge id, so the derived id can already be taken.
      for (let n = 1; usedIds.has(edge.id); n++) {
        edge.id = `${AUTO_TRIGGER_EDGE_ID_PREFIX}${triggerId}-${rootId}-${n}`;
      }
      usedIds.add(edge.id);
      added.push(edge);
    });
  });

  if (added.length === 0 && kept.length === edges.length) return edges;
  return [...kept, ...added];
};
