import React from 'react';
import { Box } from '@mui/material';
import FilterListIcon from '@mui/icons-material/FilterList';
import { Button } from '@ui/Button';
import { DropdownMenu } from '@ui/DropdownMenu';
import FilterDropdown from '@ui/FilterDropdown';
import { snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';
import type { EntityColumn } from './entityQuery';
import { EXPORT_HIDE_ATTR } from './panelImage';
import type { ViewerFilter } from './panelViewerFilters';

/** Past this many columns the menu takes a search box (DS: no more than ~7 items without one). */
const SEARCHABLE_FROM = 8;

interface MenuProps {
  panelId: number;
  /** Columns not filtered on yet. */
  columns: EntityColumn[];
  onAdd: (column: string) => void;
}

/**
 * The header's column-filter button: the columns a viewer can still filter by.
 * Renders nothing once every column has a filter, rather than an empty menu.
 *
 * Icon-only, like the Account filter beside it — the header is the title's — and
 * a different glyph from that funnel, so the two read as two controls.
 */
export function PanelFilterMenu({ panelId, columns, onAdd }: MenuProps) {
  if (columns.length === 0) return null;
  return (
    // An affordance nobody can press reads as an artefact in an exported image.
    <Box component='span' {...{ [EXPORT_HIDE_ATTR]: 'true' }} sx={{ display: 'inline-flex', flexShrink: 0 }}>
      <DropdownMenu
        align='end'
        size='sm'
        searchable={columns.length >= SEARCHABLE_FROM}
        searchPlaceholder='Search columns…'
        trigger={
          <Button
            tone='ghost'
            size='xs'
            icon={<FilterListIcon />}
            aria-label='Filter by column'
            tooltip='Filter by column'
            data-testid={`panel-filter-add-${panelId}`}
          />
        }
        items={columns.map((c) => ({ label: c.label, searchText: c.label, onSelect: () => onAdd(c.name) }))}
      />
    </Box>
  );
}

interface ValueFilterProps {
  panelId: number;
  column: EntityColumn;
  values: string[];
  onChange: (values: string[]) => void;
  loadValues: (column: string) => Promise<string[]>;
  /** Changes when the accounts or the window do — a list loaded for another one is stale. */
  scopeKey: string;
  /** Opens the dropdown on mount: the viewer just picked this column, and the values are the next step. */
  autoOpen: boolean;
}

/** One column's filter: a multi-select of the values the column holds, loaded when it opens. */
function ColumnValueFilter({ panelId, column, values, onChange, loadValues, scopeKey, autoOpen }: ValueFilterProps) {
  const [options, setOptions] = React.useState<{ label: string; value: string }[]>([]);
  const [loading, setLoading] = React.useState(false);
  // The scope the current list was loaded for, so reopening the dropdown does not ask again.
  const loadedFor = React.useRef<string | null>(null);
  const mounted = React.useRef(true);
  const anchor = React.useRef<HTMLSpanElement>(null);

  // Set in the body as well as cleared in the cleanup: StrictMode runs the cleanup once on mount in dev,
  // and a flag left false there would drop every list as it arrived.
  React.useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const load = () => {
    if (loadedFor.current === scopeKey) return;
    const key = scopeKey;
    loadedFor.current = key;
    setLoading(true);
    // Settled only by the load that is still current: a list for an earlier scope is dropped.
    loadValues(column.name).then(
      (found) => {
        if (!mounted.current || loadedFor.current !== key) return;
        setOptions(found.map((v) => ({ label: v, value: v })));
        setLoading(false);
      },
      () => {
        if (!mounted.current || loadedFor.current !== key) return;
        // Asked again on the next open, and a typed value still works meanwhile.
        loadedFor.current = null;
        setOptions([]);
        setLoading(false);
        snackbar.error(`Could not list the values of ${column.label}. Type one instead.`);
      }
    );
  };

  React.useEffect(() => {
    // FilterDropdown opens on its trigger's click and has no prop for it.
    if (autoOpen) anchor.current?.querySelector('button')?.click();
    // Mount only: the column was just added.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Box component='span' ref={anchor} sx={{ display: 'inline-flex' }}>
      <FilterDropdown
        id={`panel-filter-${panelId}-${column.name}`}
        label={column.label}
        size='sm'
        multiple
        // The list is the store's answer for this window; a value it left out — a
        // capped list, a row table read from its newest rows — can still be typed.
        freeSolo
        // Panel bodies clip their overflow, so the popover must portal.
        disablePortal={false}
        value={values}
        options={options}
        isOptionsLoading={loading}
        onOpen={load}
        searchPlaceholder={`Search ${column.label.toLowerCase()}…`}
        onSelect={(_e: unknown, next: unknown) => {
          const picked = Array.isArray(next) ? next : [];
          onChange(picked.map((v) => String(v && typeof v === 'object' ? (v as { value: unknown }).value : v)));
        }}
      />
    </Box>
  );
}

interface Props {
  panelId: number;
  /** Every column the panel can be filtered by — for the labels. */
  columns: EntityColumn[];
  filters: ViewerFilter[];
  onChange: (filters: ViewerFilter[]) => void;
  loadValues: (column: string) => Promise<string[]>;
  scopeKey: string;
  /** The column added last, whose dropdown opens as it appears. */
  openColumn: string;
}

/**
 * The row of filters under a panel's header, one dropdown per column the viewer
 * added. A dropdown with nothing picked stays — DS §1.6: an empty filter reads as
 * "nothing applied" — and "Clear all" takes the row away.
 */
const PanelColumnFilters: React.FC<Props> = ({ panelId, columns, filters, onChange, loadValues, scopeKey, openColumn }) => {
  if (filters.length === 0) return null;
  return (
    <Box data-testid={`panel-filters-${panelId}`} sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[1], px: 1.25, pt: 1 }}>
      {filters.map((filter) => {
        const column = columns.find((c) => c.name === filter.column);
        if (!column) return null;
        return (
          <ColumnValueFilter
            key={filter.column}
            panelId={panelId}
            column={column}
            values={filter.values}
            onChange={(values) => onChange(filters.map((f) => (f.column === filter.column ? { ...f, values } : f)))}
            loadValues={loadValues}
            scopeKey={scopeKey}
            autoOpen={filter.column === openColumn}
          />
        );
      })}
      <Box component='span' {...{ [EXPORT_HIDE_ATTR]: 'true' }} sx={{ display: 'inline-flex' }}>
        <Button tone='link' size='xs' onClick={() => onChange([])} data-testid={`panel-filters-clear-${panelId}`}>
          Clear all
        </Button>
      </Box>
    </Box>
  );
};

export default PanelColumnFilters;
