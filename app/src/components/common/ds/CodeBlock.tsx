/**
 * CodeBlock — DS V2 (canonical / new).
 * Spec: app/design-system/primitives/content/code-block.html (spec artifact not checked in yet)
 *
 * The lightweight primitive for **displaying** a static code snippet, shell command,
 * config fragment, or any monospace text — with a copy affordance, optional filename/
 * language header, optional line numbers, and an optional command prompt prefix.
 *
 * This fills the gap between the two heavier code surfaces already in the app:
 *   - `Markdown` (`@shared/viewers/MarkDowns`) — for prose that *contains* fenced code.
 *   - a read-only CodeMirror instance — for editable / language-aware code.
 * Reach for `CodeBlock` when you just need to *show a snippet and let the user copy it*.
 *
 * Variants:
 *   inline    = false (default, full block) | true (an inline `<code>` chip in running text)
 *   tone      = 'light' (default — neutral surface) | 'dark' (terminal aesthetic)
 *
 * Anatomy (block):
 *   ┌───────────────────────────────────────────────┐
 *   │ title.yaml                  language   [copy]   │  ← optional header (any of the three)
 *   ├───────────────────────────────────────────────┤
 *   │ 1 │ apiVersion: v1                              │  ← optional line-number gutter
 *   │ 2 │ kind: Pod                                   │
 *   └───────────────────────────────────────────────┘
 *   When there is no header, the copy button floats top-right over the body.
 *
 * Don't:
 *   - Don't use `CodeBlock` to render markdown / prose with embedded code — that's `Markdown`.
 *   - Don't use it as a code *editor* — it's display-only. Use a read-only CodeMirror for editing.
 *   - Don't hand-roll a `<pre>` + copy button elsewhere — compose this instead.
 *   - Don't include the prompt char (`$`) inside `code` when using the `prompt` prop — the
 *     prefix is decorative and excluded from copy; baking it in makes copied text un-runnable.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { CodeSurface, SurfaceHeader, SurfaceTitle, SurfaceLangLabel, CopyButton, MONO_FONT, TONE_TOKENS } from './internal/CodeSurface';

// Re-exported for backward compat — these now live in internal/CodeSurface (shared with CodeEditor
// + DiffViewer). External imports of `{ TONE_TOKENS, ToneTokens }` from '@ui/CodeBlock' still work.
export { TONE_TOKENS } from './internal/CodeSurface';
export type { ToneTokens } from './internal/CodeSurface';

export type CodeBlockTone = 'light' | 'dark';

export interface CodeBlockProps {
  /** The code / command text to display (and copy). Required. */
  code: string;
  /** Language label shown on the right of the header (e.g. 'bash', 'yaml', 'json'). */
  language?: string;
  /** Filename or caption shown on the left of the header. */
  title?: string;
  /** Render as an inline `<code>` chip for use within a line of text. Ignores block-only props. */
  inline?: boolean;
  /** 'light' (default neutral surface) or 'dark' (terminal look). */
  tone?: CodeBlockTone;
  /** Show a line-number gutter. Default false. */
  showLineNumbers?: boolean;
  /** Show the copy button. Default true (always false for inline). */
  showCopy?: boolean;
  /**
   * Wrap long lines instead of horizontal scroll. Default false (horizontal scroll).
   * Turn on for commands/prose-like content; leave off for code where columns matter.
   */
  wrap?: boolean;
  /**
   * Non-selectable per-line prefix rendered before each line (e.g. '$' for shell commands).
   * Decorative only — excluded from the copied text.
   */
  prompt?: string;
  /** Clamp the body height (px or CSS length) and scroll past it. */
  maxHeight?: number | string;
  /** If provided, the copy button shows a success toast with this message. */
  copyToast?: string;
  className?: string;
  id?: string;
  'data-testid'?: string;
}

