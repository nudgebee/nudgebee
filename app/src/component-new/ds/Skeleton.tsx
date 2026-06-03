/**
 * Skeleton — DS V2 of legacy ShimmerLoading + NewShimmerloading + SummarySkeletonLoader + ThreeDotLoader.
 * Spec:        app/design-system/primitives/feedback/skeleton.html
 * Variants:    shape = 'text' | 'rect' | 'circle'
 *              size  = 'caption' | 'text' | 'title' | 'heading'  (for shape='text', matches type-scale row heights)
 *              animation = 'shimmer' | 'none'                    (auto-honors prefers-reduced-motion via --ds-motion-* tokens)
 *              composition presets: <Skeleton.TableRow columns={N} /> · <Skeleton.Card /> · <Skeleton.ChatMessage />
 *
 * Migration:   `import ShimmerLoading from '@common/ShimmerLoading'`
 *              `import NewShimmerloading from '@common/NewShimmerloading'`
 *              `import SummarySkeletonLoader from '@common/SummarySkeletonLoader'`
 *              `import ThreeDotLoader from '@common/ThreeDotLoader'`
 *           →  `import { Skeleton } from '@components1/ds/Skeleton'`
 *
 *   V1 pattern                                     →  V2 pattern
 *   <ShimmerLoading isLoading={l}>{...}</...>      →  {l ? <Skeleton shape='text' /> : <>{...}</>}
 *   <ShimmerLoading isLoading lines={3} />         →  <Stack>{[1,2,3].map(i=> <Skeleton key={i} shape='text' />)}</Stack>
 *   <NewShimmerloading />                          →  <Skeleton.Card />
 *   <SummarySkeletonLoader />                      →  <Skeleton.Card />
 *   <ThreeDotLoader />                             →  prefer ProgressLinear for slow ops; or <Skeleton shape='text' size='caption' />
 *
 * Don't (per spec):
 *   - Don't render Skeleton at dimensions that don't match the eventual content (no layout shift).
 *   - Don't render >10 Skeleton rows on a list. 5 is enough.
 *   - Don't use Skeleton for slow operations — use ProgressLinear.
 *   - Don't combine Skeleton with a spinner. Pick one loading affordance per region.
 */
import * as React from 'react';
import { Box, Stack } from '@mui/material';

export type SkeletonShape = 'text' | 'rect' | 'circle';
export type SkeletonTextSize = 'caption' | 'text' | 'title' | 'heading';
export type SkeletonAnimation = 'shimmer' | 'none';

export interface SkeletonProps {
  shape?: SkeletonShape;
  /** Only meaningful when shape='text'; ignored otherwise. */
  size?: SkeletonTextSize;
  width?: number | string;
  height?: number | string;
  animation?: SkeletonAnimation;
  className?: string;
  sx?: object;
  /** Aria label for screen readers (default: "Loading"). */
  ariaLabel?: string;
}

const TEXT_SIZE_HEIGHT: Record<SkeletonTextSize, string> = {
  caption: 'var(--ds-text-caption)',
  text: 'var(--ds-text-body)',
  title: 'var(--ds-text-title)',
  heading: 'var(--ds-text-heading)',
};

const TEXT_SIZE_DEFAULT_WIDTH: Record<SkeletonTextSize, string> = {
  caption: '60px',
  text: '120px',
  title: '180px',
  heading: '240px',
};

const SHIMMER_KEYFRAMES = {
  '@keyframes ds-skeleton-shimmer': {
    '0%': { backgroundPosition: '-200% 0' },
    '100%': { backgroundPosition: '200% 0' },
  },
};

function shimmerStyle(animation: SkeletonAnimation) {
  if (animation === 'none') {
    return { backgroundColor: 'var(--ds-gray-100)' };
  }
  return {
    backgroundColor: 'var(--ds-gray-100)',
    backgroundImage: 'linear-gradient(90deg, transparent 0%, var(--ds-gray-200) 50%, transparent 100%)',
    backgroundSize: '200% 100%',
    backgroundRepeat: 'no-repeat',
    animation: 'ds-skeleton-shimmer 1.4s infinite linear',
    // prefers-reduced-motion: honoured via --ds-motion-* tokens (the @media in styles.css collapses to 1ms)
    '@media (prefers-reduced-motion: reduce)': {
      animation: 'none',
      backgroundImage: 'none',
    },
  };
}

