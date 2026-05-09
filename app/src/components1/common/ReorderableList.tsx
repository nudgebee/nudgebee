import React, { useState, useRef } from 'react';
import { Box, Typography } from '@mui/material';
import { DragIndicator } from '@mui/icons-material';

/**
 * Props the consumer spreads onto whatever element should be the drag handle
 * (an icon, a chevron, a whole row — caller's choice). Without spreading these
 * onto an element, drag won't start. This keeps the rest of the row free for
 * normal interactions (clicking to expand, typing in inputs, etc.) so we
 * never accidentally hijack a click.
 */
export interface DragHandleProps {
  draggable: true;
  onDragStart: (e: React.DragEvent) => void;
  onDragEnd: (e: React.DragEvent) => void;
  style: React.CSSProperties;
}

interface ReorderableListProps<T> {
  items: T[];
  /**
   * Called after a drop with the reordered array. The extra `fromIndex` /
   * `toIndex` args let consumers remap any side-state that is keyed by the
   * old positions (e.g. a Set<number> of expanded rows) without diffing the
   * array.
   */
  onReorder: (next: T[], fromIndex: number, toIndex: number) => void;
  /**
   * Stable identifier for each item — the key React uses for reconciliation
   * AND the value used to detect "did the dragged row even move?".
   * For schema items keyed only by index, return `index` (the wrapper still
   * works; you just lose stable React keys across reorders).
   */
  getItemKey: (item: T, index: number) => string | number;
  renderItem: (item: T, index: number, dragHandleProps: DragHandleProps) => React.ReactNode;
  disabled?: boolean;
  /**
   * Override the default insertion-line color. Falls back to MUI primary.main.
   */
  dropIndicatorColor?: string;
  /**
   * Short label rendered as the drag preview chip following the cursor. When
   * omitted, the browser falls back to its default (a screenshot of the
   * dragged element). Useful for tall/expanded rows where the screenshot
   * is awkward — pass a meaningful summary like the row's primary field
   * value (e.g. "Case Value: Approved").
   */
  getDragLabel?: (item: T, index: number) => string;
  /**
   * Caption shown above the list explaining the reorder affordance.
   * Defaults to "Drag the handle to reorder". Auto-hidden when there's
   * only one item (or zero) — nothing to reorder. Pass an empty string
   * to suppress entirely.
   */
  helperText?: string;
}

/**
 * ReorderableList renders a vertical list with HTML5 drag-and-drop reorder.
 *
 * The wrapper is render-prop based so it doesn't constrain the visual shape
 * of items — works equally well for compact rows (Switch case header), tall
 * accordions (KubernetesCreateAlert action cards), or anything else.
 *
 * Drag is initiated only by elements that receive `dragHandleProps` via the
 * render-prop's third argument. Any other element inside the row stays
 * interactive (text inputs, dropdowns, click-to-expand, delete buttons).
 *
 * Drop position is computed from cursor Y vs. each row's bounding-box midpoint
 * — above the midpoint inserts above the row, below inserts after. A 2px line
 * marks the insertion target. The dragged row dims to 0.4 opacity in flight.
 *
 * Keyboard / touch reordering are not implemented (HTML5 drag is mouse-only).
 * If accessibility becomes a requirement, the same render-prop API can be
 * backed by dnd-kit later without callers changing.
 */
