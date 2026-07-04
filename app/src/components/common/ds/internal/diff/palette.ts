/**
 * Per-tone diff colour palette for DiffViewer. Dark values are GitHub-dark-style tints over
 * CodeBlock's dark surface — no dark diff tokens exist in the DS yet, so (like CodeBlock's dark
 * tone) they're local literals.
 */
import type { DiffPalette, DiffViewerTone } from './types';

export const DIFF_PALETTE: Record<DiffViewerTone, DiffPalette> = {
  light: {
    addBg: 'var(--ds-green-200)',
    delBg: 'var(--ds-red-200)',
    addMarker: 'var(--ds-green-600)',
    delMarker: 'var(--ds-red-600)',
    ctxMarker: 'var(--ds-gray-400)',
    emptyBg: 'var(--ds-background-100)',
    hunkBg: 'var(--ds-background-100)',
  },
  dark: {
    addBg: 'rgba(63, 185, 80, 0.16)',
    delBg: 'rgba(248, 81, 73, 0.16)',
    addMarker: '#3fb950',
    delMarker: '#f85149',
    ctxMarker: '#6c7086',
    emptyBg: 'rgba(255, 255, 255, 0.03)',
    hunkBg: 'rgba(255, 255, 255, 0.05)',
  },
};
