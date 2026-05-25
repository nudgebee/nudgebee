import { useEffect, useRef } from 'react';
import type { Node, Edge } from 'reactflow';

interface UseWorkflowShortcutsProps {
  enabled: boolean;
  nodes: Node[];
  edges: Edge[];
  setNodes: React.Dispatch<React.SetStateAction<Node[]>>;
  copySelection: (nodes: Node[], edges: Edge[]) => boolean;
  cutSelection: (nodes: Node[], edges: Edge[]) => boolean;
  paste: (nodes: Node[], edges: Edge[]) => Promise<void> | void;
  duplicateSelection: (nodes: Node[], edges: Edge[]) => void;
  undo: () => void;
  redo: () => void;
  onEscape: () => void;
}

const INPUT_TAG_NAMES = new Set(['INPUT', 'TEXTAREA', 'SELECT']);

const isEditableTarget = (target: EventTarget | null): boolean => {
  if (!(target instanceof HTMLElement)) return false;
  if (INPUT_TAG_NAMES.has(target.tagName)) return true;
  if (target.isContentEditable) return true;
  if (target.closest('.cm-editor')) return true;
  if (target.closest('[contenteditable="true"]')) return true;
  return false;
};

const isModalOpen = (): boolean => {
  if (typeof document === 'undefined') return false;
  return document.querySelector('.MuiModal-root .MuiBackdrop-root:not(.MuiBackdrop-invisible)') !== null;
};

/**
 * Window-level keyboard shortcuts for the workflow editor.
 * Guards: bails on editable targets, open MUI modals, or when `enabled` is false.
 */
export function useWorkflowShortcuts({
  enabled,
  nodes,
  edges,
  setNodes,
  copySelection,
  cutSelection,
  paste,
  duplicateSelection,
  undo,
  redo,
  onEscape,
}: UseWorkflowShortcutsProps): void {
  const propsRef = useRef({ nodes, edges, setNodes, copySelection, cutSelection, paste, duplicateSelection, undo, redo, onEscape, enabled });
  useEffect(() => {
    propsRef.current = { nodes, edges, setNodes, copySelection, cutSelection, paste, duplicateSelection, undo, redo, onEscape, enabled };
  });

  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      const p = propsRef.current;
      if (!p.enabled) return;

      const key = event.key;

      if (key === 'Escape' && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
        if (isEditableTarget(event.target)) return;
        p.onEscape();
        return;
      }

      const isModifier = event.ctrlKey || event.metaKey;
      if (!isModifier) return;

      if (isEditableTarget(event.target)) return;
      if (isModalOpen()) return;

      const lowerKey = key.toLowerCase();

      switch (lowerKey) {
        case 'c': {
          if (event.shiftKey || event.altKey) return;
          const copied = p.copySelection(p.nodes, p.edges);
          if (copied) event.preventDefault();
          break;
        }
        case 'x': {
          if (event.shiftKey || event.altKey) return;
          const cut = p.cutSelection(p.nodes, p.edges);
          if (cut) event.preventDefault();
          break;
        }
        case 'v': {
          if (event.shiftKey || event.altKey) return;
          event.preventDefault();
          void p.paste(p.nodes, p.edges);
          break;
        }
        case 'd': {
          if (event.shiftKey || event.altKey) return;
          event.preventDefault();
          p.duplicateSelection(p.nodes, p.edges);
          break;
        }
        case 'z': {
          if (event.altKey) return;
          event.preventDefault();
          if (event.shiftKey) p.redo();
          else p.undo();
          break;
        }
        case 'y': {
          if (event.shiftKey || event.altKey) return;
          event.preventDefault();
          p.redo();
          break;
        }
        case 'a': {
          if (event.shiftKey || event.altKey) return;
          event.preventDefault();
          p.setNodes((prev) => prev.map((n) => ({ ...n, selected: true })));
          break;
        }
        default:
          break;
      }
    };

    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, []);
}

export default useWorkflowShortcuts;
