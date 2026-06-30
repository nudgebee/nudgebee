/**
 * Popover — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/overlays/popover.html
 *
 * Click-anchored, interactive panel. Holds actions, forms, filter pickers, and
 * any content the user needs to engage with.
 *
 * Distinct from:
 *   - `Tooltip` — hover-only, non-interactive, static information
 *   - `Modal` — interrupts the page
 *   - `Dialog` — decision-shaped (confirm/cancel)
 *   - `Inspector` — right-anchored, persistent
 *   - `DropdownMenu` — action list only, no free content
 *
 * Variants per spec:
 *   trigger     = 'click' | 'focus' | 'hover-with-intent'
 *                 (hover-with-intent: 300ms open, 400ms close — only for entity previews)
 *   side        = 'top' | 'bottom' | 'left' | 'right'
 *   align       = 'start' | 'center' | 'end'
 *   size        = 'sm-240' | 'md-320' | 'lg-480'  (panel min-width)
 *   composition = 'body' | 'header+body' | 'header+body+footer'  (auto from header/footer slot presence)
 *   dismiss     = 'esc' | 'outside-click' | 'both' | 'explicit'
 *
 * Don't (per spec):
 *   - Don't use Popover for static information. That's `Tooltip`.
 *   - Don't put primary navigation inside a Popover. Pages don't live in Popovers.
 *   - Don't use `hover-with-intent` for content that requires reading.
 *   - Don't put a destructive action in a Popover without confirmation. That's a `Dialog`.
 *
 * NOTES.md candidates (Phase 1 dropped features, see Track B contract §4.2):
 *   - "Opening a second dismisses the first" single-popover invariant: NOT enforced
 *     in this initial implementation. Consumers managing multiple popovers should
 *     close the previous one explicitly. A future revision could add a Provider
 *     context that singletons open state.
 */
import * as React from 'react';
import { Popover as MuiPopover, Box } from '@mui/material';

export type PopoverTrigger = 'click' | 'focus' | 'hover-with-intent';
export type PopoverSide = 'top' | 'bottom' | 'left' | 'right';
export type PopoverAlign = 'start' | 'center' | 'end';
export type PopoverSize = 'sm-240' | 'md-320' | 'lg-480';
export type PopoverDismiss = 'esc' | 'outside-click' | 'both' | 'explicit';

export interface PopoverProps {
  /** Trigger element (Button, IconButton, link, etc.). onClick / onFocus is wired by Popover. */
  children: React.ReactElement;
  /** Panel content. Compose with `header` / `footer` for the multi-slot variants. */
  content: React.ReactNode;
  /** Optional header slot (text or React node). Renders above `content` with a divider. */
  header?: React.ReactNode;
  /** Optional footer slot (e.g. Clear / Apply buttons). Renders below `content` with a divider. */
  footer?: React.ReactNode;
  trigger?: PopoverTrigger;
  side?: PopoverSide;
  align?: PopoverAlign;
  size?: PopoverSize;
  dismiss?: PopoverDismiss;
  /** Controlled open state. If omitted, Popover manages its own state. */
  open?: boolean;
  /** Called when the popover requests close (esc, outside click, etc.). */
  onClose?: () => void;
  /** Called when the popover opens. */
  onOpen?: () => void;
  /** Hover-with-intent timings (ms). Defaults: open=300, close=400. */
  hoverIntent?: { open?: number; close?: number };
  id?: string;
  className?: string;
}

const SIZE_MIN_WIDTH: Record<PopoverSize, string> = {
  'sm-240': '240px',
  'md-320': '320px',
  'lg-480': '480px',
};

