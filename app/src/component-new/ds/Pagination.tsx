/**
 * Pagination — DS V2 of legacy CustomTablePagination.
 * Spec: app/design-system/primitives/navigation/pagination.html
 *
 * Page navigator. **Sibling to Table; not nested in it.** Standard composition
 * is prev / numbers / next, with first/last when total > 7 pages and ellipsis
 * for skipped ranges.
 *
 * Variants per spec:
 *   composition = 'numbers' | 'numbers+jump' | 'compact'
 *                 - numbers: just prev/next/numbers/ellipsis
 *                 - numbers+jump: numbers + a "X-Y of Z" + rows-per-page picker
 *                 - compact: prev/next + "X of N" only (for tight inspector surfaces)
 *   size        = 'sm' | 'md'
 *
 * Don't (per spec):
 *   - Don't render Pagination for < 1 page of data. Empty table doesn't need
 *     a "Page 1 of 1".
 *   - Don't combine "Load more" and Pagination on the same list. Pick one.
 *
 * Migration:
 *   `import CustomTablePagination from '@components1/common/tables/CustomTablePagination'`
 * → `import { Pagination } from '@components1/ds/Pagination'`
 *   Now standalone — no longer coupled to Table.
 */
import * as React from 'react';
import { Box, ButtonBase, MenuItem, Select, type SelectChangeEvent } from '@mui/material';
import KeyboardArrowLeftIcon from '@mui/icons-material/KeyboardArrowLeft';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';

export type PaginationComposition = 'numbers' | 'numbers+jump' | 'compact';
export type PaginationSize = 'sm' | 'md';

export interface PaginationProps {
  /** Current page (1-based). */
  page: number;
  /** Total row count. */
  total: number;
  /** Rows per page. */
  pageSize: number;
  onChange: (next: { page: number; pageSize: number }) => void;
  composition?: PaginationComposition;
  size?: PaginationSize;
  /** Page-size options for the rows-per-page picker. Defaults `[10, 20, 50, 100]`. */
  pageSizeOptions?: number[];
  /** Show first/last buttons when total pages > 7 (default true). */
  showFirstLast?: boolean;
  className?: string;
  id?: string;
}

const SIZE_TOKENS: Record<PaginationSize, { itemSize: string; fontSize: string; gap: string; selectHeight: string }> = {
  sm: { itemSize: '24px', fontSize: 'var(--ds-text-caption)', gap: '4px', selectHeight: '24px' },
  md: { itemSize: '32px', fontSize: 'var(--ds-text-small)', gap: '6px', selectHeight: '28px' },
};

const ELLIPSIS = '__ellipsis__';

/**
 * Build the visible page numbers per spec: 1, current-1, current, current+1, totalPages,
 * with ellipsis for skipped ranges. For ≤ 7 pages, return all.
 */
function buildPageWindow(currentPage: number, totalPages: number): (number | typeof ELLIPSIS)[] {
  if (totalPages <= 7) {
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  }
  const window: (number | typeof ELLIPSIS)[] = [1];
  const left = Math.max(2, currentPage - 1);
  const right = Math.min(totalPages - 1, currentPage + 1);
  if (left > 2) window.push(ELLIPSIS);
  for (let p = left; p <= right; p++) window.push(p);
  if (right < totalPages - 1) window.push(ELLIPSIS);
  window.push(totalPages);
  return window;
}

function PageItem({
  children,
  active = false,
  disabled = false,
  ariaLabel,
  size,
  onClick,
}: {
  children: React.ReactNode;
  active?: boolean;
  disabled?: boolean;
  ariaLabel?: string;
  size: PaginationSize;
  onClick?: () => void;
}) {
  const tokens = SIZE_TOKENS[size];
  return (
    <ButtonBase
      aria-label={ariaLabel}
      aria-current={active ? 'page' : undefined}
      disabled={disabled}
      onClick={onClick}
      sx={{
        minWidth: tokens.itemSize,
        height: tokens.itemSize,
        padding: '0 var(--ds-space-2)',
        fontSize: tokens.fontSize,
        fontWeight: active ? 'var(--ds-font-weight-semibold)' : 'var(--ds-font-weight-medium)',
        borderRadius: 'var(--ds-radius-sm)',
        color: active ? 'var(--ds-blue-700)' : 'var(--ds-gray-700)',
        backgroundColor: active ? 'var(--ds-blue-100)' : 'transparent',
        border: active ? '1px solid var(--ds-blue-200)' : '1px solid transparent',
        cursor: disabled ? 'default' : 'pointer',
        transition: 'background-color var(--ds-motion-micro) var(--ds-motion-ease)',
        '&:hover': disabled ? undefined : { backgroundColor: active ? 'var(--ds-blue-100)' : 'var(--ds-gray-100)' },
        '&.Mui-disabled': { color: 'var(--ds-gray-400)' },
        '&.Mui-focusVisible': { outline: '2px solid var(--ds-blue-500)', outlineOffset: '1px' },
      }}
    >
      {children}
    </ButtonBase>
  );
}

