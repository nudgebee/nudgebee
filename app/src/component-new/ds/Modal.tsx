/**
 * Modal — DS V2 unified popup primitive.
 *
 * Combines the responsibilities of the legacy `Modal`
 * (`app/src/components1/common/modal/index.jsx`) and the legacy `NDialog`
 * (`app/src/components1/common/modal/NDialog.tsx`) into a single component
 * that lives in the DS tree. Legacy components remain untouched; call sites
 * migrate to this primitive deliberately.
 *
 * One component covers:
 *  - Plain modal shell: header (title/subtitle/right slot) + body + optional
 *    freeform footer.
 *  - Confirm/Cancel decision dialog (NDialog parity) — set `confirmText` and
 *    `onConfirm` to render the standard footer.
 *  - Loader state (top progress bar + body blur).
 *  - Success state (icon + message + "Close" button).
 *  - Password-change variant of the success icon.
 *  - Backdrop / Escape-key close guard via `backdropClickClose`.
 *  - `additionalComponent` slot rendered outside `DialogContent` (NDialog parity)
 *    for option lists or form panels that sit below the main body copy.
 *
 * Visual chrome (colors, type, radii, spacing) is sourced from the `--ds-*`
 * design tokens in `app/src/styles/theme-tokens.css`. Footer buttons render
 * through the DS V2 `Button` (`@components1/ds/Button`).
 *
 * Spec: app/design-system/primitives/overlays/modal.html
 */
import * as React from 'react';
import Dialog from '@mui/material/Dialog';
import { Box, Typography, DialogContent, IconButton, DialogActions, Fade } from '@mui/material';
import type { TransitionProps } from '@mui/material/transitions';
import CloseIcon from '@mui/icons-material/Close';
import { modalSuccess, modalPasswordChange } from '@assets';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import { Button } from '@components1/ds/Button';

// Fade transition drives MUI's enter/exit lifecycle for opacity; the Paper
// itself runs a keyframe (defined in PaperProps below) that scales up and rises
// from the bottom with a small overshoot — a controlled "pop" rather than a
// drift. Enter uses a spring-like easing (cubic-bezier(0.34, 1.56, 0.64, 1))
// for the bounce; exit is faster and linearly eased so dismissal feels crisp.
const ModalTransition = React.forwardRef(function ModalTransition(
  props: TransitionProps & { children: React.ReactElement },
  ref: React.Ref<unknown>
) {
  return (
    <Fade
      ref={ref}
      easing={{
        enter: 'cubic-bezier(0.22, 1, 0.36, 1)',
        exit: 'cubic-bezier(0.4, 0, 0.2, 1)',
      }}
      timeout={{ enter: 480, exit: 220 }}
      {...props}
    />
  );
});

export type ModalWidth = 'xs' | 'sm' | 'md' | 'lg' | 'xl';
export type ModalSuccessType = 1 | 'PASSWORD_CHANGE';
type CloseReason = 'backdropClick' | 'escapeKeyDown';

export interface ModalProps {
  // ── Open / close ─────────────────────────────────────────────────
  open: boolean;
  /**
   * Close handler. Receives the event + reason when invoked by a
   * backdrop click or Escape press (matches MUI's `Dialog.onClose`).
   * Invoked with no arguments when triggered by the X / Cancel / success
   * Close buttons.
   */
  handleClose?: (event?: object, reason?: CloseReason) => void;
  /** Alias for `handleClose`, preserved from legacy Modal. */
  onClose?: (event?: object, reason?: CloseReason) => void;
  /**
   * When `false`, clicks on the backdrop and Escape key presses are
   * ignored. Default `true`. (NDialog parity.)
   */
  backdropClickClose?: boolean;

  // ── Chrome ───────────────────────────────────────────────────────
  /** MUI breakpoint controlling max width. Default `'sm'`. */
  width?: ModalWidth;
  /** Optional max-height; also clamps the body region. */
  maxHeight?: string;
  /** Title shown in the header bar. */
  title?: React.ReactNode;
  /** Optional subtitle under the title. */
  subtitle?: string;
  /** Slot rendered next to the close button on the right of the header. */
  rightComponentOnTitle?: React.ReactNode;
  /** Hide the tinted header background, border, and shadow. */
  hideTitleBackground?: boolean;

  // ── Body ─────────────────────────────────────────────────────────
  /** Main body content. */
  children?: React.ReactNode;
  /** Style overrides applied to the inner `DialogContent`. */
  contentStyles?: object;
  /**
   * (NDialog parity.) Extra content rendered *outside* the `DialogContent`
   * block with its own padded box and hidden scrollbar. Use for option
   * lists or form panels that sit alongside the main dialog copy.
   */
  additionalComponent?: React.ReactNode;

