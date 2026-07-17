/**
 * CodeEditor — DS V2 (canonical / new).
 * Spec: app/design-system/primitives/content/code-editor.html (spec artifact not checked in yet)
 *
 * The **editable / language-aware** sibling of `CodeBlock`. It wraps CodeMirror
 * (`@uiw/react-codemirror`) so code is syntax-aware and (optionally) editable, while sharing
 * `CodeBlock`'s exact surface — same border / radius / tone tokens and the same optional header
 * (title · language · copy). Use it anywhere the app currently hand-wires a raw `<CodeMirror>`.
 *
 * CodeBlock vs CodeEditor:
 *   - `CodeBlock`  — static `<pre>`. Show a snippet/command, copy it. No syntax highlighting.
 *   - `CodeEditor` — CodeMirror. Edit code, OR display read-only code with syntax highlighting,
 *                    line numbers, folding, and language awareness (YAML/JSON/SQL/JS/Shell/MD).
 *
 * Variants:
 *   tone      = 'light' (default — neutral surface) | 'dark' (terminal aesthetic)
 *   readOnly  = false (default, editable) | true (selectable viewer; copy shown by default)
 *
 * Escape hatch:
 *   `extensions` is appended after the built-in language + theme, for cases the `language` union
 *   doesn't cover — e.g. PromQL autocomplete + lint (`@prometheus-io/codemirror-promql`).
 *c
 * Don't:
 *   - Don't use `CodeEditor` just to *display* a snippet with a copy button — that's `CodeBlock`
 *     (lighter, no CodeMirror). Reach here only when you need editing or language highlighting.
 *   - Don't pass markdown prose that *contains* fenced code — that's `Markdown`.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import CodeMirror from '@uiw/react-codemirror';
import { EditorView } from '@codemirror/view';
import type { Extension } from '@codemirror/state';
import { yaml } from '@codemirror/lang-yaml';
import { json } from '@codemirror/lang-json';
import { sql } from '@codemirror/lang-sql';
import { javascript } from '@codemirror/lang-javascript';
import { markdown } from '@codemirror/lang-markdown';
import { StreamLanguage } from '@codemirror/language';
import { shell } from '@codemirror/legacy-modes/mode/shell';
import { CodeSurface, SurfaceHeader, SurfaceTitle, SurfaceLangLabel, CopyButton, MONO_FONT, TONE_TOKENS } from './internal/CodeSurface';

export type CodeEditorTone = 'light' | 'dark';
export type CodeEditorLanguage = 'yaml' | 'json' | 'sql' | 'javascript' | 'shell' | 'markdown' | 'text';

export interface CodeEditorProps {
  /** Current editor value. */
  value: string;
  /** Change handler. Omit (or set `readOnly`) for a display-only viewer. */
  onChange?: (value: string) => void;
  /** Syntax mode. 'text' = no language extension. Default 'text'. */
  language?: CodeEditorLanguage;
  /** Read-only (selectable) viewer. Default false. */
  readOnly?: boolean;
  /** 'light' (default) or 'dark' (terminal look) — shares CodeBlock's surface tokens. */
  tone?: CodeEditorTone;
  /** Fixed editor height (px or CSS length). Default '300px'. */
  height?: number | string;
  minHeight?: number | string;
  maxHeight?: number | string;
  /** Filename / caption shown on the left of the header. */
  title?: string;
  /** Override the right-side language label. Defaults to the uppercased `language`. */
  languageLabel?: string;
  /** Show the language label in the header. Default true when a non-'text' language is set. */
  showLanguageLabel?: boolean;
  /** Show the copy button. Default = `readOnly` (viewers get copy; editors don't). */
  showCopy?: boolean;
  /** Success toast message shown after copying. */
  copyToast?: string;
  /** Line-number gutter. Default true. */
  lineNumbers?: boolean;
  /** Fold gutter. Default true. */
  foldGutter?: boolean;
  placeholder?: string;
  /** Red border (and message, if a string) for an invalid value. */
  error?: string | boolean;
  /** Extra CodeMirror extensions appended after language + theme (PromQL, lint, autocomplete…). */
  extensions?: Extension[];
  className?: string;
  id?: string;
  'data-testid'?: string;
  ariaLabel?: string;
}

/** Maps a `CodeEditorLanguage` to its CodeMirror extension(s). Shared with `ds/DiffViewer`. */
export function languageExtension(language: CodeEditorLanguage): Extension[] {
  switch (language) {
    case 'yaml':
      return [yaml()];
    case 'json':
      return [json()];
    case 'sql':
      return [sql()];
    case 'javascript':
      return [javascript()];
    case 'markdown':
      return [markdown()];
    case 'shell':
      return [StreamLanguage.define(shell)];
    case 'text':
    default:
      return [];
  }
}

