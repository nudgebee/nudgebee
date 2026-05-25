import type { Node, Edge } from 'reactflow';

export type StashedEdge = {
  source: string;
  target: string;
  sourceHandle?: string;
  targetHandle?: string;
  type?: string;
};

// Persisted stash for a disabled task. `originals` are the edges that were
// touching the disabled node. `splices` are the synthetic predecessor →
// successor edges that disableTask added so the workflow keeps flowing past
// the muted task. Re-enable removes the splices and restores the originals.
//
// The earlier shape was `StashedEdge[]` (originals only, no splice). We accept
// both shapes on read so workflows saved before splice still re-enable cleanly.
export type PrevEdgesStash = {
  originals: StashedEdge[];
  splices?: StashedEdge[];
};

const TRIGGER_NODE_ID_PREFIX = 'trigger-';

const edgeTouchesNode = (edge: Edge, nodeId: string): boolean => edge.source === nodeId || edge.target === nodeId;

const toStashedEdge = (edge: Edge): StashedEdge => ({
  source: edge.source,
  target: edge.target,
  sourceHandle: edge.sourceHandle ?? undefined,
  targetHandle: edge.targetHandle ?? undefined,
  type: edge.type,
});

const edgeKey = (e: { source: string; sourceHandle?: string | null; target: string; targetHandle?: string | null }): string =>
  `${e.source}::${e.sourceHandle ?? ''}->${e.target}::${e.targetHandle ?? ''}`;

const newEdgeId = (source: string, target: string): string => {
  const tail = typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : String(Date.now());
  return `reactflow-edge-${source}-${target}-${tail}`;
};

const stashedToEdge = (s: StashedEdge): Edge => ({
  id: newEdgeId(s.source, s.target),
  source: s.source,
  target: s.target,
  sourceHandle: s.sourceHandle,
  targetHandle: s.targetHandle,
  type: s.type ?? 'smoothstep',
});

const setTaskConfigOnNode = (node: Node, patch: Record<string, unknown>): Node => ({
  ...node,
  data: {
    ...node.data,
    taskConfig: {
      ...(node.data?.taskConfig || {}),
      ...patch,
    },
  },
});

const unpackStash = (raw: unknown): PrevEdgesStash => {
  if (Array.isArray(raw)) {
    return { originals: raw as StashedEdge[], splices: [] };
  }
  if (raw && typeof raw === 'object') {
    const obj = raw as { originals?: unknown; splices?: unknown };
    return {
      originals: Array.isArray(obj.originals) ? (obj.originals as StashedEdge[]) : [],
      splices: Array.isArray(obj.splices) ? (obj.splices as StashedEdge[]) : [],
    };
  }
  return { originals: [], splices: [] };
};

// After re-enabling a task, drop in-session trigger auto-edges that point at a
// task which now has a non-trigger predecessor. The layout engine would clean
// these up on the next reload, but the visual flicker mid-session is a known
// source of "the re-enabled node attached itself to the trigger" reports.
const scrubRedundantTriggerEdges = (edges: Edge[]): Edge[] => {
  const triggerEdges = edges.filter((e) => e.source.startsWith(TRIGGER_NODE_ID_PREFIX));
  if (triggerEdges.length === 0) return edges;
  const realEdges = edges.filter((e) => !e.source.startsWith(TRIGGER_NODE_ID_PREFIX));
  const targetsWithRealParent = new Set(realEdges.map((e) => e.target));
  const keptTriggerEdges = triggerEdges.filter((te) => !targetsWithRealParent.has(te.target));
  return [...realEdges, ...keptTriggerEdges];
};

export const disableTask = (nodeId: string, nodes: Node[], edges: Edge[]): { nodes: Node[]; edges: Edge[] } => {
  const incoming = edges.filter((e) => e.target === nodeId);
  const outgoing = edges.filter((e) => e.source === nodeId);
  const remaining = edges.filter((e) => !edgeTouchesNode(e, nodeId));

  const originals: StashedEdge[] = [...incoming, ...outgoing].map(toStashedEdge);

  // Splice predecessors → successors so the chain keeps flowing past the
  // muted task. Skip splicing through switch-case edges: the routing on a
  // switch lives in `params.cases[].next`, not depends_on, and inheriting a
  // sourceHandle like `switch-case-foo` onto a new edge would silently rewire
  // the wrong case. The executor's existing skip for `Disabled` covers the
  // "switch case target is disabled" path on its own.
  const splices: StashedEdge[] = [];
  const splicedEdges: Edge[] = [];
  const existingKeys = new Set(remaining.map(edgeKey));

  for (const inc of incoming) {
    if (typeof inc.sourceHandle === 'string' && inc.sourceHandle.startsWith('switch-')) continue;
    for (const out of outgoing) {
      const splice: StashedEdge = {
        source: inc.source,
        target: out.target,
        sourceHandle: inc.sourceHandle ?? undefined,
        targetHandle: out.targetHandle ?? undefined,
        type: out.type ?? 'smoothstep',
      };
      const key = edgeKey(splice);
      if (existingKeys.has(key)) continue;
      existingKeys.add(key);
      splices.push(splice);
      splicedEdges.push(stashedToEdge(splice));
    }
  }

  const stash: PrevEdgesStash = splices.length > 0 ? { originals, splices } : { originals };

  const nextNodes = nodes.map((node) =>
    node.id === nodeId
      ? setTaskConfigOnNode(node, {
          disabled: true,
          _prev_edges: stash,
        })
      : node
  );

  return { nodes: nextNodes, edges: [...remaining, ...splicedEdges] };
};

export const enableTask = (nodeId: string, nodes: Node[], edges: Edge[]): { nodes: Node[]; edges: Edge[] } => {
  const target = nodes.find((n) => n.id === nodeId);
  const { originals, splices = [] } = unpackStash(target?.data?.taskConfig?._prev_edges);

  // 1) Drop the splices that disableTask added when this task was muted.
  const spliceKeys = new Set(splices.map(edgeKey));
  const afterUnsplice = edges.filter((e) => !spliceKeys.has(edgeKey(e)));

  // 2) Restore originals, filtering out edges whose endpoints no longer
  // exist (e.g. neighbor was deleted while this task was disabled).
  const nodeIds = new Set(nodes.map((n) => n.id));
  const presentKeys = new Set(afterUnsplice.map(edgeKey));
  const restored: Edge[] = [];
  for (const s of originals) {
    if (!nodeIds.has(s.source) || !nodeIds.has(s.target)) continue;
    const key = edgeKey(s);
    if (presentKeys.has(key)) continue;
    presentKeys.add(key);
    restored.push(stashedToEdge(s));
  }

  // 3) Scrub trigger auto-edges that are no longer needed now that real
  // predecessors are back.
  const merged = scrubRedundantTriggerEdges([...afterUnsplice, ...restored]);

  const nextNodes = nodes.map((node) => {
    if (node.id !== nodeId) return node;
    const nextTaskConfig = { ...(node.data?.taskConfig || {}) };
    delete nextTaskConfig.disabled;
    delete nextTaskConfig._prev_edges;
    return {
      ...node,
      data: { ...node.data, taskConfig: nextTaskConfig },
    };
  });

  return { nodes: nextNodes, edges: merged };
};

export const countDirectDependents = (nodeId: string, edges: Edge[]): number => edges.filter((e) => e.source === nodeId).length;
