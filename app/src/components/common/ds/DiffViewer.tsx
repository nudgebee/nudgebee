/**
 * DiffViewer — DS V2 (canonical / new).
 * Spec: app/design-system/primitives/content/diff-viewer.html (spec artifact not checked in yet)
 *
 * The single primitive for showing **what changed** between two versions of code/text, styled as a
 * sibling of `CodeBlock` — same `TONE_TOKENS` surface, same header pattern (title left · copy
 * right), `light`/`dark` tones, soft +/- colour-coding, and Prism syntax highlighting.
 *
 * Two layouts, picked by input (override with `mode`):
 *   - `gitDiff` (a unified-diff string) → **unified**: one column, +/- rows stacked.
 *   - `originalCode` + `newCode` (a pair) → **split**: two columns side-by-side (old ↔ new), each
 *     with its own CodeBlock-style header (label left · copy right).
 *   - A pair with `mode='unified'` computes the unified diff (via the `diff` lib) and stacks it.
 *
 * Pure markup (no CodeMirror) — lightweight, mono. The diff engine lives in `internal/diff/*`
 * (parsing, split-row building, palette, Prism highlighting); this file is just the rendering.
 *
 * Don't:
 *   - Don't use for a single version of code — that's `CodeBlock` (static) or `CodeEditor` (rich).
 *   - Don't hand-roll diff colouring or a copy button in the labels — the component owns both.
 */
import * as React from 'react';
import { Box, Typography } from '@mui/material';
import ChevronRightIcon from '@mui/icons-material/ChevronRight';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import CodeIcon from '@mui/icons-material/Code';
import { createTwoFilesPatch } from 'diff';
import { CodeSurface, SurfaceHeader, SurfaceTitle, CopyButton, MONO_FONT, TONE_TOKENS, type ToneTokens } from './internal/CodeSurface';
import type { CodeEditorLanguage } from './CodeEditor';
import type { DiffViewerMode, DiffViewerTone, DiffPalette, SideCellData, SplitRow, ParsedDiff } from './internal/diff/types';
import { DIFF_PALETTE } from './internal/diff/palette';
import { highlightLine, tokenThemeSx } from './internal/diff/highlight';
import { parseUnifiedDiff } from './internal/diff/parseUnified';
import { buildSplitRows } from './internal/diff/buildSplitRows';

export type { DiffViewerMode, DiffViewerTone } from './internal/diff/types';

export interface DiffViewerProps {
  /** A unified-diff string (git format). Drives the **unified** (stacked) layout. */
  gitDiff?: string;
  /** Old version. With `newCode`, drives the **split** (side-by-side) layout. */
  originalCode?: string;
  /** New version. */
  newCode?: string;
  /** Override the layout. Default: inferred — `gitDiff` → unified, a code pair → split. */
  mode?: DiffViewerMode;
  /** Syntax-highlight the diff body with Prism. Default 'text' (no highlighting). */
  language?: CodeEditorLanguage;
  /** 'light' (default) or 'dark' (terminal look) — shares CodeBlock's surface tokens. */
  tone?: DiffViewerTone;
  /** Header title (fallback when no filename is extracted from the diff). */
  title?: string;
  /** Default filename for the unified header (overridden if the diff names a file). */
  fileName?: string;
  /** Show the header bar. Default true. */
  showHeader?: boolean;
  /** Allow collapsing the body from the (unified) header. Default true. */
  collapsible?: boolean;
  /** Start expanded. Default true. */
  defaultExpanded?: boolean;
  /** Split-layout column titles (the component adds the copy button on the right of each). */
  leftLabel?: React.ReactNode;
  rightLabel?: React.ReactNode;
  /** Clamp body height (px or CSS length) and scroll past it. Default 400. */
  maxHeight?: number | string;
  className?: string;
  id?: string;
  'data-testid'?: string;
}

