/**
 * Unified-diff parsing for DiffViewer's stacked layout. Ported from the legacy SimpleDiffViewer:
 * turns a git-diff string into colour-coded rows + add/delete counts + an extracted filename.
 */
import type { ParsedDiff, UnifiedLine } from './types';

const extractFileName = (lines: string[], fallback: string): string => {
  for (const line of lines) {
    if (line.startsWith('diff --git')) {
      const match = line.match(/diff --git a\/(.*?) b\//);
      if (match?.[1]) {
        return match[1];
      }
    }
  }
  return fallback;
};

// Skip git/jsdiff metadata noise so only hunks + content rows render.
const isMetadataLine = (line: string): boolean =>
  line.startsWith('diff --git') ||
  line.startsWith('index ') ||
  line.startsWith('Index:') ||
  line.startsWith('===') ||
  line.startsWith('---') ||
  line.startsWith('+++');

export function parseUnifiedDiff(diff: string, fallbackName: string): ParsedDiff {
  // Split on real newlines only. We deliberately do NOT un-escape "\\n" / "\\"" here — a diff line's
  // content can legitimately contain a literal `\n` (a regex, a JS/shell string, a JSON value), and
  // globally un-escaping would split that one line into several and corrupt the diff. If an upstream
  // ever returns a double-escaped diff, normalize it at the fetch/API-client layer before passing in.
  const lines = diff.split('\n');
  const fileName = extractFileName(lines, fallbackName);
  const out: UnifiedLine[] = [];
  let additions = 0;
  let deletions = 0;
  let id = 0;

  for (const line of lines) {
    if (isMetadataLine(line)) {
      continue;
    }
    if (line.startsWith('@@')) {
      out.push({ kind: 'hunk', id: `hunk-${id++}`, content: line });
    } else if (line.startsWith('+')) {
      additions++;
      out.push({ kind: 'add', id: `l-${id++}`, marker: '+', content: line.substring(1) });
    } else if (line.startsWith('-')) {
      deletions++;
      out.push({ kind: 'delete', id: `l-${id++}`, marker: '-', content: line.substring(1) });
    } else if (line.startsWith(' ')) {
      out.push({ kind: 'context', id: `l-${id++}`, marker: ' ', content: line.substring(1) });
    } else if (line.trim()) {
      out.push({ kind: 'context', id: `l-${id++}`, marker: ' ', content: line });
    }
  }

  return { fileName, additions, deletions, lines: out };
}
