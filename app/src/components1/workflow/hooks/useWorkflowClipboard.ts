import { useCallback, useRef } from 'react';
import type { Node, Edge } from 'reactflow';
import { snackbar } from '@components1/common/snackbarService';
import { generateUniqueId } from '@components1/workflow/utils';

const CLIPBOARD_SENTINEL = '__nudgebee_workflow_clipboard__';
const PASTE_OFFSET = { x: 40, y: 40 };

interface WorkflowClipboardPayload {
  [CLIPBOARD_SENTINEL]: 1;
  nodes: Node[];
  edges: Edge[];
}

interface UseWorkflowClipboardProps {
  setNodes: React.Dispatch<React.SetStateAction<Node[]>>;
  setEdges: React.Dispatch<React.SetStateAction<Edge[]>>;
}

interface UseWorkflowClipboardReturn {
  copySelection: (allNodes: Node[], allEdges: Edge[]) => boolean;
  cutSelection: (allNodes: Node[], allEdges: Edge[]) => boolean;
  paste: (allNodes: Node[], allEdges: Edge[]) => Promise<void>;
  duplicateSelection: (allNodes: Node[], allEdges: Edge[]) => void;
}

const isValidPayload = (data: unknown): data is WorkflowClipboardPayload => {
  return (
    typeof data === 'object' &&
    data !== null &&
    (data as Record<string, unknown>)[CLIPBOARD_SENTINEL] === 1 &&
    Array.isArray((data as Record<string, unknown>).nodes) &&
    Array.isArray((data as Record<string, unknown>).edges)
  );
};

const buildPayload = (selectedNodes: Node[], allEdges: Edge[]): WorkflowClipboardPayload | null => {
  if (selectedNodes.length === 0) return null;
  const selectedIds = new Set(selectedNodes.map((n) => n.id));
  const internalEdges = allEdges.filter((e) => selectedIds.has(e.source) && selectedIds.has(e.target));
  return {
    [CLIPBOARD_SENTINEL]: 1,
    nodes: selectedNodes.map((n) => ({ ...n, selected: false })),
    edges: internalEdges.map((e) => ({ ...e, selected: false })),
  };
};

const remapPayload = (payload: WorkflowClipboardPayload, currentNodes: Node[]): { nodes: Node[]; edges: Edge[] } => {
  const idMap = new Map<string, string>();
  let working = [...currentNodes];

  const newNodes: Node[] = payload.nodes.map((src) => {
    const newId = generateUniqueId(src.id, working);
    idMap.set(src.id, newId);
    const newNode: Node = {
      ...src,
      id: newId,
      selected: true,
      position: {
        x: src.position.x + PASTE_OFFSET.x,
        y: src.position.y + PASTE_OFFSET.y,
      },
      data: { ...src.data },
    };

    if (newNode.data?.taskConfig) {
      newNode.data = {
        ...newNode.data,
        taskConfig: { ...newNode.data.taskConfig, id: newId },
      };
    }

    working = [...working, newNode];
    return newNode;
  });

  const remappedNodes = newNodes.map((n) => {
    if (n.type !== 'switch' || !n.data?.taskConfig?.config) return n;
    const config = n.data.taskConfig.config;
    const cases = Array.isArray(config.cases) ? config.cases : [];
    const newCases = cases.map((c: any) => {
      if (c?.next && idMap.has(c.next)) return { ...c, next: idMap.get(c.next) };
      return c;
    });
    const newDefault = config.default_next && idMap.has(config.default_next) ? idMap.get(config.default_next) : config.default_next;
    return {
      ...n,
      data: {
        ...n.data,
        taskConfig: {
          ...n.data.taskConfig,
          config: { ...config, cases: newCases, default_next: newDefault },
        },
      },
    };
  });

  const newEdges: Edge[] = payload.edges
    .filter((e) => idMap.has(e.source) && idMap.has(e.target))
    .map((e) => {
      const newSource = idMap.get(e.source)!;
      const newTarget = idMap.get(e.target)!;
      return {
        ...e,
        id: `reactflow-edge-${newSource}-${newTarget}-${Date.now()}-${Math.floor(Math.random() * 1e6)}`,
        source: newSource,
        target: newTarget,
        selected: false,
      };
    });

  return { nodes: remappedNodes, edges: newEdges };
};

