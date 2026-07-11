/**
 * Shared types for the DiffViewer internals (`ds/internal/diff/*`). Not for app code — consumed
 * only by `ds/DiffViewer.tsx` and its sibling helpers.
 */

export type DiffViewerTone = 'light' | 'dark';
export type DiffViewerMode = 'unified' | 'split';

/** The three semantic kinds a diff line can be. */
export type DiffKind = 'context' | 'add' | 'delete';

/** Per-tone diff colours (soft row tints + coloured +/- markers). */
export interface DiffPalette {
  addBg: string;
  delBg: string;
  addMarker: string;
  delMarker: string;
  ctxMarker: string;
  emptyBg: string;
  hunkBg: string;
}

/** A parsed line of a unified-diff (the stacked layout). */
export type UnifiedLine = { kind: 'hunk'; id: string; content: string } | { kind: DiffKind; id: string; marker: string; content: string };

export interface ParsedDiff {
  fileName: string;
  additions: number;
  deletions: number;
  lines: UnifiedLine[];
}

/** One side (left or right) of a split-layout row. */
export interface SideCellData {
  lineNo: number;
  marker: string;
  content: string;
  kind: DiffKind;
}

export interface SplitRow {
  left: SideCellData | null;
  right: SideCellData | null;
}
