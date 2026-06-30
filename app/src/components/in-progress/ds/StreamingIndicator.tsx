/**
 * StreamingIndicator — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/agentic/streaming.html
 *
 * Cursor or pulse-dot at the tail of streaming agent text. Stops the moment the
 * stream closes. Honours `prefers-reduced-motion` automatically (collapses to a
 * static, dim glyph). Carries an aria-live announcement so screen readers know
 * the response is in progress.
 *
 * Per D9 (long-form streaming): use two indicators in parallel — a `pulse-trio`
 * in the response header (active for the entire stream) plus an `inline-tail`
 * cursor at the active section's tail (moves with the streamed text).
 *
 * Variants per spec:
 *   style     = 'cursor' | 'pulse-dot' | 'pulse-trio'
 *   size      = 'caption' | 'body' | 'body-lg'
 *   placement = 'inline-tail' | 'section-tail' | 'response-header'
 *
 * Don't (per spec):
 *   - Don't leave the indicator running after the stream closes. Bind to the
 *     stream's `onClose` / `onError` events (caller's responsibility — unmount this).
 *   - Don't render multiple cursors in the same response. One cursor per active stream.
 *   - Don't pulse at > 1 Hz. Faster reads as anxiety; the system is calm even when busy.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type StreamingStyle = 'cursor' | 'pulse-dot' | 'pulse-trio';
export type StreamingSize = 'caption' | 'body' | 'body-lg';
export type StreamingPlacement = 'inline-tail' | 'section-tail' | 'response-header';

export interface StreamingIndicatorProps {
  style?: StreamingStyle;
  size?: StreamingSize;
  placement?: StreamingPlacement;
  /** Announced once per stream open. Defaults to "Streaming response". */
  'aria-label'?: string;
  className?: string;
  id?: string;
}

const SIZE_FONT: Record<StreamingSize, string> = {
  caption: 'var(--ds-text-caption)',
  body: 'var(--ds-text-body)',
  'body-lg': 'var(--ds-text-body-lg)',
};

const SIZE_CURSOR_HEIGHT: Record<StreamingSize, string> = {
  caption: '0.85em',
  body: '1em',
  'body-lg': '1.1em',
};

const KEYFRAMES = {
  '@keyframes ds-streaming-blink': {
    '0%, 50%': { opacity: 1 },
    '51%, 100%': { opacity: 0 },
  },
  '@keyframes ds-streaming-pulse-dot': {
    '0%, 100%': { transform: 'scale(0.8)', opacity: 0.6 },
    '50%': { transform: 'scale(1)', opacity: 1 },
  },
  '@keyframes ds-streaming-pulse-trio': {
    '0%, 80%, 100%': { opacity: 0.3 },
    '40%': { opacity: 1 },
  },
};

const REDUCED_MOTION_OVERRIDE = {
  '@media (prefers-reduced-motion: reduce)': {
    animation: 'none',
    opacity: 0.6,
  },
};

export function StreamingIndicator({
  style = 'cursor',
  size = 'body',
  placement = 'inline-tail',
  'aria-label': ariaLabel = 'Streaming response',
  className,
  id,
}: StreamingIndicatorProps) {
  const isResponseHeader = placement === 'response-header';

  if (style === 'cursor') {
    return (
      <Box
        component='span'
        id={id}
        className={className}
        role='status'
        aria-live='polite'
        aria-label={ariaLabel}
        sx={{
          display: 'inline-block',
          width: '0.6ch',
          height: SIZE_CURSOR_HEIGHT[size],
          marginLeft: '2px',
          backgroundColor: 'var(--ds-blue-500)',
          verticalAlign: 'text-bottom',
          ...KEYFRAMES,
          // 1 Hz max per spec — 1s blink cycle
          animation: 'ds-streaming-blink 1s steps(2, start) infinite',
          ...REDUCED_MOTION_OVERRIDE,
        }}
      />
    );
  }

  if (style === 'pulse-dot') {
    return (
      <Box
        component='span'
        id={id}
        className={className}
        role='status'
        aria-live='polite'
        aria-label={ariaLabel}
        sx={{
          display: 'inline-block',
          width: '8px',
          height: '8px',
          marginLeft: '6px',
          borderRadius: 'var(--ds-radius-pill)',
          backgroundColor: 'var(--ds-blue-500)',
          verticalAlign: 'middle',
          ...KEYFRAMES,
          animation: 'ds-streaming-pulse-dot 1s ease-in-out infinite',
          ...REDUCED_MOTION_OVERRIDE,
        }}
      />
    );
  }

  // pulse-trio
  const dotSize = isResponseHeader ? '4px' : '5px';
  return (
    <Box
      component='span'
      id={id}
      className={className}
      role='status'
      aria-live='polite'
      aria-label={ariaLabel}
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '3px',
        marginLeft: '6px',
        verticalAlign: 'middle',
        fontSize: SIZE_FONT[size],
        ...KEYFRAMES,
        '& > span': {
          display: 'inline-block',
          width: dotSize,
          height: dotSize,
          borderRadius: 'var(--ds-radius-pill)',
          backgroundColor: 'var(--ds-blue-500)',
          animation: 'ds-streaming-pulse-trio 1.4s ease-in-out infinite both',
          ...REDUCED_MOTION_OVERRIDE,
        },
        '& > span:nth-of-type(1)': { animationDelay: '-0.32s' },
        '& > span:nth-of-type(2)': { animationDelay: '-0.16s' },
        '& > span:nth-of-type(3)': { animationDelay: '0s' },
      }}
    >
      <span />
      <span />
      <span />
    </Box>
  );
}

export default StreamingIndicator;
