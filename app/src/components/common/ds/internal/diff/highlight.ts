/**
 * Per-line syntax highlighting for DiffViewer, via Prism. Kept separate so the language grammars
 * are imported once and the component file stays focused on layout.
 */
import Prism from 'prismjs';
import 'prismjs/components/prism-json';
import 'prismjs/components/prism-yaml';
import 'prismjs/components/prism-bash';
import 'prismjs/components/prism-sql';
import 'prismjs/components/prism-markdown';
import type { CodeEditorLanguage } from '../../CodeEditor';
import type { DiffViewerTone } from './types';

// CodeEditor languages → Prism grammar names. 'text' (and anything unmapped) = no highlighting.
const PRISM_LANG: Partial<Record<CodeEditorLanguage, string>> = {
  yaml: 'yaml',
  json: 'json',
  sql: 'sql',
  javascript: 'javascript',
  shell: 'bash',
  markdown: 'markdown',
};

/** Returns a `dangerouslySetInnerHTML` payload for a Prism-highlighted line, or null to render raw. */
export function highlightLine(content: string, language: CodeEditorLanguage): { __html: string } | null {
  const lang = PRISM_LANG[language];
  if (!lang || !content || !Prism.languages[lang]) {
    return null;
  }
  try {
    return { __html: Prism.highlight(content, Prism.languages[lang], lang) };
  } catch {
    return null;
  }
}

/** Per-tone Prism token colours, applied via `& .token.*` on the diff body container. Blue-biased
 *  on purpose so highlighted keys/strings never clash with the red/green diff row backgrounds. */
export function tokenThemeSx(tone: DiffViewerTone) {
  const c =
    tone === 'dark'
      ? { keyword: '#ff7b72', keyProp: '#79c0ff', string: '#a5d6ff', number: '#79c0ff', comment: '#8b949e' }
      : { keyword: '#cf222e', keyProp: '#0550ae', string: '#0a3069', number: '#0550ae', comment: '#6e7781' };
  return {
    '& .token.property, & .token.key, & .token.tag, & .token.attr-name, & .token.selector, & .token.atrule': { color: c.keyProp },
    '& .token.string, & .token.attr-value, & .token.char, & .token.regex': { color: c.string },
    '& .token.number, & .token.boolean, & .token.constant, & .token.symbol': { color: c.number },
    '& .token.comment, & .token.prolog, & .token.doctype, & .token.cdata': { color: c.comment, fontStyle: 'italic' },
    '& .token.keyword, & .token.operator, & .token.important': { color: c.keyword },
    '& .token.punctuation': { color: 'inherit' },
  } as const;
}
