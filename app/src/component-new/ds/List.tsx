/**
 * List — DS V2 of legacy CustomListWithShowMore.
 * Spec: app/design-system/primitives/layout/list.html
 *
 * Vertical list of label rows with optional truncation ("show 5 / show all").
 * Distinct from `Table` — List has no columns; rows render whatever they want.
 *
 * Variants per spec:
 *   composition = 'label' | 'label+subtext' | 'icon+label' | 'icon+label+meta'
 *                 (consumer-driven via renderItem)
 *   truncate    = 'none' | { show: N, label?: string }
 *   divider     = 'none' | 'between'
 *   bordered    = boolean (the bordered/rounded surface; set false for a bare list)
 *
 * Built-in bullet composition:
 *   For the common "bulleted clickable text" case (the legacy
 *   CustomListWithShowMore shape) you can omit `renderItem` entirely and pass
 *   string items plus `bullet` / `maxItemLength` / `onItemClick`. List then
 *   renders a leading brand bullet, per-item character truncation with a hover
 *   Tooltip carrying the full text, and an optional click handler. Provide
 *   `renderItem` to take full control instead — the two are mutually exclusive.
 *
 * Don't (per spec):
 *   - Don't use List for > 50 items without virtualisation. That's a Table problem.
 *
 * Migration:
 *   `import CustomListWithShowMore from '@components1/common/CustomListWithShowMore'`
 * → `import { List } from '@components1/ds/List'`
 *   Truncation moves from per-list config to the standard `truncate` prop;
 *   `initialCount` → `truncate={{ show: N }}`, and bullets/tooltip move behind
 *   the `bullet` / `maxItemLength` props.
 */
import * as React from 'react';
import { Box, ButtonBase } from '@mui/material';
import Tooltip from '@components1/ds/Tooltip';
import { ds } from '@utils/colors';

export type ListDivider = 'none' | 'between';

const TOOLTIP_MAX_HEIGHT = '400px';

export interface ListTruncate {
  /** Initial number of rows to show. Remainder collapses behind a "show N more" button. */
  show: number;
  /** Template for the toggle label. `{n}` is substituted with the hidden-row count. Default: `Show {n} more →`. */
  label?: string;
  /** Label when expanded. Default: `Show less ↑`. */
  collapseLabel?: string;
}

export interface ListProps<T> {
  items: T[];
  /**
   * Renders one row. Receives the item and its index. Omit to use the built-in
   * bullet composition (string items + `bullet`/`maxItemLength`/`onItemClick`).
   */
  renderItem?: (item: T, index: number) => React.ReactNode;
  /** Stable key extractor; defaults to index. */
  keyFor?: (item: T, index: number) => React.Key;
  truncate?: ListTruncate;
  divider?: ListDivider;
  /** Bordered/rounded surface around the rows. Default `true`. Set `false` for a bare list. */
  bordered?: boolean;
  /** Render when `items.length === 0`. */
  empty?: React.ReactNode;
  className?: string;
  id?: string;
  /** Group accessible name. */
  ariaLabel?: string;

  /* --- Built-in bullet composition (ignored when `renderItem` is provided) --- */
  /** Show a leading brand bullet dot on each row. */
  bullet?: boolean;
  /** Truncate string items longer than this many characters, with a Tooltip carrying the full text. */
  maxItemLength?: number;
  /** Click handler for the built-in row (makes the text interactive). */
  onItemClick?: (item: T, index: number) => void;
}