export function ReorderableList<T>({
  items,
  onReorder,
  getItemKey,
  renderItem,
  disabled = false,
  dropIndicatorColor,
  getDragLabel,
  helperText = 'Drag the handle to reorder',
}: ReorderableListProps<T>) {
  const [draggingIndex, setDraggingIndex] = useState<number | null>(null);
  const [dropIndex, setDropIndex] = useState<number | null>(null);
  // Ref + state for dragging are kept in sync; the ref is read inside event
  // handlers that may close over a stale state. Specifically `onDragOver`
  // fires many times and we don't want to re-create the closure each render.
  const draggingIndexRef = useRef<number | null>(null);

  const startDrag = (index: number) => (e: React.DragEvent) => {
    if (disabled) return;
    setDraggingIndex(index);
    draggingIndexRef.current = index;
    // Firefox needs setData or the drag never starts.
    e.dataTransfer.effectAllowed = 'move';
    try {
      e.dataTransfer.setData('text/plain', String(index));
    } catch {
      /* Safari occasionally throws here; ignore — the drag still works. */
    }

    // Optional custom drag preview: build a transient DOM chip styled to
    // look like a small toast, set as the dataTransfer drag image, and
    // remove on next tick (the browser has already snapshotted it by
    // then). Falls back to the browser default when getDragLabel is
    // missing or returns an empty string.
    if (getDragLabel) {
      const label = getDragLabel(items[index], index);
      if (label) {
        const ghost = document.createElement('div');
        ghost.textContent = label;
        ghost.style.cssText = [
          'position:absolute',
          'top:-9999px',
          'left:-9999px',
          'padding:6px 12px',
          'background:#1f2937',
          'color:#ffffff',
          'border-radius:6px',
          'font-size:12px',
          'font-weight:500',
          'font-family:Roboto,system-ui,-apple-system,sans-serif',
          'box-shadow:0 4px 12px rgba(0,0,0,0.15)',
          'white-space:nowrap',
          'max-width:240px',
          'overflow:hidden',
          'text-overflow:ellipsis',
          'pointer-events:none',
          'z-index:9999',
        ].join(';');
        document.body.appendChild(ghost);
        try {
          // Anchor the chip slightly below-right of the cursor so it
          // doesn't cover what the user is pointing at.
          e.dataTransfer.setDragImage(ghost, 12, 12);
        } catch {
          /* setDragImage may fail in old browsers; default preview is fine */
        }
        // The browser snapshots the node synchronously; safe to remove
        // after the current tick.
        setTimeout(() => {
          if (ghost.parentNode) ghost.parentNode.removeChild(ghost);
        }, 0);
      }
    }
  };

  const endDrag = () => {
    setDraggingIndex(null);
    setDropIndex(null);
    draggingIndexRef.current = null;
  };

  const handleRowDragOver = (rowIndex: number) => (e: React.DragEvent) => {
    if (disabled || draggingIndexRef.current === null) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    const rect = e.currentTarget.getBoundingClientRect();
    const midY = rect.top + rect.height / 2;
    const above = e.clientY < midY;
    const target = above ? rowIndex : rowIndex + 1;
    if (target !== dropIndex) setDropIndex(target);
  };

  const handleRowDrop = (e: React.DragEvent) => {
    if (disabled) return;
    e.preventDefault();
    const from = draggingIndexRef.current;
    const to = dropIndex;
    endDrag();
    if (from === null || to === null) return;
    // No-op moves: dropping onto the same slot or the slot immediately
    // after itself would just put the item back where it was.
    if (to === from || to === from + 1) return;
    const next = items.slice();
    const [moved] = next.splice(from, 1);
    // When moving down, the splice above shifts every later index by -1, so
    // the original `to` becomes `to - 1`.
    const insertAt = to > from ? to - 1 : to;
    next.splice(insertAt, 0, moved);
    onReorder(next, from, insertAt);
  };

  const showLineAt = (index: number) => {
    if (draggingIndex === null || dropIndex === null) return false;
    if (dropIndex !== index) return false;
    // Hide the line when it would land in the same spot the row already
    // occupies (above its current position or just after it).
    if (index === draggingIndex || index === draggingIndex + 1) return false;
    return true;
  };

  const showHelper = !disabled && items.length > 1 && !!helperText;

  return (
    <Box>
      {showHelper && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 0.5,
            mb: 0.75,
            color: '#6b7280',
          }}
        >
          <DragIndicator sx={{ fontSize: 14 }} />
          <Typography component='span' sx={{ fontSize: '11px', fontStyle: 'italic' }}>
            {helperText}
          </Typography>
        </Box>
      )}
      {items.map((item, index) => {
        const dragHandleProps: DragHandleProps = {
          draggable: true,
          onDragStart: startDrag(index),
          onDragEnd: endDrag,
          style: { cursor: disabled ? 'default' : 'grab' },
        };
        return (
          <React.Fragment key={getItemKey(item, index)}>
            {showLineAt(index) && (
              <Box
                sx={{
                  height: '2px',
                  bgcolor: dropIndicatorColor ?? 'primary.main',
                  borderRadius: '1px',
                  my: 0.5,
                }}
              />
            )}
            <Box
              onDragOver={handleRowDragOver(index)}
              onDrop={handleRowDrop}
              sx={{
                opacity: draggingIndex === index ? 0.4 : 1,
                transition: 'opacity 120ms ease',
              }}
            >
              {renderItem(item, index, dragHandleProps)}
            </Box>
          </React.Fragment>
        );
      })}
      {showLineAt(items.length) && (
        <Box
          sx={{
            height: '2px',
            bgcolor: dropIndicatorColor ?? 'primary.main',
            borderRadius: '1px',
            my: 0.5,
          }}
        />
      )}
    </Box>
  );
}

export default ReorderableList;
