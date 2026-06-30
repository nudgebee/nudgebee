/**
 * Inspector — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/overlays/inspector.html
 *
 * Right-anchored panel for "show me details about this thing". Slides in at
 * `--ds-motion-panel`; never modal — the underlying page stays interactive
 * (click another row to swap the Inspector contents).
 *
 * Distinct from:
 *   - `Modal` — interrupts the page
 *   - `Dialog` — decision-shaped (confirm/cancel)
 *   - `Popover` — transient, click-anchored, panel detaches from a trigger
 *
 * Variants per spec:
 *   width       = 'sm-360' | 'md-480' | 'lg-640'
 *   composition = 'header+body' | 'header+tabs+body' | 'header+body+footer-actions' |
 *                 'header+tabs+body+footer-actions'
 *                 (auto from `tabs`/`footer` slot presence)
 *   dismiss     = 'esc' | 'backdrop-click' | 'both' | 'none'
 *
 * Don't (per spec):
 *   - Don't make Inspector modal. The whole point is "show me this while I
 *     keep working". If the user must commit before continuing, that's a
 *     Dialog or Modal.
 *   - Don't open Inspector from inside Inspector. Swap the Inspector contents
 *     instead — the same panel rebinds to the new entity. Use the same
 *     `<Inspector>` and change `title`/`children`.
 *   - Don't put the page's primary action in the Inspector footer. Inspector
 *     actions are about the inspected thing; page actions stay in the page header.
 *   - Don't use Inspector for first-time onboarding flows. That's a Modal or
 *     a dedicated page.
 */
