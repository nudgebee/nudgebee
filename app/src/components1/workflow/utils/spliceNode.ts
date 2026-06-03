import type { Edge } from 'reactflow';

const handleKey = (edge: Edge): string => `${edge.source}::${edge.sourceHandle ?? ''}->${edge.target}::${edge.targetHandle ?? ''}`;

const makeReconnectedEdge = (predecessor: Edge, successor: Edge): Edge => ({
  ...predecessor,
  id: `reactflow-edge-${predecessor.source}-${successor.target}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`,
  source: predecessor.source,
  target: successor.target,
  sourceHandle: predecessor.sourceHandle,
  targetHandle: successor.targetHandle,
  selected: false,
});

/**
 * Remove all edges touching `nodeId`, and splice the surrounding nodes back together
 * when the deleted node sits in a linear chain (1-in/1-out) or a fan-in (N-in/1-out).
 *
 * Fan-out (1-in/M-out) and N-in/M-out are intentionally left disconnected — any bridge
 * would silently drop the routing decision the deleted node was making (e.g. a Switch).
 */
export const spliceEdgesOnNodeDelete = (nodeId: string, edges: Edge[]): Edge[] => {
  const inEdges = edges.filter((e) => e.target === nodeId && e.source !== nodeId);
  const outEdges = edges.filter((e) => e.source === nodeId && e.target !== nodeId);
  const remaining = edges.filter((e) => e.source !== nodeId && e.target !== nodeId);

  const shouldSplice = outEdges.length === 1 && inEdges.length >= 1;
  if (!shouldSplice) {
    return remaining;
  }

  const successor = outEdges[0];
  const existingKeys = new Set(remaining.map(handleKey));
  const newEdges: Edge[] = [];

  for (const predecessor of inEdges) {
    const candidate = makeReconnectedEdge(predecessor, successor);
    const key = handleKey(candidate);
    if (existingKeys.has(key)) continue;
    existingKeys.add(key);
    newEdges.push(candidate);
  }

  return [...remaining, ...newEdges];
};