function BulletRow<T>({
  item,
  index,
  bullet,
  maxItemLength,
  onItemClick,
}: {
  item: T;
  index: number;
  bullet?: boolean;
  maxItemLength?: number;
  onItemClick?: (item: T, index: number) => void;
}) {
  const text = typeof item === 'string' ? item : String(item);
  const needsTruncation = maxItemLength != null && text.length > maxItemLength;
  const displayText = needsTruncation ? text.slice(0, maxItemLength) + '…' : text;
  const clickable = !!onItemClick;

  const textNode = (
    <Box
      component='span'
      onClick={clickable ? () => onItemClick!(item, index) : undefined}
      sx={{
        color: 'var(--ds-brand-500)',
        paddingLeft: 'var(--ds-space-1)',
        fontSize: 'var(--ds-text-body)',
        cursor: clickable ? 'pointer' : 'default',
        wordBreak: 'break-all',
      }}
    >
      {displayText}
    </Box>
  );

  return (
    <Box sx={{ display: 'flex', alignItems: 'flex-start' }}>
      {bullet && (
        <Box
          sx={{
            width: '5px',
            height: '5px',
            bgcolor: 'var(--ds-brand-500)',
            borderRadius: '100%',
            marginTop: 'var(--ds-space-2)',
            marginRight: 'var(--ds-space-1)',
            flexShrink: 0,
            boxShadow: '0 0 0 2px rgba(59, 131, 246, 0.15)',
          }}
        />
      )}
      {needsTruncation ? (
        <Tooltip
          title={<Box sx={{ maxHeight: TOOLTIP_MAX_HEIGHT, overflow: 'auto', fontSize: 'var(--ds-text-small)', lineHeight: 1.5 }}>{text}</Box>}
          placement='top'
        >
          {textNode}
        </Tooltip>
      ) : (
        textNode
      )}
    </Box>
  );
}

export function List<T>({
  items,
  renderItem,
  keyFor,
  truncate,
  divider = 'between',
  bordered = true,
  empty,
  className,
  id,
  ariaLabel,
  bullet,
  maxItemLength,
  onItemClick,
}: ListProps<T>) {
  const [expanded, setExpanded] = React.useState(false);

  if (items.length === 0) {
    if (empty === undefined) return null;
    return (
      <Box id={id} className={className} role='list' aria-label={ariaLabel}>
        {empty}
      </Box>
    );
  }

  const truncated = truncate && !expanded && items.length > truncate.show;
  const visibleItems = truncated ? items.slice(0, truncate!.show) : items;
  const hiddenCount = items.length - (truncate?.show ?? items.length);

  const labelTemplate = truncate?.label ?? 'Show {n} more →';
  const collapseLabel = truncate?.collapseLabel ?? 'Show less ↑';

  const renderRow = (item: T, i: number) =>
    renderItem ? renderItem(item, i) : <BulletRow item={item} index={i} bullet={bullet} maxItemLength={maxItemLength} onItemClick={onItemClick} />;

  return (
    <Box
      id={id}
      className={className}
      role='list'
      aria-label={ariaLabel}
      sx={{
        listStyle: 'none',
        padding: 0,
        margin: 0,
        ...(bordered && {
          border: '1px solid var(--ds-gray-200)',
          borderRadius: 'var(--ds-radius-md)',
          overflow: 'hidden',
        }),
      }}
    >
      {visibleItems.map((item, i) => (
        <Box
          key={keyFor ? keyFor(item, i) : i}
          role='listitem'
          sx={{
            padding: 'var(--ds-space-3)',
            borderBottom: divider === 'between' && i < visibleItems.length - 1 ? '1px solid var(--ds-gray-200)' : 'none',
          }}
        >
          {renderRow(item, i)}
        </Box>
      ))}
      {truncate && items.length > truncate.show && (
        <Box
          sx={{
            padding: 'var(--ds-space-3)',
            textAlign: 'center',
            borderTop: divider === 'between' ? '1px solid var(--ds-gray-200)' : 'none',
          }}
        >
          <ButtonBase
            onClick={() => setExpanded((v) => !v)}
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-600)',
              padding: `${ds.space[1]} ${ds.space[2]}`,
              borderRadius: 'var(--ds-radius-sm)',
              '&:hover': { color: 'var(--ds-blue-700)', backgroundColor: 'var(--ds-blue-100)' },
              '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
            }}
          >
            {expanded ? collapseLabel : labelTemplate.replace('{n}', String(hiddenCount))}
          </ButtonBase>
        </Box>
      )}
    </Box>
  );
}

export default List;
