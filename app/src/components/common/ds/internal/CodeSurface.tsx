/**
 * CodeSurface — the shared chrome for the DS code primitives (`CodeBlock`, `CodeEditor`,
 * `DiffViewer`). Not for app code — these primitives compose it so the card shell, header bar,
 * tone tokens, mono font, and copy button stay byte-identical across all three.
 *
 * Owns:
 *   - `TONE_TOKENS` / `ToneTokens` — the light/dark surface palette (single source of truth).
 *   - `CodeSurface` — the outer card (bg / border / radius / overflow / relative).
 *   - `SurfaceHeader` / `SurfaceTitle` / `SurfaceLangLabel` — the header bar + its slots.
 *   - `CopyButton` (typed) and `MONO_FONT`.
 *
 * What it does NOT own: the bodies (`<pre>` / CodeMirror / diff rows), inline chips, language
 * extensions, syntax highlighting — those stay in each primitive.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import CopyButtonRaw from '@shared/buttons/CopyButton';

export type CodeSurfaceTone = 'light' | 'dark';

export interface ToneTokens {
  bg: string;
  border: string;
  headerBg: string;
  text: string;
  gutterText: string;
  promptText: string;
  titleText: string;
}

/**
 * The light/dark surface palette shared by every DS code surface. Change a tone here and
 * `CodeBlock`, `CodeEditor`, and `DiffViewer` all retone together.
 */
export const TONE_TOKENS: Record<CodeSurfaceTone, ToneTokens> = {
  light: {
    bg: 'var(--ds-background-200)',
    border: 'var(--ds-gray-300)',
    headerBg: 'var(--ds-background-300)',
    text: 'var(--ds-gray-700)',
    gutterText: 'var(--ds-gray-400)',
    promptText: 'var(--ds-gray-400)',
    titleText: 'var(--ds-gray-600)',
  },
  dark: {
    // No dark-surface token exists in the DS yet; terminal surfaces use these locals.
    bg: '#1e1e2e',
    border: '#313244',
    headerBg: '#181825',
    text: '#cdd6f4',
    gutterText: '#6c7086',
    promptText: '#89b4fa',
    titleText: '#a6adc8',
  },
};

export const MONO_FONT = 'var(--ds-font-mono)';

// CopyButton is an untyped .jsx — re-type so callers can pass the optional toast message.
export const CopyButton = CopyButtonRaw as React.ComponentType<{
  text: string;
  size?: 'xs' | 'sm' | 'md' | 'lg';
  tone?: string;
  toastMessage?: string;
}>;

/** The outer card. `relative` is on so a primitive can float a copy button over the body. */
export function CodeSurface({
  tone,
  borderColor,
  children,
  className,
  id,
  'data-testid': dataTestId,
}: {
  tone: CodeSurfaceTone;
  /** Override the border colour (e.g. an error red). Defaults to the tone's border. */
  borderColor?: string;
  children: React.ReactNode;
  className?: string;
  id?: string;
  'data-testid'?: string;
}) {
  const cfg = TONE_TOKENS[tone];
  return (
    <Box
      id={id}
      className={className}
      data-testid={dataTestId}
      sx={{
        position: 'relative',
        backgroundColor: cfg.bg,
        border: `1px solid ${borderColor ?? cfg.border}`,
        borderRadius: 'var(--ds-radius-md)',
        overflow: 'hidden',
        fontFamily: MONO_FONT,
      }}
    >
      {children}
    </Box>
  );
}

/** The 32px header bar (title left, trailing right). `divided` draws the bottom border. */
export function SurfaceHeader({
  tone,
  onClick,
  interactive,
  divided = true,
  children,
}: {
  tone: CodeSurfaceTone;
  onClick?: () => void;
  /** Show a pointer cursor (the header toggles something). */
  interactive?: boolean;
  /** Draw the bottom border. Default true; pass false when the body below is collapsed. */
  divided?: boolean;
  children: React.ReactNode;
}) {
  const cfg = TONE_TOKENS[tone];
  return (
    <Box
      onClick={onClick}
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-2)',
        minHeight: '32px',
        padding: '0 var(--ds-space-2) 0 var(--ds-space-3)',
        backgroundColor: cfg.headerBg,
        borderBottom: divided ? `1px solid ${cfg.border}` : 'none',
        ...(interactive ? { cursor: 'pointer' } : null),
      }}
    >
      {children}
    </Box>
  );
}

/** Header title slot — mono, ellipsised, flex-grows to push trailing content right. */
export function SurfaceTitle({ tone, children }: { tone: CodeSurfaceTone; children: React.ReactNode }) {
  const cfg = TONE_TOKENS[tone];
  return (
    <Box
      component='span'
      sx={{
        flex: 1,
        minWidth: 0,
        fontFamily: MONO_FONT,
        fontSize: 'var(--ds-text-small)',
        fontWeight: 'var(--ds-font-weight-medium)',
        color: cfg.titleText,
        overflow: 'hidden',
        textOverflow: 'ellipsis',
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </Box>
  );
}

/** Right-side language label — uppercase caption. */
export function SurfaceLangLabel({ tone, children }: { tone: CodeSurfaceTone; children: React.ReactNode }) {
  const cfg = TONE_TOKENS[tone];
  return (
    <Box
      component='span'
      sx={{
        fontFamily: MONO_FONT,
        fontSize: 'var(--ds-text-caption)',
        fontWeight: 'var(--ds-font-weight-medium)',
        letterSpacing: '0.04em',
        textTransform: 'uppercase',
        color: cfg.gutterText,
        flexShrink: 0,
      }}
    >
      {children}
    </Box>
  );
}