function deriveAnchorOrigin(side: PopoverSide, align: PopoverAlign) {
  if (side === 'top') {
    return {
      vertical: 'top' as const,
      horizontal: align === 'end' ? ('right' as const) : align === 'center' ? ('center' as const) : ('left' as const),
    };
  }
  if (side === 'left') {
    return {
      vertical: align === 'end' ? ('bottom' as const) : align === 'center' ? ('center' as const) : ('top' as const),
      horizontal: 'left' as const,
    };
  }
  if (side === 'right') {
    return {
      vertical: align === 'end' ? ('bottom' as const) : align === 'center' ? ('center' as const) : ('top' as const),
      horizontal: 'right' as const,
    };
  }
  return {
    vertical: 'bottom' as const,
    horizontal: align === 'end' ? ('right' as const) : align === 'center' ? ('center' as const) : ('left' as const),
  };
}

function deriveTransformOrigin(side: PopoverSide, align: PopoverAlign) {
  if (side === 'top') {
    return {
      vertical: 'bottom' as const,
      horizontal: align === 'end' ? ('right' as const) : align === 'center' ? ('center' as const) : ('left' as const),
    };
  }
  if (side === 'left') {
    return {
      vertical: align === 'end' ? ('bottom' as const) : align === 'center' ? ('center' as const) : ('top' as const),
      horizontal: 'right' as const,
    };
  }
  if (side === 'right') {
    return {
      vertical: align === 'end' ? ('bottom' as const) : align === 'center' ? ('center' as const) : ('top' as const),
      horizontal: 'left' as const,
    };
  }
  return {
    vertical: 'top' as const,
    horizontal: align === 'end' ? ('right' as const) : align === 'center' ? ('center' as const) : ('left' as const),
  };
}

