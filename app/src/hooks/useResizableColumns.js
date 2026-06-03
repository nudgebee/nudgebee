import { useState, useEffect, useRef, useCallback } from 'react';

const DEFAULT_MIN_COL_WIDTH = 60;
const DEFAULT_MAX_COL_WIDTH = 600;
const DEFAULT_WIDTH_PERCENT = 20;

// Size presets. Use `size: 'xs'|'sm'|'md'|'lg'|'xl'` on a header to declare intent
// instead of picking raw min/max numbers. Raw `minWidth`/`maxWidth` on the header
// still win if set, so specific columns can override the preset.
const SIZE_PRESETS = {
  xs: { min: 60, max: 100 }, // icon-only columns
  sm: { min: 90, max: 160 }, // action buttons, status chips, short enum cells
  md: { min: 120, max: 320 }, // standard text columns (default)
  lg: { min: 180, max: 500 }, // wide text: application/resource names
  xl: { min: 240, max: 900 }, // long text: messages, descriptions
};

function parsePercent(widthStr) {
  if (!widthStr) return DEFAULT_WIDTH_PERCENT;
  const num = parseFloat(widthStr);
  return isNaN(num) ? DEFAULT_WIDTH_PERCENT : num;
}

function getColBounds(header) {
  const isObj = header && typeof header === 'object';
  const preset = isObj && header.size && SIZE_PRESETS[header.size];
  const baseMin = preset ? preset.min : DEFAULT_MIN_COL_WIDTH;
  const baseMax = preset ? preset.max : DEFAULT_MAX_COL_WIDTH;
  const min = isObj && typeof header.minWidth === 'number' ? header.minWidth : baseMin;
  const max = isObj && typeof header.maxWidth === 'number' ? header.maxWidth : baseMax;
  return { min, max: Math.max(min, max) };
}

function computePixelWidths(headers, containerWidth) {
  return headers.map((h) => {
    const pct = parsePercent(typeof h === 'string' ? null : h?.width);
    const px = Math.round((pct / 100) * containerWidth);
    const { min, max } = getColBounds(h);
    return Math.max(min, Math.min(max, px));
  });
}

function applyWidth(cols, cells, colIndex, width) {
  const col = cols?.[colIndex];
  if (col) col.style.width = width + 'px';
  const cell = cells?.[colIndex];
  if (cell) {
    cell.style.width = width + 'px';
    cell.style.minWidth = width + 'px';
    cell.style.maxWidth = width + 'px';
    cell.setAttribute('width', width);
  }
}

/**
 * useResizableColumns - Hook for Excel-style pair resize of adjacent table columns.
 *
 * Dragging the handle between column N and N+1 grows one and shrinks the other by the
 * same amount, so the total table width stays constant. Per-column `minWidth`/`maxWidth`
 * (pixels) on the header object override the defaults.
 *
 * @param {Object} params
 * @param {Array} params.headers - Current visible headers (objects or strings). Objects
 *   may specify `width` (percent string, e.g. '25%'), `size` ('xs'|'sm'|'md'|'lg'|'xl'
 *   for preset bounds), or raw `minWidth`/`maxWidth` in pixels (override the preset).
 * @param {Object} params.containerRef - Ref to the TableContainer element
 * @param {boolean} params.enabled - Whether resize is enabled
 * @returns {{columnWidths: number[], totalTableWidth: number, isResizing: boolean, handleResizeStart: Function}}
 */
