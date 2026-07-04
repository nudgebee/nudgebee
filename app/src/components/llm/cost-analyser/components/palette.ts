/**
 * Shared colour palette. Charts use a soft **pastel** ramp (per design feedback)
 * so the surface reads calm and modern rather than neon. Order of preference:
 *   sky blue · sage green · lavender · blush · aqua · olive · mauve · slate.
 *
 * Canvas (Chart.js) can't read CSS vars, so chart series use resolved pastel
 * hex here; chips keep the DS token-based `MODEL_HUE`.
 */

/**
 * Series ramp — a richer pastel (deeper, more saturated than the original wash)
 * so series read with presence against the white card without going neon. Same
 * hue family / order of preference as before.
 */
export const PASTEL_PALETTE = [
  '#5B9BD5', // sky blue
  '#70AD63', // sage green
  '#9B79D0', // lavender / violet
  '#DB7C92', // dusty rose / blush
  '#3FB8AE', // teal / aqua
  '#A6B23C', // olive green
  '#C66BA6', // mauve pink
  '#6E86A8', // slate blue grey
];

/** hex (#rrggbb) → rgba string with the given alpha. For chart fills/gradients. */
export function withAlpha(hex: string, alpha: number): string {
  const h = hex.replace('#', '');
  const r = parseInt(h.slice(0, 2), 16);
  const g = parseInt(h.slice(2, 4), 16);
  const b = parseInt(h.slice(4, 6), 16);
  return `rgba(${r}, ${g}, ${b}, ${alpha})`;
}

const MODEL_ORDER = ['claude-opus-4', 'claude-haiku-4', 'gpt-4o', 'gemini-2.5-pro', 'titan-text-bedrock', 'llama-3-70b-self'];

/** Stable pastel per known model (falls back to ramp by index). */
export const MODEL_COLORS: Record<string, string> = MODEL_ORDER.reduce<Record<string, string>>((acc, m, i) => {
  acc[m] = PASTEL_PALETTE[i % PASTEL_PALETTE.length];
  return acc;
}, {});

export function modelColor(model: string, index = 0): string {
  return MODEL_COLORS[model] ?? PASTEL_PALETTE[index % PASTEL_PALETTE.length];
}

export function seriesColor(key: string, index: number): string {
  return MODEL_COLORS[key] ?? PASTEL_PALETTE[index % PASTEL_PALETTE.length];
}

/** Categorical chip hue per model — DS token families (used by Chip, not canvas). */
export const MODEL_HUE: Record<string, 'violet' | 'blue' | 'green' | 'teal' | 'amber' | 'slate'> = {
  'claude-opus-4': 'blue',
  'claude-haiku-4': 'green',
  'gpt-4o': 'violet',
  'gemini-2.5-pro': 'teal',
  'titan-text-bedrock': 'amber',
  'llama-3-70b-self': 'slate',
};