export function CodeBlock({
  code,
  language,
  title,
  inline = false,
  tone = 'light',
  showLineNumbers = false,
  showCopy = true,
  wrap = false,
  prompt,
  maxHeight,
  copyToast,
  className,
  id,
  'data-testid': dataTestId,
}: CodeBlockProps) {
  const cfg = TONE_TOKENS[tone];

  // Inline chip — a styled <code> for running text. Block-only props are ignored.
  if (inline) {
    return (
      <Box
        component='code'
        id={id}
        className={className}
        data-testid={dataTestId}
        sx={{
          fontFamily: MONO_FONT,
          fontSize: '0.875em',
          lineHeight: 1.4,
          color: tone === 'dark' ? cfg.text : 'var(--ds-gray-700)',
          backgroundColor: tone === 'dark' ? cfg.bg : 'var(--ds-gray-alpha-100)',
          border: `1px solid ${tone === 'dark' ? cfg.border : 'var(--ds-gray-200)'}`,
          borderRadius: 'var(--ds-radius-sm)',
          padding: '1px 5px',
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
        }}
      >
        {code}
      </Box>
    );
  }

  const lines = code.split('\n');
  // A header only exists when there's something to label (title/language). The copy button
  // alone does NOT make a header — otherwise a bare `<CodeBlock code>` would render an empty
  // header strip with just the copy icon. With no header, copy floats over the body instead.
  const hasHeader = Boolean(title || language);

  const copyNode = showCopy ? <CopyButton text={code} size='sm' toastMessage={copyToast} /> : null;

  return (
    <CodeSurface tone={tone} id={id} className={className} data-testid={dataTestId}>
      {hasHeader && (
        <SurfaceHeader tone={tone}>
          {title ? <SurfaceTitle tone={tone}>{title}</SurfaceTitle> : <Box sx={{ flex: 1 }} />}
          {language && <SurfaceLangLabel tone={tone}>{language}</SurfaceLangLabel>}
          {copyNode}
        </SurfaceHeader>
      )}

      {/* Floating copy button when there's no header to host it. */}
      {!hasHeader && showCopy && <Box sx={{ position: 'absolute', top: 'var(--ds-space-1)', right: 'var(--ds-space-1)', zIndex: 1 }}>{copyNode}</Box>}

      <Box
        sx={{
          overflow: 'auto',
          maxHeight: maxHeight ?? 'none',
          padding: 'var(--ds-space-2) 0',
        }}
      >
        <Box
          component='pre'
          sx={{
            margin: 0,
            fontFamily: MONO_FONT,
            fontSize: 'var(--ds-text-small)',
            lineHeight: 1.6,
            color: cfg.text,
            // pad the right so the floating copy button never overlaps the first line of code
            paddingRight: !hasHeader && showCopy ? 'var(--ds-space-7)' : 'var(--ds-space-3)',
          }}
        >
          {lines.map((line, i) => (
            <Box
              key={i}
              sx={{
                display: 'flex',
                alignItems: 'flex-start',
                whiteSpace: wrap ? 'pre-wrap' : 'pre',
              }}
            >
              {showLineNumbers && (
                <Box
                  component='span'
                  aria-hidden='true'
                  sx={{
                    flexShrink: 0,
                    position: 'sticky',
                    left: 0,
                    width: `${Math.max(2, String(lines.length).length)}ch`,
                    paddingRight: 'var(--ds-space-3)',
                    paddingLeft: 'var(--ds-space-3)',
                    textAlign: 'right',
                    color: cfg.gutterText,
                    backgroundColor: cfg.bg,
                    userSelect: 'none',
                  }}
                >
                  {i + 1}
                </Box>
              )}
              {!showLineNumbers && <Box sx={{ width: 'var(--ds-space-3)', flexShrink: 0 }} />}
              {prompt && (
                <Box
                  component='span'
                  aria-hidden='true'
                  sx={{ flexShrink: 0, paddingRight: 'var(--ds-space-2)', color: cfg.promptText, userSelect: 'none' }}
                >
                  {prompt}
                </Box>
              )}
              <Box
                component='span'
                sx={{
                  flex: 1,
                  minWidth: 0,
                  ...(wrap ? { wordBreak: 'break-word' } : null),
                }}
              >
                {/* keep blank lines from collapsing to zero height */}
                {line.length ? line : ' '}
              </Box>
            </Box>
          ))}
        </Box>
      </Box>
    </CodeSurface>
  );
}

export default CodeBlock;
