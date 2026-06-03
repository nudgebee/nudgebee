/**
 * CollapsableCard — DS V2 generic single-unit collapsible card.
 * Spec: app/design-system/primitives/layout/collapsable-card.html
 *
 * Distinct from Accordion: Accordion groups multiple sibling collapsibles,
 * CollapsableCard is a single standalone unit.
 *
 * Surface composition (Phase 4 of the card-consolidation migration):
 *   This component is a behavior wrapper. It composes `<Card>` for the surface
 *   (border, radius, background, shadow) and contributes its own internal
 *   structure (header-button + Collapse body). Centralising the surface in
 *   Card means any future fix to focus rings, border tokens, shadow values,
 *   etc. propagates to CollapsableCard automatically.
 *
 * Variants per spec:
 *   - defaultOpen: boolean (initial state when no persisted value found)
 *   - composition: 'header+body' | 'header+meta+body'
 *   - persist:     'none' | 'local' | 'url'   — 'url' deep-links via ?id=open|closed
 *   - elevation:   'raised' | 'flat'          — forwarded to Card
 *
 * Slots: `header` (left of header row), `meta` (right of header row, only when
 * composition === 'header+meta+body'), `children` (body), `footer` (rendered
 * inside the Collapse panel, below body, separated by a 1px divider — hides
 * when the card is collapsed).
 *
 * Note: The existing domain-heavy `CollapsableCard` at
 * @common/widgets/CollapsableCard is not a generic primitive — keep that one
 * for highlights/resolve flows; use this for generic 1-row collapsibles
 * (replacing legacy MUI Accordion patterns).
 */
import * as React from 'react';
import { Box, Collapse } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import Card from './Card';

export type CollapsableCardComposition = 'header+body' | 'header+meta+body';
export type CollapsableCardPersist = 'none' | 'local' | 'url';
export type CollapsableCardElevation = 'raised' | 'flat';

export interface CollapsableCardProps {
  /** Stable identifier — required for `persist: 'local' | 'url'`. */
  id?: string;
  defaultOpen?: boolean;
  composition?: CollapsableCardComposition;
  persist?: CollapsableCardPersist;
  /** Resting elevation. `raised` = drop-shadow (default), `flat` = none. */
  elevation?: CollapsableCardElevation;
  /** Header content (left side). Chevron is rendered rightmost. */
  header: React.ReactNode;
  /** Meta slot (right side, before the chevron). Rendered only when composition === 'header+meta+body'. */
  meta?: React.ReactNode;
  children: React.ReactNode;
  /** Footer content (inside the Collapse panel, below body, with a top divider). */
  footer?: React.ReactNode;
  sx?: object;
  /** Fired when the open state changes (after persist sync). */
  onOpenChange?: (open: boolean) => void;
}

const STORAGE_PREFIX = 'ds:collapsable-card:';

function readPersisted(persist: CollapsableCardPersist, id: string | undefined, fallback: boolean): boolean {
  if (!id || persist === 'none') return fallback;
  if (typeof window === 'undefined') return fallback;
  if (persist === 'local') {
    const v = window.localStorage.getItem(STORAGE_PREFIX + id);
    if (v === 'open') return true;
    if (v === 'closed') return false;
    return fallback;
  }
  if (persist === 'url') {
    const params = new URLSearchParams(window.location.search);
    const v = params.get(id);
    if (v === 'open') return true;
    if (v === 'closed') return false;
    return fallback;
  }
  return fallback;
}

function writePersisted(persist: CollapsableCardPersist, id: string | undefined, open: boolean) {
  if (!id || persist === 'none') return;
  if (typeof window === 'undefined') return;
  const stateStr = open ? 'open' : 'closed';
  if (persist === 'local') {
    window.localStorage.setItem(STORAGE_PREFIX + id, stateStr);
    return;
  }
  if (persist === 'url') {
    const url = new URL(window.location.href);
    url.searchParams.set(id, stateStr);
    window.history.replaceState({}, '', url.toString());
  }
}

export function CollapsableCard({
  id,
  defaultOpen = true,
  composition = 'header+body',
  persist = 'none',
  elevation = 'raised',
  header,
  meta,
  children,
  footer,
  sx,
  onOpenChange,
}: CollapsableCardProps) {
  const [open, setOpen] = React.useState<boolean>(() => readPersisted(persist, id, defaultOpen));

  const toggle = React.useCallback(() => {
    setOpen((prev) => {
      const next = !prev;
      writePersisted(persist, id, next);
      onOpenChange?.(next);
      return next;
    });
  }, [persist, id, onOpenChange]);

  const showMeta = composition === 'header+meta+body';

  return (
    <Card
      elevation={elevation}
      sx={{
        // Internal-composition override: CollapsableCard manages its own padding
        // via the header-button + Collapse body below. Card's size-driven padding
        // would double-pad. This is the documented exception to Card's "don't sx
        // padding" rule — internal primitives composing Card are allowed to.
        padding: 0,
        overflow: 'hidden',
        ...sx,
      }}
    >
      <Box
        component='button'
        type='button'
        aria-expanded={open}
        onClick={toggle}
        sx={{
          all: 'unset',
          boxSizing: 'border-box',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-3)',
          width: '100%',
          padding: 'var(--ds-space-3) var(--ds-space-4)',
          cursor: 'pointer',
          fontSize: 'var(--ds-text-body)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-gray-700)',
          borderBottom: open ? '1px solid var(--ds-gray-200)' : 'none',
          '&:hover': { backgroundColor: 'var(--ds-background-200)' },
          '&:focus-visible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '-2px' },
        }}
      >
        <Box sx={{ flex: 1, minWidth: 0, textAlign: 'left' }}>{header}</Box>
        {showMeta && meta && <Box sx={{ flexShrink: 0 }}>{meta}</Box>}
        <KeyboardArrowDownIcon
          aria-hidden
          sx={{
            fontSize: 20,
            flexShrink: 0,
            marginLeft: 'auto',
            transition: 'transform 0.15s ease',
            transform: open ? 'rotate(0deg)' : 'rotate(-90deg)',
            color: 'var(--ds-gray-600)',
          }}
        />
      </Box>
      <Collapse in={open} unmountOnExit>
        <Box sx={{ padding: 'var(--ds-space-4)', color: 'var(--ds-gray-700)' }}>{children}</Box>
        {footer && (
          <Box
            sx={{
              borderTop: '1px solid var(--ds-gray-200)',
              padding: 'var(--ds-space-3) var(--ds-space-4)',
            }}
          >
            {footer}
          </Box>
        )}
      </Collapse>
    </Card>
  );
}

export default CollapsableCard;
