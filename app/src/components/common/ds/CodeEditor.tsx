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
      // `shell`'s StreamParser (from @codemirror/legacy-modes) is structurally identical but
      // nominally distinct from StreamLanguage.define()'s own -- cast is intentional.
      return [StreamLanguage.define(shell as unknown as Parameters<typeof StreamLanguage.define>[0])];
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
  const cmTheme = React.useMemo(() => {
    // Search-panel controls. Light mirrors `ds/Button` tone="secondary" (white / brand-600 /
    // brand-200); dark has no DS surface tokens yet, so it reuses this surface's own locals.
    const panelBtn =
      tone === 'dark'
        ? { bg: cfg.bg, bgHover: cfg.border, bgActive: cfg.headerBg, text: cfg.text, border: cfg.border }
        : {
            bg: 'var(--ds-background-100)',
            bgHover: 'var(--ds-brand-100)',
            bgActive: 'var(--ds-brand-200)',
            text: 'var(--ds-brand-600)',
            border: 'var(--ds-brand-200)',
          };
    const panelField = tone === 'dark' ? cfg.bg : 'var(--ds-background-100)';

    return EditorView.theme(
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

        // --- Search / replace panel (Cmd-F) ---------------------------------------------------
        // `@codemirror/search` builds its own panel out of raw `input` / `button` / `label`, so DS
        // primitives can't be slotted in — we restyle its DOM instead. Selectors carry the extra
        // `&.cm-editor` class because the stock look comes from `&light`/`&dark` base-theme rules,
        // which already have three classes of specificity; `.cm-panels` itself still needs
        // `!important` (same reason as the surface rules above).
        '.cm-panels': { backgroundColor: `${cfg.headerBg} !important`, color: `${cfg.text} !important`, fontFamily: 'var(--ds-font-sans)' },
        '.cm-panels-bottom': { borderTop: `1px solid ${cfg.border} !important` },
        '.cm-panels-top': { borderBottom: `1px solid ${cfg.border} !important` },
        '&.cm-editor .cm-panel.cm-search': {
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
          // Extra right padding keeps the first row clear of the absolutely-positioned close (×).
          padding: 'var(--ds-space-2) var(--ds-space-5) var(--ds-space-2) var(--ds-space-2)',
        },
        // The panel separates Find from Replace with a bare `<br>`. As a full-basis flex item it
        // collapses when the whole row fits and becomes the wrap point when it doesn't — so a
        // narrow panel breaks between the Find and Replace groups instead of mid-group.
        '&.cm-editor .cm-panel.cm-search br': { flexBasis: '100%', height: '0px', lineHeight: 0, fontSize: 0, margin: 0 },
        '&.cm-editor .cm-panel.cm-search input, &.cm-editor .cm-panel.cm-search button, &.cm-editor .cm-panel.cm-search label': {
          margin: 0,
        },
        '&.cm-editor .cm-panel.cm-search .cm-textfield': {
          boxSizing: 'border-box',
          height: '28px',
          width: '180px',
          padding: '0 var(--ds-space-2)',
          fontFamily: 'var(--ds-font-sans)',
          fontSize: 'var(--ds-text-small)',
          color: cfg.text,
          backgroundColor: panelField,
          border: `1px solid ${cfg.border}`,
          borderRadius: 'var(--ds-radius-sm)',
          outline: 'none',
        },
        '&.cm-editor .cm-panel.cm-search .cm-textfield:focus': { borderColor: 'var(--ds-brand-500)' },
        '&.cm-editor .cm-panel.cm-search .cm-textfield::placeholder': { color: cfg.gutterText },
        '&.cm-editor .cm-panel.cm-search .cm-button': {
          boxSizing: 'border-box',
          height: '28px',
          padding: '0 var(--ds-space-3)',
          backgroundImage: 'none',
          backgroundColor: panelBtn.bg,
          color: panelBtn.text,
          border: `1px solid ${panelBtn.border}`,
          borderRadius: 'var(--ds-radius-md)',
          fontFamily: 'var(--ds-font-sans)',
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-medium)',
          cursor: 'pointer',
        },
        '&.cm-editor .cm-panel.cm-search .cm-button:hover': { backgroundColor: panelBtn.bgHover },
        '&.cm-editor .cm-panel.cm-search .cm-button:active': { backgroundImage: 'none', backgroundColor: panelBtn.bgActive },
        '&.cm-editor .cm-panel.cm-search label': {
          display: 'inline-flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          fontFamily: 'var(--ds-font-sans)',
          fontSize: 'var(--ds-text-small)',
          color: cfg.titleText,
          whiteSpace: 'nowrap',
          cursor: 'pointer',
        },
        '&.cm-editor .cm-panel.cm-search input[type=checkbox]': {
          width: '14px',
          height: '14px',
          accentColor: 'var(--ds-brand-500)',
          cursor: 'pointer',
        },
        '&.cm-editor .cm-panel.cm-search [name=close]': {
          position: 'absolute',
          top: 'var(--ds-space-1)',
          right: 'var(--ds-space-1)',
          width: '20px',
          height: '20px',
          padding: 0,
          border: 'none',
          backgroundColor: 'transparent',
          color: cfg.gutterText,
          fontSize: 'var(--ds-text-title)',
          lineHeight: 1,
          cursor: 'pointer',
        },
        '&.cm-editor .cm-panel.cm-search [name=close]:hover': { color: cfg.text },
      },
      { dark: tone === 'dark' }
    );
  }, [tone, readOnly, cfg]);

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