function shapeStyle(shape: SkeletonShape, size: SkeletonTextSize, width?: number | string, height?: number | string) {
  if (shape === 'circle') {
    const dim = width ?? height ?? 32;
    return {
      width: typeof dim === 'number' ? `${dim}px` : dim,
      height: typeof dim === 'number' ? `${dim}px` : dim,
      borderRadius: 'var(--ds-radius-pill)',
    };
  }
  if (shape === 'rect') {
    return {
      width: typeof width === 'number' ? `${width}px` : width ?? '100%',
      height: typeof height === 'number' ? `${height}px` : height ?? '80px',
      borderRadius: 'var(--ds-radius-sm)',
    };
  }
  // shape === 'text'
  return {
    width: typeof width === 'number' ? `${width}px` : width ?? TEXT_SIZE_DEFAULT_WIDTH[size],
    height: typeof height === 'number' ? `${height}px` : TEXT_SIZE_HEIGHT[size],
    borderRadius: 'var(--ds-radius-sm)',
  };
}

function SkeletonBase({ shape = 'text', size = 'text', width, height, animation = 'shimmer', className, sx, ariaLabel = 'Loading' }: SkeletonProps) {
  return (
    <Box
      role='status'
      aria-busy='true'
      aria-label={ariaLabel}
      className={className}
      sx={{
        display: 'inline-block',
        ...SHIMMER_KEYFRAMES,
        ...shimmerStyle(animation),
        ...shapeStyle(shape, size, width, height),
        ...sx,
      }}
    />
  );
}

// ── Composition presets ──────────────────────────────────────────────────────

export interface SkeletonTableRowProps {
  columns: number;
  /** Per-column widths; defaults to a reasonable spread if omitted */
  columnWidths?: Array<string | number>;
  rowHeight?: number | string;
  animation?: SkeletonAnimation;
}

function SkeletonTableRow({ columns, columnWidths, rowHeight, animation = 'shimmer' }: SkeletonTableRowProps) {
  const defaultWidths = ['140px', '80px', '24px', '48px', '60px'];
  const widths = columnWidths ?? Array.from({ length: columns }, (_, i) => defaultWidths[i % defaultWidths.length]);
  return (
    <>
      {widths.slice(0, columns).map((w, i) => (
        <td key={i} style={{ padding: 'var(--ds-space-2) var(--ds-space-3)' }}>
          <SkeletonBase shape='text' size='text' width={w} height={rowHeight} animation={animation} />
        </td>
      ))}
    </>
  );
}

export interface SkeletonCardProps {
  width?: number | string;
  height?: number | string;
  /** Number of body lines under the title (default 2) */
  lines?: number;
  animation?: SkeletonAnimation;
}

function SkeletonCard({ width = 240, height, lines = 2, animation = 'shimmer' }: SkeletonCardProps) {
  return (
    <Box
      sx={{
        width: typeof width === 'number' ? `${width}px` : width,
        minHeight: typeof height === 'number' ? `${height}px` : height,
        padding: 'var(--ds-space-4)',
        border: '1px solid var(--ds-gray-200)',
        borderRadius: 'var(--ds-radius-md)',
        backgroundColor: 'var(--ds-background-100)',
      }}
    >
      <Stack spacing={1.5}>
        <SkeletonBase shape='text' size='caption' width='40%' animation={animation} />
        <SkeletonBase shape='text' size='heading' width='60%' animation={animation} />
        {Array.from({ length: lines }).map((_, i) => (
          <SkeletonBase key={i} shape='text' size='text' width={i === lines - 1 ? '70%' : '100%'} animation={animation} />
        ))}
      </Stack>
    </Box>
  );
}

export interface SkeletonChatMessageProps {
  width?: number | string;
  /** Number of message body lines (default 3) */
  lines?: number;
  animation?: SkeletonAnimation;
}

function SkeletonChatMessage({ width = 480, lines = 3, animation = 'shimmer' }: SkeletonChatMessageProps) {
  return (
    <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', maxWidth: typeof width === 'number' ? `${width}px` : width }}>
      <SkeletonBase shape='circle' width={32} animation={animation} />
      <Stack spacing={1} sx={{ flex: 1 }}>
        <SkeletonBase shape='text' size='caption' width='80px' animation={animation} />
        {Array.from({ length: lines }).map((_, i) => (
          <SkeletonBase key={i} shape='text' size='text' width={i === lines - 1 ? '60%' : '100%'} animation={animation} />
        ))}
      </Stack>
    </Box>
  );
}

// Compound API: <Skeleton.TableRow /> · <Skeleton.Card /> · <Skeleton.ChatMessage />
type SkeletonComponent = React.FC<SkeletonProps> & {
  TableRow: React.FC<SkeletonTableRowProps>;
  Card: React.FC<SkeletonCardProps>;
  ChatMessage: React.FC<SkeletonChatMessageProps>;
};

export const Skeleton = SkeletonBase as SkeletonComponent;
Skeleton.TableRow = SkeletonTableRow;
Skeleton.Card = SkeletonCard;
Skeleton.ChatMessage = SkeletonChatMessage;

export default Skeleton;
