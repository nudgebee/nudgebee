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
 *
 * Don't (per spec):
 *   - Don't use List for > 50 items without virtualisation. That's a Table problem.
 *
 * Migration:
 *   `import CustomListWithShowMore from '@components1/common/CustomListWithShowMore'`
 * → `import { List } from '@components1/ds/List'`
 *   Truncation moves from per-list config to the standard `truncate` prop.
 */
import * as React from 'react';
import { Box, ButtonBase } from '@mui/material';

export type ListDivider = 'none' | 'between';

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
  /** Renders one row. Receives the item and its index. */
  renderItem: (item: T, index: number) => React.ReactNode;
  /** Stable key extractor; defaults to index. */
  keyFor?: (item: T, index: number) => React.Key;
  truncate?: ListTruncate;
  divider?: ListDivider;
  /** Render when `items.length === 0`. */
  empty?: React.ReactNode;
  className?: string;
  id?: string;
  /** Group accessible name. */
  ariaLabel?: string;
}

export function List<T>({ items, renderItem, keyFor, truncate, divider = 'between', empty, className, id, ariaLabel }: ListProps<T>) {
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
        border: '1px solid var(--ds-gray-200)',
        borderRadius: 'var(--ds-radius-md)',
        overflow: 'hidden',
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
          {renderItem(item, i)}
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
              padding: '4px 8px',
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