export function Popover({
  children,
  content,
  header,
  footer,
  trigger = 'click',
  side = 'bottom',
  align = 'start',
  size = 'md-320',
  dismiss = 'both',
  open: controlledOpen,
  onClose,
  onOpen,
  hoverIntent,
  id,
  className,
}: PopoverProps) {
  const [anchorEl, setAnchorEl] = React.useState<HTMLElement | null>(null);
  const isControlled = controlledOpen !== undefined;
  const isOpen = isControlled ? controlledOpen : Boolean(anchorEl);
  const triggerRef = React.useRef<HTMLElement | null>(null);
  const hoverOpenTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const hoverCloseTimerRef = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  const openMs = hoverIntent?.open ?? 300;
  const closeMs = hoverIntent?.close ?? 400;

  const open = React.useCallback(
    (target: HTMLElement) => {
      setAnchorEl(target);
      onOpen?.();
    },
    [onOpen]
  );

  const close = React.useCallback(() => {
    setAnchorEl(null);
    onClose?.();
  }, [onClose]);

  const clearHoverTimers = () => {
    if (hoverOpenTimerRef.current) {
      clearTimeout(hoverOpenTimerRef.current);
      hoverOpenTimerRef.current = null;
    }
    if (hoverCloseTimerRef.current) {
      clearTimeout(hoverCloseTimerRef.current);
      hoverCloseTimerRef.current = null;
    }
  };

  React.useEffect(() => {
    return () => clearHoverTimers();
  }, []);

  // Build the trigger element with the appropriate event handlers per `trigger` mode.
  const triggerProps: Record<string, unknown> = {
    ref: (el: HTMLElement | null) => {
      triggerRef.current = el;
      // Preserve any existing ref on the child
      const childRef = (children as { ref?: React.Ref<HTMLElement> }).ref;
      if (typeof childRef === 'function') childRef(el);
      else if (childRef && 'current' in childRef) (childRef as React.MutableRefObject<HTMLElement | null>).current = el;
    },
    'aria-haspopup': 'dialog',
    'aria-expanded': isOpen,
  };

  if (trigger === 'click') {
    triggerProps.onClick = (e: React.MouseEvent<HTMLElement>) => {
      const existing = (children.props as { onClick?: (e: React.MouseEvent<HTMLElement>) => void }).onClick;
      existing?.(e);
      if (isOpen) close();
      else open(e.currentTarget);
    };
  } else if (trigger === 'focus') {
    triggerProps.onFocus = (e: React.FocusEvent<HTMLElement>) => {
      const existing = (children.props as { onFocus?: (e: React.FocusEvent<HTMLElement>) => void }).onFocus;
      existing?.(e);
      open(e.currentTarget);
    };
    triggerProps.onBlur = (e: React.FocusEvent<HTMLElement>) => {
      const existing = (children.props as { onBlur?: (e: React.FocusEvent<HTMLElement>) => void }).onBlur;
      existing?.(e);
      // Close on blur unless focus moved into the popover panel
      if (!e.relatedTarget || !(e.relatedTarget as HTMLElement).closest('.ds-popover-panel')) {
        close();
      }
    };
  } else if (trigger === 'hover-with-intent') {
    triggerProps.onMouseEnter = (e: React.MouseEvent<HTMLElement>) => {
      const existing = (children.props as { onMouseEnter?: (e: React.MouseEvent<HTMLElement>) => void }).onMouseEnter;
      existing?.(e);
      clearHoverTimers();
      const target = e.currentTarget;
      hoverOpenTimerRef.current = setTimeout(() => open(target), openMs);
    };
    triggerProps.onMouseLeave = (e: React.MouseEvent<HTMLElement>) => {
      const existing = (children.props as { onMouseLeave?: (e: React.MouseEvent<HTMLElement>) => void }).onMouseLeave;
      existing?.(e);
      clearHoverTimers();
      hoverCloseTimerRef.current = setTimeout(() => close(), closeMs);
    };
  }

  const enhancedTrigger = React.cloneElement(children, triggerProps);

  const handleMuiClose = (_e: object, reason: 'escapeKeyDown' | 'backdropClick') => {
    if (dismiss === 'explicit') return; // Caller closes via controlled `open`
    if (reason === 'escapeKeyDown' && (dismiss === 'esc' || dismiss === 'both')) close();
    if (reason === 'backdropClick' && (dismiss === 'outside-click' || dismiss === 'both')) close();
  };

  // For hover-with-intent: keep panel open while hovered; close after hover-out + delay.
  const panelHoverProps =
    trigger === 'hover-with-intent'
      ? {
          onMouseEnter: () => clearHoverTimers(),
          onMouseLeave: () => {
            clearHoverTimers();
            hoverCloseTimerRef.current = setTimeout(() => close(), closeMs);
          },
        }
      : {};

  return (
    <>
      {enhancedTrigger}
      <MuiPopover
        id={id}
        className={className}
        open={isOpen}
        anchorEl={anchorEl ?? triggerRef.current}
        onClose={handleMuiClose}
        anchorOrigin={deriveAnchorOrigin(side, align)}
        transformOrigin={deriveTransformOrigin(side, align)}
        slotProps={{
          paper: {
            className: 'ds-popover-panel',
            sx: {
              minWidth: SIZE_MIN_WIDTH[size],
              borderRadius: 'var(--ds-radius-md)',
              border: '1px solid var(--ds-gray-200)',
              boxShadow: '0px 4px 20px var(--ds-gray-alpha-200)',
              backgroundColor: 'var(--ds-background-100)',
              overflow: 'hidden',
              marginTop: side === 'bottom' ? '6px' : 0,
              marginBottom: side === 'top' ? '6px' : 0,
              marginLeft: side === 'right' ? '6px' : 0,
              marginRight: side === 'left' ? '6px' : 0,
            },
            ...panelHoverProps,
          },
        }}
      >
        {header !== undefined && (
          <Box
            sx={{
              padding: 'var(--ds-space-3)',
              borderBottom: '1px solid var(--ds-gray-200)',
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
            }}
          >
            {header}
          </Box>
        )}
        <Box sx={{ padding: 'var(--ds-space-3)' }}>{content}</Box>
        {footer !== undefined && (
          <Box
            sx={{
              padding: 'var(--ds-space-3)',
              borderTop: '1px solid var(--ds-gray-200)',
              display: 'flex',
              justifyContent: 'space-between',
              gap: 'var(--ds-space-2)',
            }}
          >
            {footer}
          </Box>
        )}
      </MuiPopover>
    </>
  );
}

export default Popover;
