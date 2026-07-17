/**
 * Jest manual mock for src/utils/colors.
 *
 * Covers every ds token consumed across the test suite so individual test
 * files can drop their inline factory functions and use:
 *
 *   jest.mock('src/utils/colors');
 *   jest.mock('@utils/colors');     // if the component under test uses the alias
 *
 * Keep this in sync with src/utils/colors.ts whenever new token groups are added.
 */

const makeScale = (base: Record<number, string>) => base;

const space = Object.assign(
  { 0: '2px', 1: '4px', 2: '8px', 3: '12px', 4: '16px', 5: '24px', 6: '32px', 7: '48px' },
  { mul: (_step: number, multiplier: number) => `${multiplier * 4}px` }
);

export const ds = {
  space,
  background: makeScale({ 100: '#f5f5f5', 200: '#eeeeee', 300: '#e0e0e0' }),
  gray: {
    ...makeScale({
      100: '#f3f4f6',
      200: '#e5e7eb',
      300: '#d1d5db',
      400: '#9ca3af',
      500: '#6b7280',
      600: '#4b5563',
      700: '#374151',
    }),
    alpha: {
      100: 'rgba(0,0,0,0.05)',
      200: 'rgba(0,0,0,0.1)',
      300: 'rgba(0,0,0,0.2)',
      400: 'rgba(0,0,0,0.3)',
      500: 'rgba(0,0,0,0.4)',
      600: 'rgba(0,0,0,0.5)',
      700: 'rgba(0,0,0,0.6)',
    },
  },
  red: makeScale({
    100: '#fee2e2',
    200: '#fecaca',
    300: '#fca5a5',
    400: '#f87171',
    500: '#ef4444',
    600: '#dc2626',
    700: '#b91c1c',
  }),
  blue: makeScale({
    100: '#dbeafe',
    200: '#bfdbfe',
    300: '#93c5fd',
    400: '#60a5fa',
    500: '#3b82f6',
    600: '#2563eb',
    700: '#1d4ed8',
  }),
  green: makeScale({
    100: '#dcfce7',
    200: '#bbf7d0',
    300: '#86efac',
    400: '#4ade80',
    500: '#22c55e',
    600: '#16a34a',
    700: '#15803d',
  }),
  amber: makeScale({
    100: '#fef3c7',
    200: '#fde68a',
    300: '#fcd34d',
    400: '#fbbf24',
    500: '#f59e0b',
    600: '#d97706',
    700: '#b45309',
  }),
  purple: makeScale({
    100: '#f3e8ff',
    200: '#e9d5ff',
    300: '#d8b4fe',
    400: '#c084fc',
    500: '#a855f7',
    600: '#9333ea',
    700: '#7e22ce',
  }),
  yellow: makeScale({
    100: '#fefce8',
    200: '#fef9c3',
    300: '#fef08a',
    400: '#fde047',
    500: '#eab308',
    600: '#ca8a04',
    700: '#a16207',
  }),
  pink: makeScale({
    100: '#fce7f3',
    200: '#fbcfe8',
    300: '#f9a8d4',
    400: '#f472b6',
    500: '#ec4899',
    600: '#db2777',
    700: '#be185d',
  }),
  teal: makeScale({
    50: '#f0fdfa',
    100: '#ccfbf1',
    200: '#99f6e4',
    300: '#5eead4',
    400: '#2dd4bf',
    500: '#14b8a6',
    600: '#0d9488',
    700: '#0f766e',
  }),
  brand: makeScale({
    100: '#e8edf5',
    150: '#e0e7ff',
    200: '#c8d3e6',
    300: '#8fa8cc',
    400: '#4d78ab',
    500: '#1565c0',
    600: '#0d47a1',
    700: '#0a2e6e',
  }),
  text: {
    caption: '10px',
    small: '11px',
    body: '13px',
    bodyLg: '14px',
    title: '16px',
    heading: '20px',
    display: '28px',
  },
  weight: {
    regular: '400',
    medium: '500',
    semibold: '600',
  },
  font: {
    sans: 'sans-serif',
    mono: 'monospace',
  },
  radius: {
    sm: '4px',
    md: '6px',
    lg: '8px',
    xl: '12px',
    pill: '9999px',
  },
  motion: {
    micro: '100ms ease',
    panel: '200ms ease',
    ease: 'ease',
  },
};

export const resolveColor = (value: string): string => value;

export const resolveColors = (values: string[]): string[] => values;

export const withAlpha = (_color: string, _alpha: number): string => 'rgba(0,0,0,0.1)';
