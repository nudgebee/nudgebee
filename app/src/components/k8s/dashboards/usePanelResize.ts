import React from 'react';
import { snapPanelWidth } from './panelDefaults';

interface Options {
  /** The 12-column grid element. Its measured width defines one column. */
  gridRef: React.RefObject<HTMLDivElement | null>;
  /** Column gap in px. Must match the grid's own `gap` or the maths drifts. */
  gap: number;
  /** Called once per width CHANGE, not once per mouse move. */
  onResize: (panelId: number, width: number) => void;
}

/** Drag-to-resize for dashboard panels, snapping to the legal 12ths. */
export function usePanelResize({ gridRef, gap, onResize }: Options) {
  const [resizingId, setResizingId] = React.useState<number | null>(null);

  /** Live drag state. */
  const dragRef = React.useRef<{ id: number; startX: number; startW: number; columnW: number; committed: number } | null>(null);

  // Keeps the document handlers calling the CURRENT onResize without rebinding
  // them mid-drag — rebinding would drop the listeners and strand the drag.
  const onResizeRef = React.useRef(onResize);
  React.useEffect(() => {
    onResizeRef.current = onResize;
  }, [onResize]);

  /** Set while a drag is live so unmount can tear the same listeners down. */
  const stopRef = React.useRef<(() => void) | null>(null);
  React.useEffect(() => () => stopRef.current?.(), []);

  const startResize = React.useCallback(
    (panelId: number, startWidth: number, event: React.MouseEvent) => {
      const grid = gridRef.current;
      if (!grid) return;
      // The handle sits inside a draggable panel; without this the same gesture
      // would also start a sort.
      event.preventDefault();
      event.stopPropagation();

      // 12 columns with 11 gaps between them.
      const columnW = (grid.getBoundingClientRect().width - gap * 11) / 12;
      // A grid that has not been laid out yet (hidden tab, zero-width parent)
      // would make every delta infinite.
      if (!(columnW > 0)) return;

      dragRef.current = { id: panelId, startX: event.clientX, startW: startWidth, columnW, committed: startWidth };
      setResizingId(panelId);

      const previousCursor = document.body.style.cursor;
      const previousSelect = document.body.style.userSelect;
      document.body.style.cursor = 'col-resize';
      // Without this a drag across the dashboard selects every panel title.
      document.body.style.userSelect = 'none';

      const move = (e: MouseEvent) => {
        const drag = dragRef.current;
        if (!drag) return;
        // One column step is the column plus the gap that follows it.
        const columns = (e.clientX - drag.startX) / (drag.columnW + gap);
        const width = snapPanelWidth(drag.startW + columns);
        if (width === drag.committed) return;
        drag.committed = width;
        onResizeRef.current(drag.id, width);
      };

      const stop = () => {
        document.removeEventListener('mousemove', move);
        document.removeEventListener('mouseup', stop);
        document.body.style.cursor = previousCursor;
        document.body.style.userSelect = previousSelect;
        dragRef.current = null;
        stopRef.current = null;
        setResizingId(null);
      };

      stopRef.current = stop;
      document.addEventListener('mousemove', move);
      document.addEventListener('mouseup', stop);
    },
    [gap, gridRef]
  );

  return { resizingId, startResize };
}

export default usePanelResize;