export function CodeEditor({
  value,
  onChange,
  language = 'text',
  readOnly = false,
  tone = 'light',
  height = '300px',
  minHeight,
  maxHeight,
  title,
  languageLabel,
  showLanguageLabel,
  showCopy,
  copyToast,
  lineNumbers = true,
  foldGutter = true,
  placeholder,
  error,
  extensions,
  className,
  id,
  'data-testid': dataTestId,
  ariaLabel,
}: CodeEditorProps) {
  const cfg = TONE_TOKENS[tone];
  const editable = !readOnly;
  const copyEnabled = showCopy ?? readOnly;
  const langLabelShown = (showLanguageLabel ?? language !== 'text') && language !== 'text';
  const label = languageLabel ?? language.toUpperCase();
  const hasHeader = Boolean(title || langLabelShown);

  // Token-driven CodeMirror theme so the editor body matches the DS surface exactly. The built-in
  // dark theme is `oneDark`, whose `&`/gutter backgrounds (`#282c34`) otherwise win the cascade and
  // make the editor a different shade than CodeBlock / the header. We force OUR `cfg.bg` with
  // `!important` so the surface is identical across both tones (oneDark's syntax token colours,
  // which are foreground-only, still apply on top).
  const cmTheme = React.useMemo(
    () =>
      EditorView.theme(
        {
          '&': { backgroundColor: `${cfg.bg} !important`, fontSize: 'var(--ds-text-small)' },
          '&.cm-focused': { outline: 'none' },
          '.cm-scroller': { fontFamily: MONO_FONT, lineHeight: '1.6' },
          '.cm-content': { fontFamily: MONO_FONT, color: cfg.text },
          '.cm-gutters': { backgroundColor: `${cfg.bg} !important`, border: 'none', color: cfg.gutterText, fontFamily: MONO_FONT },
          '.cm-activeLine': { backgroundColor: readOnly ? 'transparent' : tone === 'dark' ? 'var(--ds-brand-600)' : 'var(--ds-gray-alpha-100)' },
          '.cm-activeLineGutter': { backgroundColor: 'transparent', color: cfg.titleText },
          '.cm-cursor': { borderLeftColor: cfg.text },
          '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {
            backgroundColor: tone === 'dark' ? 'var(--ds-brand-500)' : 'var(--ds-blue-200)',
          },
          '.cm-placeholder': { color: cfg.gutterText },
        },
        { dark: tone === 'dark' }
      ),
    [tone, readOnly, cfg]
  );

  const allExtensions = React.useMemo(() => [...languageExtension(language), cmTheme, ...(extensions ?? [])], [language, cmTheme, extensions]);

  const copyNode = copyEnabled ? <CopyButton text={value} size='sm' toastMessage={copyToast} /> : null;
  const errorMessage = typeof error === 'string' ? error : null;

  return (
    <Box id={id} className={className} data-testid={dataTestId}>
      <CodeSurface tone={tone} borderColor={error ? 'var(--ds-red-500)' : undefined}>
        {hasHeader && (
          <SurfaceHeader tone={tone}>
            {title ? <SurfaceTitle tone={tone}>{title}</SurfaceTitle> : <Box sx={{ flex: 1 }} />}
            {langLabelShown && <SurfaceLangLabel tone={tone}>{label}</SurfaceLangLabel>}
            {copyNode}
          </SurfaceHeader>
        )}

        {/* Floating copy when there's no header to host it (read-only viewers). */}
        {!hasHeader && copyEnabled && (
          <Box sx={{ position: 'absolute', top: 'var(--ds-space-1)', right: 'var(--ds-space-1)', zIndex: 2 }}>{copyNode}</Box>
        )}

        <CodeMirror
          value={value}
          onChange={editable ? onChange : undefined}
          editable={editable}
          readOnly={readOnly}
          height={typeof height === 'number' ? `${height}px` : height}
          minHeight={typeof minHeight === 'number' ? `${minHeight}px` : minHeight}
          maxHeight={typeof maxHeight === 'number' ? `${maxHeight}px` : maxHeight}
          theme={tone === 'dark' ? 'dark' : 'light'}
          placeholder={placeholder}
          aria-label={ariaLabel ?? title ?? 'Code editor'}
          extensions={allExtensions}
          basicSetup={{
            lineNumbers,
            foldGutter,
            highlightActiveLine: editable,
            highlightActiveLineGutter: editable,
            dropCursor: editable,
            allowMultipleSelections: false,
            indentOnInput: editable,
            bracketMatching: true,
            closeBrackets: editable,
          }}
        />
      </CodeSurface>
      {errorMessage && <Box sx={{ mt: 'var(--ds-space-1)', fontSize: 'var(--ds-text-small)', color: 'var(--ds-red-500)' }}>{errorMessage}</Box>}
    </Box>
  );
}

export default CodeEditor;
