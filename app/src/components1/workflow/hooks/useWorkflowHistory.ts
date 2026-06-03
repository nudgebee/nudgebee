import { useCallback, useEffect, useRef } from 'react';
import type { Node, Edge } from 'reactflow';
import type { WorkflowSettings } from '../types';

const MAX_HISTORY = 50;
const DEBOUNCE_MS = 300;

interface HistorySnapshot {
  nodes: Node[];
  edges: Edge[];
  settings: WorkflowSettings;
}

interface UseWorkflowHistoryProps {
  nodes: Node[];
  edges: Edge[];
  workflowSettings: WorkflowSettings;
  setNodes: React.Dispatch<React.SetStateAction<Node[]>>;
  setEdges: React.Dispatch<React.SetStateAction<Edge[]>>;
  setWorkflowSettings: React.Dispatch<React.SetStateAction<WorkflowSettings>>;
  /** False until initial load completes — gates snapshot collection. */
  enabled: boolean;
  /** Changes blow away both stacks. */
  workflowId: string | undefined;
}

interface UseWorkflowHistoryReturn {
  undo: () => void;
  redo: () => void;
}

const deepClone = <T>(value: T): T => {
  if (typeof structuredClone === 'function') return structuredClone(value);
  return JSON.parse(JSON.stringify(value));
};

const snapshotSignature = (snapshot: HistorySnapshot): string =>
  JSON.stringify({
    n: snapshot.nodes.map((n) => ({ id: n.id, type: n.type, position: n.position, data: n.data })),
    e: snapshot.edges.map((e) => ({
      id: e.id,
      source: e.source,
      target: e.target,
      sourceHandle: e.sourceHandle,
      targetHandle: e.targetHandle,
      type: e.type,
      data: e.data,
    })),
    s: snapshot.settings,
  });

/**
 * Undo/redo for workflow editor state. Debounced push (300ms) collapses rapid
 * changes like position drags into a single history entry. Suppresses pushes
 * during apply so undo doesn't immediately re-record what it just restored.
 */
export function useWorkflowHistory({
  nodes,
  edges,
  workflowSettings,
  setNodes,
  setEdges,
  setWorkflowSettings,
  enabled,
  workflowId,
}: UseWorkflowHistoryProps): UseWorkflowHistoryReturn {
  const pastRef = useRef<HistorySnapshot[]>([]);
  const futureRef = useRef<HistorySnapshot[]>([]);
  const lastSignatureRef = useRef<string>('');
  const isApplyingRef = useRef<boolean>(false);
  const debounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    pastRef.current = [];
    futureRef.current = [];
    lastSignatureRef.current = '';
  }, [workflowId]);

  useEffect(() => {
    if (!enabled) return;
    if (isApplyingRef.current) return;

    if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    debounceTimerRef.current = setTimeout(() => {
      const current: HistorySnapshot = {
        nodes: deepClone(nodes),
        edges: deepClone(edges),
        settings: deepClone(workflowSettings),
      };
      const signature = snapshotSignature(current);

      if (signature === lastSignatureRef.current) return;

      if (lastSignatureRef.current !== '') {
        pastRef.current.push({
          nodes: deepClone(nodes),
          edges: deepClone(edges),
          settings: deepClone(workflowSettings),
        });
        if (pastRef.current.length > MAX_HISTORY) pastRef.current.shift();
      }
      lastSignatureRef.current = signature;
      futureRef.current = [];
    }, DEBOUNCE_MS);

    return () => {
      if (debounceTimerRef.current) clearTimeout(debounceTimerRef.current);
    };
  }, [nodes, edges, workflowSettings, enabled]);

  const undo = useCallback(() => {
    if (pastRef.current.length === 0) return;
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current);
      debounceTimerRef.current = null;
    }
    const previous = pastRef.current.pop()!;
    const current: HistorySnapshot = {
      nodes: deepClone(nodes),
      edges: deepClone(edges),
      settings: deepClone(workflowSettings),
    };
    futureRef.current.push(current);
    if (futureRef.current.length > MAX_HISTORY) futureRef.current.shift();

    isApplyingRef.current = true;
    setNodes(previous.nodes);
    setEdges(previous.edges);
    setWorkflowSettings(previous.settings);
    lastSignatureRef.current = snapshotSignature(previous);

    setTimeout(() => {
      isApplyingRef.current = false;
    }, DEBOUNCE_MS + 50);
  }, [nodes, edges, workflowSettings, setNodes, setEdges, setWorkflowSettings]);

  const redo = useCallback(() => {
    if (futureRef.current.length === 0) return;
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current);
      debounceTimerRef.current = null;
    }
    const next = futureRef.current.pop()!;
    const current: HistorySnapshot = {
      nodes: deepClone(nodes),
      edges: deepClone(edges),
      settings: deepClone(workflowSettings),
    };
    pastRef.current.push(current);
    if (pastRef.current.length > MAX_HISTORY) pastRef.current.shift();

    isApplyingRef.current = true;
    setNodes(next.nodes);
    setEdges(next.edges);
    setWorkflowSettings(next.settings);
    lastSignatureRef.current = snapshotSignature(next);

    setTimeout(() => {
      isApplyingRef.current = false;
    }, DEBOUNCE_MS + 50);
  }, [nodes, edges, workflowSettings, setNodes, setEdges, setWorkflowSettings]);

  return { undo, redo };
}

export default useWorkflowHistory;