export function Pagination({
  page,
  total,
  pageSize,
  onChange,
  composition = 'numbers',
  size = 'md',
  pageSizeOptions = [10, 20, 50, 100],
  showFirstLast = true,
  className,
  id,
}: PaginationProps) {
  const tokens = SIZE_TOKENS[size];
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  // Spec: "Don't render Pagination for < 1 page of data."
  if (total === 0) return null;

  const goTo = (next: number) => {
    const clamped = Math.min(Math.max(1, next), totalPages);
    if (clamped !== page) onChange({ page: clamped, pageSize });
  };

  const handlePageSizeChange = (e: SelectChangeEvent<number>) => {
    const newSize = Number(e.target.value);
    // Reset to page 1 when page-size changes (otherwise current page may exceed new totalPages)
    onChange({ page: 1, pageSize: newSize });
  };

  // Compact composition: prev / "X of N" / next only
  if (composition === 'compact') {
    return (
      <Box id={id} className={className} sx={{ display: 'inline-flex', alignItems: 'center', gap: tokens.gap }}>
        <PageItem ariaLabel='Previous page' disabled={page <= 1} size={size} onClick={() => goTo(page - 1)}>
          <KeyboardArrowLeftIcon sx={{ fontSize: 18 }} />
        </PageItem>
        <Box
          component='span'
          sx={{ fontSize: tokens.fontSize, color: 'var(--ds-gray-700)', fontVariantNumeric: 'tabular-nums', padding: '0 var(--ds-space-2)' }}
        >
          {page} of {totalPages}
        </Box>
        <PageItem ariaLabel='Next page' disabled={page >= totalPages} size={size} onClick={() => goTo(page + 1)}>
          <KeyboardArrowRightIcon sx={{ fontSize: 18 }} />
        </PageItem>
      </Box>
    );
  }

  const window = buildPageWindow(page, totalPages);
  const useFirstLast = showFirstLast && totalPages > 7;

  const numbersBlock = (
    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: tokens.gap }}>
      {useFirstLast && (
        <PageItem ariaLabel='First page' disabled={page <= 1} size={size} onClick={() => goTo(1)}>
          «
        </PageItem>
      )}
      <PageItem ariaLabel='Previous page' disabled={page <= 1} size={size} onClick={() => goTo(page - 1)}>
        <KeyboardArrowLeftIcon sx={{ fontSize: 18 }} />
      </PageItem>
      {window.map((entry, i) =>
        entry === ELLIPSIS ? (
          <Box
            key={`ell-${i}`}
            component='span'
            aria-hidden='true'
            sx={{
              minWidth: tokens.itemSize,
              height: tokens.itemSize,
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              fontSize: tokens.fontSize,
              color: 'var(--ds-gray-500)',
              userSelect: 'none',
            }}
          >
            …
          </Box>
        ) : (
          <PageItem key={entry} ariaLabel={`Page ${entry}`} active={entry === page} size={size} onClick={() => goTo(entry)}>
            {entry}
          </PageItem>
        )
      )}
      <PageItem ariaLabel='Next page' disabled={page >= totalPages} size={size} onClick={() => goTo(page + 1)}>
        <KeyboardArrowRightIcon sx={{ fontSize: 18 }} />
      </PageItem>
      {useFirstLast && (
        <PageItem ariaLabel='Last page' disabled={page >= totalPages} size={size} onClick={() => goTo(totalPages)}>
          »
        </PageItem>
      )}
    </Box>
  );

  if (composition === 'numbers') {
    return (
      <Box id={id} className={className}>
        {numbersBlock}
      </Box>
    );
  }

  // composition === 'numbers+jump'
  const start = (page - 1) * pageSize + 1;
  const end = Math.min(page * pageSize, total);

  return (
    <Box
      id={id}
      className={className}
      sx={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 'var(--ds-space-4)',
        flexWrap: 'wrap',
      }}
    >
      {numbersBlock}
      <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-3)', fontSize: tokens.fontSize, color: 'var(--ds-gray-500)' }}>
        <Box component='span' sx={{ fontVariantNumeric: 'tabular-nums' }}>
          {start}–{end} of {total}
        </Box>
        <Box component='span' aria-hidden='true' sx={{ borderLeft: '1px solid var(--ds-gray-200)', height: '1em' }} />
        <Box component='span'>Rows per page</Box>
        <Select
          value={pageSize}
          onChange={handlePageSizeChange}
          size='small'
          sx={{
            height: tokens.selectHeight,
            fontSize: tokens.fontSize,
            color: 'var(--ds-gray-700)',
            '& .MuiOutlinedInput-notchedOutline': { borderColor: 'var(--ds-gray-300)' },
            '&:hover .MuiOutlinedInput-notchedOutline': { borderColor: 'var(--ds-gray-400)' },
          }}
        >
          {pageSizeOptions.map((n) => (
            <MenuItem key={n} value={n} sx={{ fontSize: tokens.fontSize }}>
              {n}
            </MenuItem>
          ))}
        </Select>
      </Box>
    </Box>
  );
}

export default Pagination;
