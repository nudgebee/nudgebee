/**
 * TableCell — DS V2. Net-new primitive (no V1 equivalent).
 * Spec: app/design-system/primitives/navigation/table-cell.html
 *
 * The atomic cell-renderer used inside `ds/Table`. Three densities, seven
 * compositions. Numeric cells are always right-aligned with tabular-nums;
 * subtext uses gray-500; chips/labels inside cells use the smallest density
 * that fits.
 *
 * Note: `ds/Table` already inlines the same compositions via its `column.cellType`
 * enum. `TableCell` is the standalone primitive for use outside Table — e.g. in
 * KeyValue grids, summary blocks, comparison views — and as a building block
 * when consumers need finer control than `Table.columns[].cellType` provides.
 *
 * Variants per spec:
 *   density     = 'xs' | 'sm' | 'md'  (32px / 40px / 48px row heights)
 *   composition = 'main' | 'main+subtext' | 'icon+main' | 'main+chip' | 'label' | 'num' | 'action'
 *                 (auto from props presence; or pass `composition` explicitly)
 *   align       = 'start' | 'end' | 'center'
 *   truncate    = boolean  (ellipsis with `title` tooltip on overflow)
 *
 * Don't (per spec):
 *   - Don't render a Card or composite layout inside a TableCell. The cell is
 *     a flat read; complex content belongs in the Inspector.
 *   - Don't left-align numbers. Right-aligned + tabular-nums is the only way
 *     columns of numbers compare cleanly. (`composition='num'` enforces this.)
 *   - Don't put more than one Chip / Label per cell.
 *   - Don't disable `truncate` on free-text columns.
 */
import * as React from 'react';
import { Box } from '@mui/material';

export type TableCellDensity = 'xs' | 'sm' | 'md';
export type TableCellComposition = 'main' | 'main+subtext' | 'icon+main' | 'main+chip' | 'label' | 'num' | 'action';
export type TableCellAlign = 'start' | 'end' | 'center';

export interface TableCellProps {
  density?: TableCellDensity;
  /** Explicit composition. If omitted, derived from props presence. */
  composition?: TableCellComposition;
  /** Primary text (or any ReactNode). */
  main?: React.ReactNode;
  /** Secondary line under main. */
  subtext?: React.ReactNode;
  /** Optional left icon. */
  icon?: React.ReactNode;
  /** Optional right-aligned chip / label / status badge. */
  chip?: React.ReactNode;
  /** For composition='num': numeric value rendered with tabular-nums + right-align. */
  num?: React.ReactNode;
  /** For composition='action': action button(s) — typically a 3-dot menu. */
  action?: React.ReactNode;
  /** For composition='label': the inline status tag — e.g. `<Label tone='success'>Healthy</Label>`. */
  label?: React.ReactNode;
  align?: TableCellAlign;
  /** Ellipsis with `title` tooltip on overflow (default true). */
  truncate?: boolean;
  className?: string;
  id?: string;
  /** Explicit tooltip text override; defaults to `main` when truncating. */
  title?: string;
}

const DENSITY_HEIGHT: Record<TableCellDensity, string> = {
  xs: '32px',
  sm: '40px',
  md: '48px',
};

const DENSITY_FONT: Record<TableCellDensity, string> = {
  xs: 'var(--ds-text-small)',
  sm: 'var(--ds-text-body)',
  md: 'var(--ds-text-body)',
};

const DENSITY_PAD_X: Record<TableCellDensity, string> = {
  xs: 'var(--ds-space-2)',
  sm: 'var(--ds-space-3)',
  md: 'var(--ds-space-4)',
};

function alignToCss(align: TableCellAlign): 'flex-start' | 'flex-end' | 'center' {
  if (align === 'end') return 'flex-end';
  if (align === 'center') return 'center';
  return 'flex-start';
}

