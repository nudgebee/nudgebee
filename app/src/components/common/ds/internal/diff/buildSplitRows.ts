/**
 * Split-layout row building for DiffViewer's side-by-side layout — the old↔new line-pairing the
 * CodeMirror MergeView used to do. Removed lines pair against the adjacent added lines so a change
 * shows the old text on the left against the new text on the right.
 */
import { diffLines } from 'diff';
import type { SideCellData, SplitRow } from './types';

/** Split a diff part's value into lines, dropping the trailing '' that a final newline produces. */
function partLines(value: string): string[] {
  const lines = value.split('\n');
  if (lines.length > 0 && lines[lines.length - 1] === '') {
    lines.pop();
  }
  return lines;
}

export function buildSplitRows(original: string, modified: string): SplitRow[] {
  const parts = diffLines(original, modified);
  const rows: SplitRow[] = [];
  let oldNo = 0;
  let newNo = 0;

  for (let i = 0; i < parts.length; i++) {
    const part = parts[i];
    const lines = partLines(part.value);

    if (!part.added && !part.removed) {
      for (const content of lines) {
        oldNo++;
        newNo++;
        rows.push({
          left: { lineNo: oldNo, marker: ' ', content, kind: 'context' },
          right: { lineNo: newNo, marker: ' ', content, kind: 'context' },
        });
      }
    } else if (part.removed && parts[i + 1]?.added) {
      // changed block: line up deletions (left) against the following additions (right)
      const rightLines = partLines(parts[i + 1].value);
      const max = Math.max(lines.length, rightLines.length);
      for (let k = 0; k < max; k++) {
        let left: SideCellData | null = null;
        let right: SideCellData | null = null;
        if (k < lines.length) {
          oldNo++;
          left = { lineNo: oldNo, marker: '-', content: lines[k], kind: 'delete' };
        }
        if (k < rightLines.length) {
          newNo++;
          right = { lineNo: newNo, marker: '+', content: rightLines[k], kind: 'add' };
        }
        rows.push({ left, right });
      }
      i++; // consume the paired added part
    } else if (part.removed) {
      for (const content of lines) {
        oldNo++;
        rows.push({ left: { lineNo: oldNo, marker: '-', content, kind: 'delete' }, right: null });
      }
    } else {
      for (const content of lines) {
        newNo++;
        rows.push({ left: null, right: { lineNo: newNo, marker: '+', content, kind: 'add' } });
      }
    }
  }

  return rows;
}