  // ── Loader state ─────────────────────────────────────────────────
  /** Show a top progress bar and blur the body. Also disables footer buttons. */
  loader?: boolean;

  // ── Success state (legacy `onSuccess` layout) ────────────────────
  onSuccess?: boolean;
  message?: string;
  icon?: string;
  /** Switches the default success icon. `'PASSWORD_CHANGE'` shows the key icon. */
  type?: ModalSuccessType;

  // ── Footer ───────────────────────────────────────────────────────
  /**
   * Freeform footer slot. When provided, takes precedence over the
   * standard Confirm/Cancel footer.
   */
  actionButtons?: React.ReactNode;
  /**
   * When `true`, the `actionButtons` wrapper (`DialogActions`) drops its
   * default 8px padding and uses `display: 'block'`, letting the freeform
   * JSX fill the footer edge-to-edge. Use this for tinted / full-bleed
   * footers that need a coloured background or custom layout reaching the
   * modal's outer edges. Leave `false` (default) for the common case of
   * a button cluster that should sit inset 8px from the modal edges.
   */
  actionButtonsFullBleed?: boolean;

  /**
   * Standard Confirm button label. When set (and `actionButtons` is not
   * provided), the component renders an NDialog-style footer with
   * Cancel + Confirm buttons.
   */
  confirmText?: string;
  /** Click handler for the Confirm button. */
  onConfirm?: () => void;
  /** Disable the Confirm button independently of the `loader` flag. */
  confirmDisabled?: boolean;
  /** Show the Confirm button. Default `true`. */
  isConfirmRequired?: boolean;
  /** Show the Cancel button. Default `true`. */
  isCancelRequired?: boolean;

  // ── Style escape hatch ──────────────────────────────────────────
  sx?: object;
}

const resolveAssetSrc = (asset: unknown): string => {
  if (typeof asset === 'string') return asset;
  const a = asset as { default?: { src?: string }; src?: string } | undefined;
  return a?.src ?? a?.default?.src ?? '';
};

const SUCCESS_ICONS: Record<ModalSuccessType, string> = {
  1: resolveAssetSrc(modalSuccess),
  PASSWORD_CHANGE: resolveAssetSrc(modalPasswordChange),
};