export function useWorkflowClipboard({ setNodes, setEdges }: UseWorkflowClipboardProps): UseWorkflowClipboardReturn {
  const inMemoryRef = useRef<WorkflowClipboardPayload | null>(null);

  const writeClipboard = useCallback((payload: WorkflowClipboardPayload) => {
    inMemoryRef.current = payload;
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(JSON.stringify(payload)).catch(() => {
        // Non-fatal: in-memory ref still serves same-tab paste
      });
    }
  }, []);

  const copySelection = useCallback(
    (allNodes: Node[], allEdges: Edge[]): boolean => {
      const selected = allNodes.filter((n) => n.selected);
      const payload = buildPayload(selected, allEdges);
      if (!payload) {
        snackbar.info('Select node(s) to copy');
        return false;
      }
      writeClipboard(payload);
      snackbar.success(`Copied ${payload.nodes.length} node${payload.nodes.length === 1 ? '' : 's'}`);
      return true;
    },
    [writeClipboard]
  );

  const applyPasted = useCallback(
    (payload: WorkflowClipboardPayload, allNodes: Node[]) => {
      const { nodes: pastedNodes, edges: pastedEdges } = remapPayload(payload, allNodes);
      setNodes((prev) => [...prev.map((n) => ({ ...n, selected: false })), ...pastedNodes]);
      setEdges((prev) => [...prev, ...pastedEdges]);
      snackbar.success(`Pasted ${pastedNodes.length} node${pastedNodes.length === 1 ? '' : 's'}`);
    },
    [setNodes, setEdges]
  );

  const paste = useCallback(
    async (allNodes: Node[], _allEdges: Edge[]): Promise<void> => {
      if (inMemoryRef.current) {
        applyPasted(inMemoryRef.current, allNodes);
        return;
      }
      if (typeof navigator === 'undefined' || !navigator.clipboard?.readText) {
        snackbar.info('Nothing to paste');
        return;
      }
      try {
        const text = await navigator.clipboard.readText();
        if (!text) {
          snackbar.info('Nothing to paste');
          return;
        }
        const parsed = JSON.parse(text);
        if (!isValidPayload(parsed)) {
          snackbar.info('Clipboard does not contain workflow nodes');
          return;
        }
        applyPasted(parsed, allNodes);
      } catch {
        snackbar.info('Nothing to paste');
      }
    },
    [applyPasted]
  );

  const cutSelection = useCallback(
    (allNodes: Node[], allEdges: Edge[]): boolean => {
      const selected = allNodes.filter((n) => n.selected);
      const payload = buildPayload(selected, allEdges);
      if (!payload) {
        snackbar.info('Select node(s) to cut');
        return false;
      }
      writeClipboard(payload);
      const selectedIds = new Set(selected.map((n) => n.id));
      setNodes((prev) => prev.filter((n) => !selectedIds.has(n.id)));
      setEdges((prev) => prev.filter((e) => !selectedIds.has(e.source) && !selectedIds.has(e.target)));
      snackbar.success(`Cut ${payload.nodes.length} node${payload.nodes.length === 1 ? '' : 's'}`);
      return true;
    },
    [writeClipboard, setNodes, setEdges]
  );

  const duplicateSelection = useCallback(
    (allNodes: Node[], allEdges: Edge[]): void => {
      const selected = allNodes.filter((n) => n.selected);
      const payload = buildPayload(selected, allEdges);
      if (!payload) {
        snackbar.info('Select node(s) to duplicate');
        return;
      }
      const { nodes: dupNodes, edges: dupEdges } = remapPayload(payload, allNodes);
      setNodes((prev) => [...prev.map((n) => ({ ...n, selected: false })), ...dupNodes]);
      setEdges((prev) => [...prev, ...dupEdges]);
      snackbar.success(`Duplicated ${dupNodes.length} node${dupNodes.length === 1 ? '' : 's'}`);
    },
    [setNodes, setEdges]
  );

  return { copySelection, cutSelection, paste, duplicateSelection };
}

export default useWorkflowClipboard;