function SideCell({
  cell,
  numWidth,
  cfg,
  palette,
  divider,
  language,
}: {
  cell: SideCellData | null;
  numWidth: string;
  cfg: ToneTokens;
  palette: DiffPalette;
  divider: boolean;
  language: CodeEditorLanguage;
}) {
  const highlighted = cell ? highlightLine(cell.content, language) : null;
  const rowBg = !cell ? palette.emptyBg : cell.kind === 'add' ? palette.addBg : cell.kind === 'delete' ? palette.delBg : 'transparent';
  const markerColor = cell
    ? cell.kind === 'add'
      ? palette.addMarker
      : cell.kind === 'delete'
      ? palette.delMarker
      : palette.ctxMarker
    : 'transparent';
  // `divider` puts the column boundary on the LEFT cell's right edge (border-box keeps it at 50%).
  const cellSx = {
    flex: 1,
    minWidth: 0,
    display: 'flex',
    alignItems: 'stretch',
    backgroundColor: rowBg,
    ...(divider ? { borderRight: `1px solid ${cfg.border}` } : null),
  };
  return (
    <Box sx={cellSx}>
      {/* line-number gutter — neutral background + right border separates it from the code */}
      <Box
        component='span'
        aria-hidden='true'
        sx={{
          flexShrink: 0,
          width: numWidth,
          paddingX: 'var(--ds-space-2)',
          textAlign: 'right',
          color: cfg.gutterText,
          backgroundColor: cfg.headerBg,
          borderRight: `1px solid ${cfg.border}`,
          userSelect: 'none',
        }}
      >
        {cell?.lineNo ?? ''}
      </Box>
      {cell && (
        <>
          <Box
            component='span'
            aria-hidden='true'
            sx={{ flexShrink: 0, width: '1.5em', textAlign: 'center', color: markerColor, userSelect: 'none' }}
          >
            {cell.marker}
          </Box>
          <Box
            component='span'
            sx={{ flex: 1, minWidth: 0, paddingRight: 'var(--ds-space-2)', whiteSpace: 'pre-wrap', wordBreak: 'break-all', color: cfg.text }}
            {...(highlighted ? { dangerouslySetInnerHTML: highlighted } : { children: cell.content || ' ' })}
          />
        </>
      )}
    </Box>
  );
}

function SplitDiff({
  rows,
  maxHeight,
  cfg,
  palette,
  header,
  language,
  tone,
}: {
  rows: SplitRow[];
  maxHeight: string;
  cfg: ToneTokens;
  palette: DiffPalette;
  header: React.ReactNode;
  language: CodeEditorLanguage;
  tone: DiffViewerTone;
}) {
  const maxNo = rows.reduce((m, r) => Math.max(m, r.left?.lineNo ?? 0, r.right?.lineNo ?? 0), 0);
  const numWidth = `${String(maxNo).length + 1}ch`;
  // Header lives INSIDE the scroll container (sticky) so it shares the body's width context — a
  // vertical scrollbar then shrinks header and body equally, keeping the two columns aligned.
  return (
    <Box sx={{ maxHeight, overflow: 'auto', fontFamily: MONO_FONT, fontSize: 'var(--ds-text-small)', lineHeight: 1.6, ...tokenThemeSx(tone) }}>
      {header}
      {rows.map((row, i) => (
        <Box key={`r-${i}-${row.left?.lineNo ?? 'x'}-${row.right?.lineNo ?? 'x'}`} sx={{ display: 'flex', alignItems: 'stretch' }}>
          <SideCell cell={row.left} numWidth={numWidth} cfg={cfg} palette={palette} divider language={language} />
          <SideCell cell={row.right} numWidth={numWidth} cfg={cfg} palette={palette} divider={false} language={language} />
        </Box>
      ))}
    </Box>
  );
}