function deriveComposition(p: TableCellProps): TableCellComposition {
  if (p.composition) return p.composition;
  if (p.action !== undefined) return 'action';
  if (p.num !== undefined) return 'num';
  if (p.label !== undefined) return 'label';
  if (p.chip !== undefined) return 'main+chip';
  if (p.icon !== undefined) return 'icon+main';
  if (p.subtext !== undefined) return 'main+subtext';
  return 'main';
}

const TRUNCATE_SX = {
  whiteSpace: 'nowrap' as const,
  overflow: 'hidden' as const,
  textOverflow: 'ellipsis' as const,
};

export function TableCell(props: TableCellProps) {
  const { density = 'sm', main, subtext, icon, chip, num, action, label, align = 'start', truncate = true, className, id, title } = props;

  const composition = deriveComposition(props);
  // 'num' and 'action' force end-alignment per spec
  const resolvedAlign: TableCellAlign = composition === 'num' || composition === 'action' ? 'end' : align;

  const containerSx = {
    display: 'flex',
    alignItems: 'center',
    justifyContent: alignToCss(resolvedAlign),
    gap: 'var(--ds-space-2)',
    minHeight: DENSITY_HEIGHT[density],
    paddingLeft: DENSITY_PAD_X[density],
    paddingRight: DENSITY_PAD_X[density],
    paddingTop: 'var(--ds-space-2)',
    paddingBottom: 'var(--ds-space-2)',
    fontSize: DENSITY_FONT[density],
    color: 'var(--ds-gray-700)',
    minWidth: 0,
    width: '100%',
    boxSizing: 'border-box' as const,
  };

  const tooltipText = title !== undefined ? title : truncate && typeof main === 'string' ? main : undefined;

  const mainText =
    main !== undefined ? (
      <Box
        component='span'
        title={tooltipText}
        sx={{
          ...(truncate ? TRUNCATE_SX : {}),
          color: 'var(--ds-gray-700)',
          lineHeight: 1.3,
        }}
      >
        {main}
      </Box>
    ) : null;

  // Render per composition
  switch (composition) {
    case 'num':
      return (
        <Box id={id} className={className} sx={containerSx}>
          <Box
            component='span'
            sx={{
              fontVariantNumeric: 'tabular-nums',
              color: 'var(--ds-gray-700)',
            }}
          >
            {num !== undefined ? num : main}
          </Box>
        </Box>
      );

    case 'action':
      return (
        <Box id={id} className={className} sx={containerSx}>
          {action}
        </Box>
      );

    case 'label':
      return (
        <Box id={id} className={className} sx={containerSx}>
          {label}
        </Box>
      );

    case 'icon+main':
      return (
        <Box id={id} className={className} sx={containerSx}>
          {icon && (
            <Box
              component='span'
              aria-hidden='true'
              sx={{
                display: 'inline-flex',
                alignItems: 'center',
                flexShrink: 0,
                color: 'var(--ds-gray-600)',
              }}
            >
              {icon}
            </Box>
          )}
          {mainText}
        </Box>
      );

    case 'main+chip':
      return (
        <Box id={id} className={className} sx={containerSx}>
          {mainText}
          {chip !== undefined && <Box sx={{ marginLeft: 'auto', flexShrink: 0 }}>{chip}</Box>}
        </Box>
      );

    case 'main+subtext':
      return (
        <Box id={id} className={className} sx={containerSx}>
          <Box sx={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
            {mainText}
            {subtext !== undefined && (
              <Box
                component='span'
                title={truncate && typeof subtext === 'string' ? subtext : undefined}
                sx={{
                  ...(truncate ? TRUNCATE_SX : {}),
                  fontSize: 'var(--ds-text-caption)',
                  color: 'var(--ds-gray-500)',
                  lineHeight: 1.4,
                  mt: 0.25,
                }}
              >
                {subtext}
              </Box>
            )}
          </Box>
        </Box>
      );

    case 'main':
    default:
      return (
        <Box id={id} className={className} sx={containerSx}>
          {mainText}
        </Box>
      );
  }
}

export default TableCell;
