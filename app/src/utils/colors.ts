/**
 * Design System tokens (--ds-*).
 * Source of truth: app/design-system/shared/styles.css
 */
export const ds = {
  background: {
    100: 'var(--ds-background-100)',
    200: 'var(--ds-background-200)',
    300: 'var(--ds-background-300)',
  },
  gray: {
    100: 'var(--ds-gray-100)',
    200: 'var(--ds-gray-200)',
    300: 'var(--ds-gray-300)',
    400: 'var(--ds-gray-400)',
    500: 'var(--ds-gray-500)',
    600: 'var(--ds-gray-600)',
    700: 'var(--ds-gray-700)',
    alpha: {
      100: 'var(--ds-gray-alpha-100)',
      200: 'var(--ds-gray-alpha-200)',
      300: 'var(--ds-gray-alpha-300)',
      400: 'var(--ds-gray-alpha-400)',
      500: 'var(--ds-gray-alpha-500)',
      600: 'var(--ds-gray-alpha-600)',
      700: 'var(--ds-gray-alpha-700)',
    },
  },
  blue: {
    100: 'var(--ds-blue-100)',
    200: 'var(--ds-blue-200)',
    300: 'var(--ds-blue-300)',
    400: 'var(--ds-blue-400)',
    500: 'var(--ds-blue-500)',
    600: 'var(--ds-blue-600)',
    700: 'var(--ds-blue-700)',
  },
  red: {
    100: 'var(--ds-red-100)',
    200: 'var(--ds-red-200)',
    300: 'var(--ds-red-300)',
    400: 'var(--ds-red-400)',
    500: 'var(--ds-red-500)',
    600: 'var(--ds-red-600)',
    700: 'var(--ds-red-700)',
  },
  amber: {
    100: 'var(--ds-amber-100)',
    200: 'var(--ds-amber-200)',
    300: 'var(--ds-amber-300)',
    400: 'var(--ds-amber-400)',
    500: 'var(--ds-amber-500)',
    600: 'var(--ds-amber-600)',
    700: 'var(--ds-amber-700)',
  },
  green: {
    100: 'var(--ds-green-100)',
    200: 'var(--ds-green-200)',
    300: 'var(--ds-green-300)',
    400: 'var(--ds-green-400)',
    500: 'var(--ds-green-500)',
    600: 'var(--ds-green-600)',
    700: 'var(--ds-green-700)',
  },
  teal: {
    50: 'var(--ds-teal-50)',
    100: 'var(--ds-teal-100)',
    200: 'var(--ds-teal-200)',
    300: 'var(--ds-teal-300)',
    400: 'var(--ds-teal-400)',
    500: 'var(--ds-teal-500)',
    600: 'var(--ds-teal-600)',
    700: 'var(--ds-teal-700)',
  },
  purple: {
    100: 'var(--ds-purple-100)',
    200: 'var(--ds-purple-200)',
    300: 'var(--ds-purple-300)',
    400: 'var(--ds-purple-400)',
    500: 'var(--ds-purple-500)',
    600: 'var(--ds-purple-600)',
    700: 'var(--ds-purple-700)',
  },
  pink: {
    100: 'var(--ds-pink-100)',
    200: 'var(--ds-pink-200)',
    300: 'var(--ds-pink-300)',
    400: 'var(--ds-pink-400)',
    500: 'var(--ds-pink-500)',
    600: 'var(--ds-pink-600)',
    700: 'var(--ds-pink-700)',
  },
  /** Brand navy ("Sidebar Dark"), anchored at #1B2D4A.
   *  Use 600 for primary-button rest, 500 for hover, 700 for active. 100/200
   *  for selected-row tints and dividers. Never tone destructive / cost-axis
   *  surfaces with brand. */
  brand: {
    100: 'var(--ds-brand-100)',
    150: 'var(--ds-brand-150)',
    200: 'var(--ds-brand-200)',
    300: 'var(--ds-brand-300)',
    400: 'var(--ds-brand-400)',
    500: 'var(--ds-brand-500)',
    600: 'var(--ds-brand-600)',
    700: 'var(--ds-brand-700)',
  },
  /** Brand yellow ("Nudgebee Yellow"), anchored at #FACF39.
   *  Reserved for focus rings, brand sigil bg, marketing accents. Never a
   *  status / warning colour — that's amber. */
  yellow: {
    100: 'var(--ds-yellow-100)',
    200: 'var(--ds-yellow-200)',
    300: 'var(--ds-yellow-300)',
    400: 'var(--ds-yellow-400)',
    500: 'var(--ds-yellow-500)',
    600: 'var(--ds-yellow-600)',
    700: 'var(--ds-yellow-700)',
  },
  text: {
    caption: 'var(--ds-text-caption)',
    small: 'var(--ds-text-small)',
    body: 'var(--ds-text-body)',
    bodyLg: 'var(--ds-text-body-lg)',
    title: 'var(--ds-text-title)',
    heading: 'var(--ds-text-heading)',
    display: 'var(--ds-text-display)',
  },
  weight: {
    regular: 'var(--ds-font-weight-regular)',
    medium: 'var(--ds-font-weight-medium)',
    semibold: 'var(--ds-font-weight-semibold)',
  },
  font: {
    sans: 'var(--ds-font-sans)',
    mono: 'var(--ds-font-mono)',
  },
  space: {
    0: 'var(--ds-space-0)',
    1: 'var(--ds-space-1)',
    2: 'var(--ds-space-2)',
    3: 'var(--ds-space-3)',
    4: 'var(--ds-space-4)',
    5: 'var(--ds-space-5)',
    6: 'var(--ds-space-6)',
    7: 'var(--ds-space-7)',
    mul: (step: 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7, multiplier: number): string => `calc(var(--ds-space-${step}) * ${multiplier})`,
  },
  radius: {
    sm: 'var(--ds-radius-sm)',
    md: 'var(--ds-radius-md)',
    lg: 'var(--ds-radius-lg)',
    xl: 'var(--ds-radius-xl)',
    pill: 'var(--ds-radius-pill)',
  },
  motion: {
    micro: 'var(--ds-motion-micro)',
    panel: 'var(--ds-motion-panel)',
    ease: 'var(--ds-motion-ease)',
  },
} as const;