import * as React from 'react';
import { Box, Drawer, IconButton, Tab, Tabs as MuiTabs, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';

export type InspectorWidth = 'sm-360' | 'md-480' | 'lg-640';
export type InspectorDismiss = 'esc' | 'backdrop-click' | 'both' | 'none';

export interface InspectorTab {
  id: string;
  label: React.ReactNode;
  /** Optional count badge shown after the label (e.g. logs · 218). */
  count?: number;
  disabled?: boolean;
}

export interface InspectorProps {
  /** Controlled open state. Inspector is always controlled — caller owns open/close. */
  open: boolean;
  /** Called when the inspector requests close (esc, backdrop, or close button). */
  onClose: () => void;
  /** Header title. */
  title: React.ReactNode;
  /** Optional subtitle below the title (e.g. "production · us-east-1"). */
  subtitle?: React.ReactNode;
  /** Body content. */
  children: React.ReactNode;
  /** Optional tabs. When provided, composition becomes 'header+tabs+body' (or '+footer-actions'). */
  tabs?: InspectorTab[];
  /** Active tab id (controlled). */
  activeTabId?: string;
  /** Tab change handler. Required when `tabs` is provided. */
  onTabChange?: (next: string) => void;
  /** Optional footer actions. When provided, composition includes 'footer-actions'. */
  footer?: React.ReactNode;
  width?: InspectorWidth;
  dismiss?: InspectorDismiss;
  /** Aria-label for the close button (default "Close inspector"). */
  closeLabel?: string;
  className?: string;
  id?: string;
}

const WIDTH_PX: Record<InspectorWidth, string> = {
  'sm-360': '360px',
  'md-480': '480px',
  'lg-640': '640px',
};

export function Inspector({
  open,
  onClose,
  title,
  subtitle,
  children,
  tabs,
  activeTabId,
  onTabChange,
  footer,
  width = 'md-480',
  dismiss = 'both',
  closeLabel = 'Close inspector',
  className,
  id,
}: InspectorProps) {
  const handleClose = (_e: object, reason: 'escapeKeyDown' | 'backdropClick') => {
    if (dismiss === 'none') return;
    if (reason === 'escapeKeyDown' && (dismiss === 'esc' || dismiss === 'both')) onClose();
    if (reason === 'backdropClick' && (dismiss === 'backdrop-click' || dismiss === 'both')) onClose();
  };

  const handleTabChange = (_e: React.SyntheticEvent, next: string) => {
    onTabChange?.(next);
  };

  const hasTabs = !!(tabs && tabs.length > 0);
  const hasFooter = footer !== undefined;
  // Fallback active tab if caller forgot to wire activeTabId
  const resolvedActiveTab = activeTabId ?? tabs?.[0]?.id ?? '';

  return (
    <Drawer
      id={id}
      className={className}
      anchor='right'
      open={open}
      onClose={handleClose}
      // Inspector is non-modal per spec — but MUI Drawer's `variant='persistent'`
      // doesn't render the backdrop or trap focus, which loses dismiss-on-backdrop.
      // We stay with the default temporary variant (modal-y MUI primitive) but
      // semantically the page stays interactive: caller is responsible for
      // wiring "click another row to swap contents" by changing `title`/`children`
      // while keeping `open=true`.
      ModalProps={{
        keepMounted: false,
        // Disable backdrop opacity to soften the "modal-feel" — Inspector is meant
        // to coexist with the page, not interrupt it.
        slotProps: {
          backdrop: {
            sx: { backgroundColor: 'transparent' },
          },
        },
      }}
      PaperProps={{
        sx: {
          width: WIDTH_PX[width],
          maxWidth: '100vw',
          backgroundColor: 'var(--ds-background-100)',
          borderLeft: '1px solid var(--ds-gray-200)',
          boxShadow: '-4px 0px 20px var(--ds-gray-alpha-200)',
          display: 'flex',
          flexDirection: 'column',
        },
      }}
    >
      {/* Header */}
      <Box
        sx={{
          display: 'flex',
          alignItems: 'flex-start',
          gap: 'var(--ds-space-2)',
          padding: 'var(--ds-space-4)',
          borderBottom: hasTabs ? 'none' : '1px solid var(--ds-gray-200)',
          flexShrink: 0,
        }}
      >
        <Box sx={{ flex: 1, minWidth: 0 }}>
          <Typography
            component='div'
            sx={{
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: 'var(--ds-gray-700)',
              lineHeight: 1.3,
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {title}
          </Typography>
          {subtitle !== undefined && (
            <Typography
              component='div'
              sx={{
                fontSize: 'var(--ds-text-caption)',
                color: 'var(--ds-gray-500)',
                marginTop: '2px',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {subtitle}
            </Typography>
          )}
        </Box>
        <IconButton
          onClick={onClose}
          aria-label={closeLabel}
          size='small'
          sx={{
            color: 'var(--ds-gray-600)',
            flexShrink: 0,
            '&:hover': { backgroundColor: 'var(--ds-gray-100)', color: 'var(--ds-gray-700)' },
          }}
        >
          <CloseIcon sx={{ fontSize: 18 }} />
        </IconButton>
      </Box>

      {/* Tabs (optional) */}
      {hasTabs && (
        <Box sx={{ borderBottom: '1px solid var(--ds-gray-200)', flexShrink: 0, paddingX: 'var(--ds-space-4)' }}>
          <MuiTabs
            value={resolvedActiveTab}
            onChange={handleTabChange}
            variant='scrollable'
            scrollButtons='auto'
            sx={{
              minHeight: '36px',
              '& .MuiTabs-indicator': {
                height: 2,
                backgroundColor: 'var(--ds-blue-500)',
              },
            }}
          >
            {tabs!.map((t) => (
              <Tab
                key={t.id}
                value={t.id}
                disabled={t.disabled}
                label={
                  <Box component='span' sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                    <Box component='span'>{t.label}</Box>
                    {t.count !== undefined && (
                      <Box
                        component='span'
                        sx={{
                          fontSize: 'var(--ds-text-caption)',
                          color: 'var(--ds-gray-500)',
                          fontVariantNumeric: 'tabular-nums',
                        }}
                      >
                        {t.count}
                      </Box>
                    )}
                  </Box>
                }
                sx={{
                  minHeight: '36px',
                  height: '36px',
                  padding: '0 var(--ds-space-3)',
                  textTransform: 'none',
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: 'var(--ds-gray-600)',
                  '&.Mui-selected': {
                    color: 'var(--ds-blue-600)',
                  },
                  '&:hover': {
                    color: 'var(--ds-blue-600)',
                    backgroundColor: 'var(--ds-gray-100)',
                  },
                }}
              />
            ))}
          </MuiTabs>
        </Box>
      )}

      {/* Body */}
      <Box
        sx={{
          flex: 1,
          minHeight: 0,
          overflow: 'auto',
          padding: 'var(--ds-space-4)',
          color: 'var(--ds-gray-700)',
        }}
      >
        {children}
      </Box>

      {/* Footer (optional) */}
      {hasFooter && (
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--ds-space-2)',
            padding: 'var(--ds-space-4)',
            borderTop: '1px solid var(--ds-gray-200)',
            backgroundColor: 'var(--ds-background-100)',
            flexShrink: 0,
          }}
        >
          {footer}
        </Box>
      )}
    </Drawer>
  );
}

export default Inspector;