export function Modal({
  open,
  handleClose,
  onClose,
  backdropClickClose = true,
  width = 'sm',
  maxHeight,
  title,
  subtitle,
  rightComponentOnTitle,
  hideTitleBackground = false,
  children,
  contentStyles,
  additionalComponent,
  loader = false,
  onSuccess = false,
  message = '',
  icon,
  type = 1,
  actionButtons,
  actionButtonsFullBleed = false,
  confirmText,
  onConfirm,
  confirmDisabled = false,
  isConfirmRequired = true,
  isCancelRequired = true,
  sx = {},
}: ModalProps) {
  const close = handleClose ?? onClose;
  const resolvedSuccessIcon = icon ?? SUCCESS_ICONS[type] ?? SUCCESS_ICONS[1];

  const handleDialogClose = (event: object, reason: CloseReason) => {
    if (!backdropClickClose && (reason === 'backdropClick' || reason === 'escapeKeyDown')) {
      return;
    }
    close?.(event, reason);
  };

  const showStandardFooter = !actionButtons && confirmText !== undefined && (isCancelRequired || isConfirmRequired);

  return (
    <Dialog
      open={open}
      onClose={handleDialogClose}
      aria-labelledby='alert-dialog-title'
      aria-describedby='alert-dialog-description'
      fullWidth
      maxWidth={width}
      TransitionComponent={ModalTransition}
      sx={{
        ...sx,
        filter: loader ? 'blur(1px)' : 'none',
        '& .MuiBackdrop-root': {
          transition: 'opacity 400ms cubic-bezier(0.22, 1, 0.36, 1) !important',
        },
        // Animation lives on the container (flex wrapper), NOT the Paper.
        // This keeps the Paper transform-free so MUI Menu portals inside
        // the modal can compute their fixed position correctly.
        '& .MuiDialog-container': {
          animation: 'ds-modal-pop-up 480ms cubic-bezier(0.22, 1, 0.36, 1) forwards',
        },
        '@keyframes ds-modal-pop-up': {
          '0%': { transform: 'translateY(44px) scale(0.96)', opacity: 0 },
          // End at transform:none — fill-mode won't keep a non-none transform
          // alive, so no new stacking context is created after animation ends.
          '100%': { transform: 'none', opacity: 1 },
        },
        '@media (prefers-reduced-motion: reduce)': {
          '& .MuiDialog-container': { animation: 'none !important' },
        },
      }}
      PaperProps={{
        sx: {
          borderRadius: 'var(--ds-radius-xl)',
          ...(maxHeight && { maxHeight, height: maxHeight }),
        },
      }}
    >
      {loader && (
        <Box sx={{ position: 'absolute', top: 0, left: 0, width: '100%', zIndex: 1 }}>
          <LinearLoader />
        </Box>
      )}

      {/* ── Header ──────────────────────────────────────────────── */}
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          padding: 'var(--ds-space-4) var(--ds-space-6)',
          ...(!hideTitleBackground && {
            borderBottom: '1px solid var(--ds-gray-200)',
            background: 'var(--ds-blue-100)',
            boxShadow: '0px 3px 4px -2px rgba(0, 0, 0, 0.10)',
          }),
        }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          <Typography
            id='alert-dialog-title'
            sx={{
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              fontFamily: 'var(--ds-font-display)',
            }}
          >
            {title}
          </Typography>
          {subtitle && (
            <Typography
              sx={{
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-500)',
                mt: 0,
              }}
            >
              {subtitle}
            </Typography>
          )}
        </Box>
        <Box display='flex' alignItems='center' justifyContent='flex-end'>
          {rightComponentOnTitle}
          <IconButton id='close-modal-btn' sx={{ padding: 0 }} onClick={() => close?.()} aria-label='Close'>
            <CloseIcon sx={{ fontSize: '24px' }} />
          </IconButton>
        </Box>
      </Box>

      {/* ── Body ────────────────────────────────────────────────── */}
      {/* Default body padding is 24px / 32px so most modal content sits with
          comfortable breathing room out of the box. Pass `contentStyles={{
          padding: 0 }}` for full-bleed lists, or `contentStyles={{ padding:
          'var(--ds-space-5)' }}` to override with custom spacing. */}
      <DialogContent
        sx={{
          padding: 'var(--ds-space-5) var(--ds-space-6)',
          ...contentStyles,
          ...(maxHeight && { maxHeight, height: '100%' }),
        }}
      >
        {onSuccess ? (
          <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='center' my='140px' mx='60px'>
            <Box component='img' sx={{ height: '84px', width: '84px' }} alt='check' src={resolvedSuccessIcon} mx='auto' mb='var(--ds-space-5)' />
            <Box
              sx={{
                textAlign: 'center',
                mt: '14px',
                color: 'var(--ds-gray-600)',
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-regular)',
              }}
            >
              {message}
            </Box>
            <Box
              sx={{
                textAlign: 'center',
                mt: 3,
                mb: 2,
                button: { minWidth: '140px' },
              }}
            >
              <Button size='md' tone='secondary' onClick={() => close?.()}>
                Close
              </Button>
            </Box>
          </Box>
        ) : (
          children
        )}
      </DialogContent>

      {/* ── Optional padded panel below the main content (NDialog parity) ── */}
      {additionalComponent && (
        <Box px='var(--ds-space-5)' sx={{ '& ::-webkit-scrollbar': { display: 'none' } }}>
          {additionalComponent}
        </Box>
      )}

      {/* ── Footer: freeform ────────────────────────────────────── */}
      {/* Default: MUI DialogActions chrome (display: flex, padding: 8px,
          justify-content: flex-end) — the button cluster sits inset 8px from
          the modal edges, right-aligned. Suits the common case (a Stack of
          Cancel + Submit DS Buttons).
          When `actionButtonsFullBleed`, drop padding to 0 and switch to
          display: block so the consumer's freeform JSX can extend
          edge-to-edge (e.g. a tinted-bg footer with status text + buttons). */}
      {actionButtons && (
        <DialogActions
          sx={
            actionButtonsFullBleed
              ? { display: 'block', padding: 0, borderTop: '0.5px solid var(--ds-gray-200)' }
              : { p: 'var(--ds-space-3) var(--ds-space-5)', borderTop: '0.5px solid var(--ds-gray-200)' }
          }
        >
          {actionButtons}
        </DialogActions>
      )}

      {/* ── Footer: standard Confirm/Cancel (NDialog parity) ────── */}
      {showStandardFooter && (
        <DialogActions
          sx={{
            px: 'var(--ds-space-5)',
            my: 'var(--ds-space-4)',
            button: { minWidth: '140px' },
          }}
        >
          {isCancelRequired && (
            <Button tone='secondary' size='md' id='cancel' type='button' onClick={() => close?.()} disabled={loader}>
              Cancel
            </Button>
          )}
          {isConfirmRequired && (
            <Button tone='primary' size='md' id='submit' type='button' onClick={onConfirm} disabled={confirmDisabled || loader}>
              {confirmText}
            </Button>
          )}
        </DialogActions>
      )}
    </Dialog>
  );
}

export default Modal;