export default function useResizableColumns({ headers, containerRef, enabled }) {
  const [columnWidths, setColumnWidths] = useState([]);
  const [isResizing, setIsResizing] = useState(false);

  const widthsRef = useRef([]);
  const headersRef = useRef(headers);
  const hasManualResizeRef = useRef(false);
  const dragStateRef = useRef(null);
  const headersLenRef = useRef(0);

  // Keep the latest headers available to drag handlers without re-binding them.
  useEffect(() => {
    headersRef.current = headers;
  }, [headers]);

  // Initialize / recalculate widths from percentages on mount / header change
  useEffect(() => {
    if (!enabled || !containerRef?.current) return;

    const container = containerRef.current;
    const containerWidth = container.offsetWidth;
    if (containerWidth === 0) return;

    if (!hasManualResizeRef.current || headers.length !== headersLenRef.current) {
      const widths = computePixelWidths(headers, containerWidth);
      widthsRef.current = widths;
      headersLenRef.current = headers.length;
      setColumnWidths(widths);
      hasManualResizeRef.current = false;
    }
  }, [enabled, headers, containerRef]);

  // ResizeObserver: adjust widths when container size changes (only pre manual-resize)
  useEffect(() => {
    if (!enabled || !containerRef?.current) return;

    const observer = new ResizeObserver((entries) => {
      if (hasManualResizeRef.current) return;
      const entry = entries[0];
      if (!entry) return;
      const containerWidth = entry.contentRect.width;
      if (containerWidth === 0) return;
      const widths = computePixelWidths(headers, containerWidth);
      widthsRef.current = widths;
      setColumnWidths(widths);
    });

    observer.observe(containerRef.current);
    return () => observer.disconnect();
  }, [enabled, headers, containerRef]);

  const handleResizeStart = useCallback(
    (colIndex, e) => {
      if (!enabled) return;
      e.preventDefault();
      e.stopPropagation();

      const headersNow = headersRef.current || [];
      const startX = e.clientX;
      const startWidth = widthsRef.current[colIndex];

      const colBounds = getColBounds(headersNow[colIndex]);

      dragStateRef.current = { colIndex, startX, startWidth };
      setIsResizing(true);

      const prevCursor = document.body.style.cursor;
      document.body.style.cursor = 'col-resize';

      const table = containerRef.current?.querySelector('table');
      const headerCells = table?.querySelectorAll('thead tr:last-of-type th');
      const colEls = table?.querySelectorAll('colgroup[data-resizable-colgroup="true"] > col');

      const onMouseMove = (moveEvent) => {
        const state = dragStateRef.current;
        if (!state) return;

        const rawDelta = moveEvent.clientX - state.startX;

        // Only the dragged column changes width. Everything after it shifts right and
        // the table grows (horizontal scroll appears on the container).
        const minDelta = colBounds.min - state.startWidth;
        const maxDelta = colBounds.max - state.startWidth;
        const delta = Math.max(minDelta, Math.min(maxDelta, rawDelta));

        const newWidth = state.startWidth + delta;
        widthsRef.current[state.colIndex] = newWidth;
        applyWidth(colEls, headerCells, state.colIndex, newWidth);

        if (table) {
          // Drive table minWidth — table's CSS width stays 100%, so the last column
          // (which has no explicit width) flexes to fill any remaining container space.
          const total = widthsRef.current.reduce((sum, w) => sum + w, 0);
          table.style.minWidth = total + 'px';
        }
      };

      const onMouseUp = () => {
        document.removeEventListener('mousemove', onMouseMove);
        document.removeEventListener('mouseup', onMouseUp);
        document.body.style.cursor = prevCursor || '';

        hasManualResizeRef.current = true;
        dragStateRef.current = null;
        setIsResizing(false);
        setColumnWidths([...widthsRef.current]);
      };

      document.addEventListener('mousemove', onMouseMove);
      document.addEventListener('mouseup', onMouseUp);
    },
    [enabled, containerRef]
  );

  if (!enabled) {
    return {
      columnWidths: [],
      totalTableWidth: 0,
      isResizing: false,
      handleResizeStart: () => {},
    };
  }

  const totalTableWidth = columnWidths.reduce((sum, w) => sum + w, 0);

  return {
    columnWidths,
    totalTableWidth,
    isResizing,
    handleResizeStart,
  };
}