export function DiffViewer({
  gitDiff,
  originalCode,
  newCode,
  mode,
  language = 'text',
  tone = 'light',
  title = 'Code Changes',
  fileName = 'code',
  showHeader = true,
  collapsible = true,
  defaultExpanded = true,
  leftLabel,
  rightLabel,
  maxHeight = 400,
  className,
  id,
  'data-testid': dataTestId,
}: DiffViewerProps) {
  const [expanded, setExpanded] = React.useState(defaultExpanded);
  const cfg = TONE_TOKENS[tone];
  const palette = DIFF_PALETTE[tone];

  const hasPair = originalCode !== undefined && newCode !== undefined;
  // Infer: a code pair → split; a diff string → unified. `mode` overrides.
  const resolvedMode: DiffViewerMode = mode ?? (hasPair && gitDiff === undefined ? 'split' : 'unified');
  const clampHeight = typeof maxHeight === 'number' ? `${maxHeight}px` : maxHeight;

  // Unified engine: use `gitDiff` directly, or compute one from the code pair on demand.
  const unified = React.useMemo<ParsedDiff | null>(() => {
    if (resolvedMode === 'split') {
      return null;
    }
    let source = gitDiff;
    if (source === undefined && hasPair) {
      source = createTwoFilesPatch('original', 'modified', originalCode as string, newCode as string);
    }
    if (source === undefined) {
      return null;
    }
    return parseUnifiedDiff(source, fileName);
  }, [resolvedMode, gitDiff, hasPair, originalCode, newCode, fileName]);

  // Split engine: aligned old↔new rows from the pair.
  const splitRows = React.useMemo<SplitRow[] | null>(() => {
    if (resolvedMode !== 'split' || !hasPair) {
      return null;
    }
    return buildSplitRows(originalCode as string, newCode as string);
  }, [resolvedMode, hasPair, originalCode, newCode]);

  if (resolvedMode === 'split' && !splitRows) {
    return (
      <Box sx={{ padding: 'var(--ds-space-4)', border: `1px solid ${cfg.border}`, borderRadius: 'var(--ds-radius-md)' }}>
        <Typography color='error'>DiffViewer: split mode needs both `originalCode` and `newCode`.</Typography>
      </Box>
    );
  }
  if (resolvedMode === 'unified' && !unified) {
    return (
      <Box sx={{ padding: 'var(--ds-space-4)', border: `1px solid ${cfg.border}`, borderRadius: 'var(--ds-radius-md)' }}>
        <Typography color='error'>DiffViewer: no diff data provided.</Typography>
      </Box>
    );
  }

  const headerName = unified?.fileName || title;
  const additions = unified ? unified.additions : splitRows?.filter((r) => r.right?.kind === 'add').length ?? 0;
  const deletions = unified ? unified.deletions : splitRows?.filter((r) => r.left?.kind === 'delete').length ?? 0;
  const unifiedCopyText = gitDiff ?? (hasPair ? newCode : undefined);

  // Header label style — title left, in the DS display font (matches CodeBlock's header title slot).
  const labelSx = {
    flex: 1,
    minWidth: 0,
    fontFamily: 'var(--ds-font-display)',
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
    color: cfg.titleText,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  } as const;

  const splitHeaderCols = [
    { label: leftLabel ?? 'Original', copy: originalCode, border: true },
    { label: rightLabel ?? 'Modified', copy: newCode, border: false },
  ];

  // Sticky two-column header rendered INSIDE the split scroll container (see SplitDiff). Same
  // 50/50 geometry as the body rows (left half border-right) so the divider lines up.
  const splitHeaderNode = showHeader ? (
    <Box sx={{ position: 'sticky', top: 0, zIndex: 1, display: 'flex', backgroundColor: cfg.headerBg, borderBottom: `1px solid ${cfg.border}` }}>
      {splitHeaderCols.map((col, idx) => (
        <Box
          key={idx === 0 ? 'left' : 'right'}
          sx={{
            flex: 1,
            minWidth: 0,
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            minHeight: '32px',
            padding: '0 var(--ds-space-2) 0 var(--ds-space-3)',
            borderRight: col.border ? `1px solid ${cfg.border}` : 'none',
          }}
        >
          <Box component='span' sx={labelSx}>
            {col.label}
          </Box>
          {col.copy !== undefined && <CopyButton text={col.copy} size='sm' toastMessage='Copied' />}
        </Box>
      ))}
    </Box>
  ) : null;

  return (
    <CodeSurface tone={tone} id={id} className={className} data-testid={dataTestId}>
      {/* Unified header — CodeBlock-style bar: title left, counts + copy right. */}
      {showHeader && resolvedMode === 'unified' && (
        <SurfaceHeader tone={tone} onClick={collapsible ? () => setExpanded((e) => !e) : undefined} interactive={collapsible} divided={expanded}>
          {collapsible &&
            (expanded ? (
              <ExpandMoreIcon sx={{ fontSize: 16, color: cfg.titleText }} />
            ) : (
              <ChevronRightIcon sx={{ fontSize: 16, color: cfg.titleText }} />
            ))}
          <CodeIcon sx={{ fontSize: 14, color: cfg.titleText }} />
          <SurfaceTitle tone={tone}>{headerName}</SurfaceTitle>
          {additions > 0 && (
            <Typography sx={{ fontFamily: MONO_FONT, fontSize: 'var(--ds-text-small)', color: 'var(--ds-green-500)' }}>+{additions}</Typography>
          )}
          {deletions > 0 && (
            <Typography sx={{ fontFamily: MONO_FONT, fontSize: 'var(--ds-text-small)', color: 'var(--ds-red-500)' }}>-{deletions}</Typography>
          )}
          {unifiedCopyText !== undefined && <CopyButton text={unifiedCopyText} size='sm' toastMessage='Copied' />}
        </SurfaceHeader>
      )}

      {expanded && (
        <>
          {resolvedMode === 'split' ? (
            <SplitDiff
              rows={splitRows as SplitRow[]}
              maxHeight={clampHeight}
              cfg={cfg}
              palette={palette}
              header={splitHeaderNode}
              language={language}
              tone={tone}
            />
          ) : (
            <Box sx={{ fontFamily: MONO_FONT, fontSize: 'var(--ds-text-small)', maxHeight: clampHeight, overflow: 'auto', ...tokenThemeSx(tone) }}>
              {(unified as ParsedDiff).lines.map((line) => {
                const bg =
                  line.kind === 'hunk'
                    ? palette.hunkBg
                    : line.kind === 'add'
                    ? palette.addBg
                    : line.kind === 'delete'
                    ? palette.delBg
                    : 'transparent';
                const markerColor = line.kind === 'add' ? palette.addMarker : line.kind === 'delete' ? palette.delMarker : palette.ctxMarker;
                // Hunk headers (@@ …) are not code — render them raw; highlight everything else.
                const highlighted = line.kind === 'hunk' ? null : highlightLine(line.content, language);
                return (
                  <Box
                    key={line.id}
                    sx={{
                      display: 'flex',
                      padding: 'var(--ds-space-1) 0',
                      backgroundColor: bg,
                      color: cfg.text,
                      ...(line.kind === 'hunk' ? { fontWeight: 'var(--ds-font-weight-semibold)', color: cfg.gutterText } : null),
                    }}
                  >
                    {line.kind !== 'hunk' && (
                      <Box
                        component='span'
                        sx={{ minWidth: '32px', paddingX: 'var(--ds-space-2)', color: markerColor, textAlign: 'right', userSelect: 'none' }}
                      >
                        {line.marker}
                      </Box>
                    )}
                    <Box
                      component='span'
                      sx={{ flex: 1, paddingX: 'var(--ds-space-3)', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}
                      {...(highlighted ? { dangerouslySetInnerHTML: highlighted } : { children: line.content })}
                    />
                  </Box>
                );
              })}
            </Box>
          )}
        </>
      )}
    </CodeSurface>
  );
}

export default DiffViewer;