/**
 * Resolve a CSS custom property to its computed value.
 * Use in canvas/Chart.js contexts where CSS var() strings won't work.
 * Accepts both "var(--nb-color-x)" and "--nb-color-x" formats.
 * Non-variable strings (e.g. "#FF0000", "red") are returned as-is.
 */
export function resolveColor(value: string): string {
  if (!value) return value;
  const varMatch = /^var\((--[^)]+)\)$/.exec(value);
  let prop: string | null = null;
  if (varMatch) {
    prop = varMatch[1];
  } else if (value.startsWith('--')) {
    prop = value;
  }
  if (!prop) return value; // already a resolved color
  if (typeof document === 'undefined') return value; // return original during SSR
  return getComputedStyle(document.documentElement).getPropertyValue(prop).trim() || value;
}

/**
 * Resolve an array of color values for use with Chart.js canvas rendering.
 */
export function resolveColors(values: string[]): string[] {
  return values.map(resolveColor);
}

/**
 * Apply alpha/opacity to any resolved color string.
 * Handles hex (#RGB, #RRGGBB, #RRGGBBAA), rgb(), and rgba() formats.
 * Use this instead of string concatenation (e.g. color + '70') which breaks with rgb() values.
 */
export function withAlpha(color: string, alpha: number): string {
  if (!color) return color;
  // Handle rgb/rgba
  const rgbMatch = /^rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(color);
  if (rgbMatch) {
    return `rgba(${rgbMatch[1]}, ${rgbMatch[2]}, ${rgbMatch[3]}, ${alpha})`;
  }
  // Handle hex
  if (color.startsWith('#')) {
    const hex = color.slice(1);
    let r: number, g: number, b: number;
    if (hex.length === 3) {
      r = parseInt(hex[0] + hex[0], 16);
      g = parseInt(hex[1] + hex[1], 16);
      b = parseInt(hex[2] + hex[2], 16);
    } else {
      r = parseInt(hex.slice(0, 2), 16);
      g = parseInt(hex.slice(2, 4), 16);
      b = parseInt(hex.slice(4, 6), 16);
    }
    return `rgba(${r}, ${g}, ${b}, ${alpha})`;
  }
  return color;
}
