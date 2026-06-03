/**
 * CustomTable2 — domain swiss-army-knife table for the existing app.
 *
 * This is the DS V2 redesign of CustomTable2 (white header + brand-navy
 * delimiter, top/bottom-only container borders, top-aligned cells, 1px row
 * dividers, skeleton loading state). It is the primary table for the
 * `component-new` migration — preferred over `ds/Table`, which was built from
 * scratch and does not model the features below:
 *   - `upperHeaders` (multi-row grouped column headers)
 *   - `expandable: { tabs, component }` (expandable rows with tabs inside)
 *   - `rowComponent` (caller-supplied custom row component)
 *   - `stickyColumnIndex` / `stickyFirstColumn` (sticky columns vs. only header)
 *   - `resizableColumns` (drag-resize)
 *   - `renderVertical` (pivot mode)
 *   - Built-in pagination + tabs + show-all link + custom empty-state strings
 *   - Inline JSX cell content via `headers[i].component` / `tableData[i][j].component`
 */
import { Box, IconButton, Popover, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Collapse, Typography } from '@mui/material';
import ViewColumnOutlinedIcon from '@mui/icons-material/ViewColumnOutlined';
import ArrowOutwardIcon from '@mui/icons-material/ArrowOutward';
import CustomCheckBox from '@common/CustomCheckbox';
import React, { useEffect, useState, useCallback, memo, useMemo, useRef } from 'react';
import useResizableColumns from '@hooks/useResizableColumns';
import { Button } from '@components1/ds/Button';
import CustomTablePagination from './CustomTablePagination';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import Skeleton from '@components1/ds/Skeleton';
import { capitalize } from '@mui/material/utils';
import PropTypes from 'prop-types';
import EmptyData from '@common/EmptyData';
import { DataNotAvailable, infoIcon, ThumbsUp } from '@assets';
import { ds } from 'src/utils/colors';
import CustomTabs from '@common-new/CustomTabsForDrilldown';
import SafeIcon from '@components1/common/SafeIcon';
import Tooltip from '@components1/ds/Tooltip';
import apiUser from '@api1/user';

const SortIcon = ({ active, direction }) => {
  const activeColor = '#1a1a1a';
  const inactiveColor = '#C0C0C0';
  return (
    <Box component='span' sx={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'center', ml: 'var(--ds-space-1)', lineHeight: 0 }}>
      <svg width='8' height='6' viewBox='0 0 10 6' style={{ marginBottom: 0 }}>
        <path
          d='M1 5L5 1L9 5'
          stroke={active && direction === 'asc' ? activeColor : inactiveColor}
          strokeWidth='1.4'
          strokeLinecap='round'
          strokeLinejoin='round'
          fill='none'
        />
      </svg>
      <svg width='8' height='6' viewBox='0 0 10 6' style={{ marginTop: 'var(--ds-space-1)' }}>
        <path
          d='M1 1L5 5L9 1'
          stroke={active && direction === 'desc' ? activeColor : inactiveColor}
          strokeWidth='1.4'
          strokeLinecap='round'
          strokeLinejoin='round'
          fill='none'
        />
      </svg>
    </Box>
  );
};

SortIcon.propTypes = {
  active: PropTypes.bool,
  direction: PropTypes.string,
};

const DEFAULT_EXPANDABLE = {};
const DEFAULT_HEADERS = [];
const DEFAULT_TABLE_DATA = [];
const AUTO_PAGINATE_THRESHOLD = 10;

const TabPanel = (props) => {
  const { children, value, index, ...other } = props;

  return (
    <div role='tabpanel' hidden={value !== index} id={`table-tabpanel-${index}`} aria-labelledby={`table-tab-${index}`} {...other}>
      {value === index && (
        <Box sx={{ pt: 0.2 }}>
          <Typography>{children}</Typography>
        </Box>
      )}
    </div>
  );
};

TabPanel.propTypes = {
  children: PropTypes.node,
  value: PropTypes.number,
  index: PropTypes.number,
};

const getCellTooltipText = (cell) => {
  if (cell == null) return undefined;
  if (typeof cell === 'string') return cell;
  return cell.tooltipText || cell.data || (typeof cell.text === 'string' ? cell.text : undefined);
};

const getTruncateSx = (truncate) => {
  if (truncate === 'ellipsis') {
    return {
      whiteSpace: 'nowrap',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      display: 'block',
      maxWidth: '100%',
    };
  }
  if (typeof truncate === 'string' && truncate.startsWith('clamp-')) {
    const lines = Number(truncate.split('-')[1]) || 2;
    return {
      display: '-webkit-box',
      WebkitLineClamp: lines,
      WebkitBoxOrient: 'vertical',
      overflow: 'hidden',
      wordBreak: 'break-word',
    };
  }
  return null;
};

// Resolves each visible column's alignment: explicit header `align` wins, otherwise
// fall back to `tableHeadingCenter`, the updated-table last-column right alignment,
// or left. Extracted from CustomTable to keep render-body cognitive complexity low.
const buildCellAligns = (filteredHeaders, tableHeadingCenter, showUpdatedTable, showColumnSelector) =>
  filteredHeaders.map((h, idx) => {
    if (h?.align) return h.align;
    const name = typeof h === 'string' ? h : h?.name;
    if (name && tableHeadingCenter.includes(name)) return 'center';
    const isLast = idx === filteredHeaders.length - 1;
    if (isLast && showUpdatedTable && !showColumnSelector) return 'right';
    return 'left';
  });

// Builds the resize-enabled Table sx: fixed layout, horizontal overflow, and sticky
// first/last columns (first-column sticky is opt-in via stickyFirstColumn).
const buildResizeTableSx = ({ totalTableWidth, isResizing, stickyFirstColumn }) => ({
  width: '100%',
  minWidth: totalTableWidth || '100%',
  tableLayout: 'fixed',
  userSelect: isResizing ? 'none' : 'auto',
  ...(stickyFirstColumn && {
    'thead tr th:first-of-type': {
      position: 'sticky',
      left: 0,
      zIndex: 3,
      background: 'var(--ds-background-100)',
    },
    'tbody tr td:first-of-type': {
      position: 'sticky',
      left: 0,
      zIndex: 1,
      background: 'var(--ds-background-100)',
    },
  }),
  'thead tr th:last-of-type': {
    position: 'sticky',
    right: 0,
    zIndex: 3,
    // Match the teal-100 header fill so the sticky last column doesn't
    // expose a white strip while other columns scroll underneath.
    background: ds.teal[50],
  },
  'tbody tr td:last-of-type': {
    position: 'sticky',
    right: 0,
    zIndex: 1,
    background: 'var(--ds-background-100)',
  },
});

// Builds the legacy (non-resize) Table sx: per-expandable sticky last column plus
// optional sticky column at stickyColumnIndex with viewport-aware offset.
const buildLegacyTableSx = ({ showExpandable, stickyColumnIndex }) => ({
  'tbody tr td:last-of-type': {
    position: showExpandable && 'sticky',
    right: '0px',
  },
  [`tbody tr td:nth-of-type(${stickyColumnIndex})`]: {
    position: 'sticky',
    right: showExpandable ? ds.space.mul(0, 15) : '0px',
    '@media (max-width: 1380px)': {
      right: showExpandable ? ds.space.mul(0, 25) : '0px',
    },
  },
});

const getDrillDownQuery = (row) => {
  let drilldownQuery = {};
  for (let r of row) {
    if (r.drilldownQuery) {
      drilldownQuery = { ...drilldownQuery, ...r.drilldownQuery };
    }
  }
  return drilldownQuery;
};

export const ExpandedRowComponent = ({ row = [], tabOptions = [], isExpanded = false, tabPadding }) => {
  const [tab, setTab] = useState(tabOptions[0]?.value || 0); // Set the default tab value
  // Sticky "has been opened" flag, flipped on synchronously during render so
  // the very first render with isExpanded=true already includes the inner
  // content — MUI Collapse then measures the correct expanded height up front
  // (no open-snap), and keeping content mounted on close lets the exit
  // animation play against real height instead of an unmount.
  const [hasBeenOpened, setHasBeenOpened] = useState(isExpanded);
  if (isExpanded && !hasBeenOpened) {
    setHasBeenOpened(true);
  }

  const handleChangeTab = (e, value) => {
    setTab(value);
  };

  if (!hasBeenOpened) return null;

  return (
    <Box
      sx={{
        // Redesigned Table: the expanded panel shares its background with the
        // expanded row above (gray-300, no top rule) and continues the brand
        // left rail so the pair reads as a single selected section.
        p: 'var(--ds-space-2) var(--ds-space-6)',
        backgroundColor: ds.background[200],
        boxShadow: `inset 3px 0 0 0 ${ds.brand[500]}`,
        borderBottomLeftRadius: ds.radius.lg,
        borderBottomRightRadius: ds.radius.lg,
        '@media (max-width: 1350px)': {
          p: 'var(--ds-space-3)',
        },
      }}
    >
      <Box mb={ds.space[3]}>
        <CustomTabs padding={tabPadding} options={tabOptions} value={tab} onChange={handleChangeTab} />
      </Box>
      {tabOptions.map((option, tabIndex) => (
        <TabPanel key={option.key || ''} value={tab} index={tabIndex}>
          {option.componentFn ? option.componentFn(option, getDrillDownQuery(row), row) : <></>}
        </TabPanel>
      ))}
    </Box>
  );
};
ExpandedRowComponent.propTypes = {
  row: PropTypes.array,
  tabOptions: PropTypes.array,
  isExpanded: PropTypes.bool,
  tabPadding: PropTypes.string,
};

const areRowPropsEqual = (prevProps, nextProps) => {
  // 1. Check strict equality for all props EXCEPT collapsedObj
  const keys = Object.keys(nextProps);
  const prevKeys = Object.keys(prevProps);

  if (keys.length !== prevKeys.length) return false;

  for (const key of keys) {
    if (key === 'collapsedObj') continue;
    if (prevProps[key] !== nextProps[key]) return false;
  }

  // 2. Check collapsedObj specific logic
  // We only care if the expansion state FOR THIS ROW changed.
  const prevExpanded = prevProps.collapsedObj?.[prevProps.itemNo];
  const nextExpanded = nextProps.collapsedObj?.[nextProps.itemNo];

  return prevExpanded === nextExpanded;
};

const ExpandableTableRowBase = ({
  itemNo = 0,
  row = [],
  expandable = DEFAULT_EXPANDABLE,
  checkForTabsWithData,
  showExpandable = false,
  collapsedObj = {},
  handleCollapseOnRow = () => undefined,
  showUpdatedTable,
  onRowClick = () => undefined,
  tabPadding,
  cellAligns,
  cellTruncates,
}) => {
  const [expanded, setExpanded] = useState(false);
  const isExpandable = expandable?.tabs?.length > 0 || showExpandable;
  const ExpandedComponent = expandable?.component || ExpandedRowComponent;

  function defaultAction(row) {
    if (onRowClick) {
      onRowClick(getDrillDownQuery(row));
    }
  }

  return (
    <>
      <TableRow
        sx={{
          cursor: isExpandable || onRowClick ? 'pointer' : 'auto',
          ...(isExpandable && {
            '&:hover': {
              backgroundColor: ds.background[200],
            },
          }),
          '& td': {
            transition: 'background-color 220ms ease, box-shadow 220ms ease, border-radius 220ms ease',
            // Redesigned Table: an expanded row tints to gray-300 (matching the
            // panel below) and drops its divider so the row + panel read as one
            // selected unit, accented by a brand-navy left rail.
            ...(isExpandable && collapsedObj[itemNo]
              ? {
                  backgroundColor: ds.background[200],
                  borderBottom: '0 !important',
                  '&:first-of-type': {
                    boxShadow: `inset 3px 0 0 0 ${ds.brand[500]}`,
                    borderTopLeftRadius: ds.radius.lg,
                  },
                  '&:last-of-type': {
                    borderTopRightRadius: ds.radius.lg,
                  },
                }
              : {}),
          },
          ...row.sx,
        }}
        onClick={(e) => {
          // Don't toggle expansion when the click originated from an interactive
          // element inside a cell (Investigate button, kebab menu, links, etc.).
          // Those clicks bubble up to the row by default; the row toggle should
          // only fire when the user clicks empty cell chrome.
          if (e.target.closest('button, a, input, select, textarea, [role="button"], [role="link"], [role="menuitem"], [role="tab"]')) {
            return;
          }
          checkForTabsWithData ? checkForTabsWithData(getDrillDownQuery(row)) : defaultAction(row);
          handleCollapseOnRow(!expanded, itemNo);
          setExpanded(!expanded);
        }}
      >
        {row.map((cell, index) => {
          const cellAlign = cell?.align || cellAligns?.[index] || (showUpdatedTable && index === row.length - 1 ? 'right' : 'left');
          const justify = cellAlign === 'center' ? 'center' : cellAlign === 'right' ? 'flex-end' : 'flex-start';
          const truncate = cellTruncates?.[index];
          const truncateSx = getTruncateSx(truncate);
          const tooltipText = truncateSx ? getCellTooltipText(cell) : undefined;
          const rawContent = cell?.component || cell?.text;
          const truncated = truncateSx ? <Box sx={truncateSx}>{rawContent}</Box> : rawContent;
          const wrapped = tooltipText ? <Tooltip title={tooltipText}>{truncated}</Tooltip> : truncated;
          return (
            <TableCell
              key={index}
              align={cellAlign}
              sx={{
                fontWeight: showUpdatedTable ? 500 : 400,
                fontSize: showUpdatedTable ? ds.text.small : 'inherit',
                color: showUpdatedTable && 'var(--ds-gray-700)',
                ...(truncateSx ? { maxWidth: 0 } : {}),
              }}
              data-export-enabled={cell?.exportEnabled ?? true}
              data-export-data={cell?.data || cell?.text?.props?.value || cell?.text}
            >
              {cellAlign !== 'left' ? (
                <Box sx={{ display: 'flex', justifyContent: justify, alignItems: 'center', minWidth: 0, width: '100%' }}>{wrapped}</Box>
              ) : (
                wrapped
              )}
            </TableCell>
          );
        })}
        {isExpandable ? (
          <TableCell
            data-export-enabled={false}
            sx={{ width: ds.space.mul(0, 20), padding: 'var(--ds-space-2)' }}
            onClick={(e) => e.stopPropagation()}
          >
            <Box display='flex' justifyContent='flex-end'>
              <IconButton
                aria-label={collapsedObj[itemNo] ? 'Collapse row' : 'Expand row'}
                aria-expanded={!!collapsedObj[itemNo]}
                size='small'
                onClick={() => {
                  checkForTabsWithData && checkForTabsWithData(getDrillDownQuery(row));
                  handleCollapseOnRow(!expanded, itemNo);
                  setExpanded(!expanded);
                }}
                sx={{
                  color: ds.gray[500],
                  transition: 'transform 160ms ease, color 120ms ease',
                  transform: collapsedObj[itemNo] ? 'rotate(180deg)' : 'rotate(0deg)',
                  '&:hover': { color: ds.gray[700], backgroundColor: ds.gray[100] },
                }}
              >
                <KeyboardArrowDownIcon fontSize='small' />
              </IconButton>
            </Box>
          </TableCell>
        ) : null}
      </TableRow>
      {isExpandable ? (
        <TableRow>
          <TableCell
            colSpan={row.length + (isExpandable ? 1 : 0)}
            style={{ padding: 0, maxWidth: 0, borderBottom: collapsedObj[itemNo] ? `1px solid ${ds.gray[200]}` : 0 }}
            data-export-enabled={false}
          >
            <Collapse timeout={280} in={collapsedObj[itemNo]}>
              <ExpandedComponent tabPadding={tabPadding} row={row} tabOptions={expandable.tabs} isExpanded={collapsedObj[itemNo]} />
            </Collapse>
          </TableCell>
        </TableRow>
      ) : null}
    </>
  );
};

ExpandableTableRowBase.propTypes = {
  itemNo: PropTypes.number,
  row: PropTypes.array,
  expandable: PropTypes.shape({
    tabs: PropTypes.array,
    component: PropTypes.any,
  }),
  checkForTabsWithData: PropTypes.any,
  showExpandable: PropTypes.bool,
  collapsedObj: PropTypes.object,
  handleCollapseOnRow: PropTypes.func,
  showUpdatedTable: PropTypes.bool,
  onRowClick: PropTypes.func,
  tabPadding: PropTypes.string,
  cellAligns: PropTypes.array,
  cellTruncates: PropTypes.array,
};

export const ExpandableTableRow = memo(ExpandableTableRowBase, areRowPropsEqual);
ExpandableTableRow.displayName = 'ExpandableTableRow';

/**
 * @param {{
 *  tableData?: any[],
 *  headers?: any[],
 *  rowsPerPage?: number,
 *  expandable?: any,
 *  onRowClick?: Function,
 *  sx?: object,
 *  renderVertical?: boolean
 * }} props
 */

const CustomTable = ({
  tableData = DEFAULT_TABLE_DATA,
  headers = DEFAULT_HEADERS,
  upperHeaders,
  rowComponent: RowComponent = ExpandableTableRow,
  expandable = DEFAULT_EXPANDABLE,
  rowsPerPage,
  sort = {},
  totalRows,
  onPageChange,
  onSortChange,
  showAllLink = false,
  linkToShowAll = '',
  borderSpacing = '0',
  id,
  checkForTabsWithData,
  showExpandable = false,
  loading = false,
  errorMessage = '',
  pageNumber = 1,
  rounded,
  timeStampMinWidth = false,
  tableHeadingCenter = [],
  stickyColumnIndex = '',
  showUpdatedEmptyData = false,
  showEmptyStateText = false,
  emptyStateText = 'No Data Available',
  showUpdatedTable = false,
  onRowClick,
  tabPadding,
  sx = {},
  renderVertical = false,
  hideHeader = false,
  resizableColumns = false,
  stickyFirstColumn = false,
  resetPage = '',
}) => {
  const isExpandableRows = expandable?.tabs?.length > 0 || showExpandable;
  const tableContainerRef = useRef(null);
  const [collapsedObj, setCollapsedObj] = useState(Object.fromEntries(Array.from({ length: rowsPerPage }, (_, index) => [index, false])));

  // --- Column visibility feature ---
  const hasDefaultVisibleConfig = useMemo(() => headers.some((h) => typeof h === 'object' && h.defaultVisible !== undefined), [headers]);
  const showColumnSelector = hasDefaultVisibleConfig && !upperHeaders?.length;

  const deriveVisibleIndices = useCallback(
    (hdrs) => {
      if (!hasDefaultVisibleConfig) return hdrs.map((_, i) => i);
      return hdrs.map((h, i) => (typeof h === 'string' || h.defaultVisible === true ? i : null)).filter((i) => i !== null);
    },
    [hasDefaultVisibleConfig]
  );

  const [visibleColumnIndices, setVisibleColumnIndices] = useState(() => deriveVisibleIndices(headers));
  const [columnSelectorOpen, setColumnSelectorOpen] = useState(false);
  const columnSelectorBtnRef = useRef(null);

  useEffect(() => {
    setVisibleColumnIndices(deriveVisibleIndices(headers));
  }, [headers, deriveVisibleIndices]);

  const filteredHeaders = useMemo(
    () => (showColumnSelector ? visibleColumnIndices.map((i) => headers[i]) : headers),
    [headers, visibleColumnIndices, showColumnSelector]
  );

  const cellAligns = useMemo(
    () => buildCellAligns(filteredHeaders, tableHeadingCenter, showUpdatedTable, showColumnSelector),
    [filteredHeaders, showUpdatedTable, showColumnSelector, tableHeadingCenter]
  );

  const cellTruncates = useMemo(() => filteredHeaders.map((h) => h?.truncate || null), [filteredHeaders]);

  const resizeEnabled = resizableColumns && !upperHeaders?.length;
  const { columnWidths, totalTableWidth, isResizing, handleResizeStart } = useResizableColumns({
    headers: filteredHeaders,
    containerRef: tableContainerRef,
    enabled: resizeEnabled,
  });

  const filteredTableData = useMemo(() => {
    if (!showColumnSelector) return tableData;
    return tableData.map((originalRow) => {
      const filtered = visibleColumnIndices.map((i) => originalRow[i]);
      const allDrilldown = {};
      for (const cell of originalRow) {
        if (cell?.drilldownQuery) Object.assign(allDrilldown, cell.drilldownQuery);
      }
      if (filtered.length > 0 && Object.keys(allDrilldown).length > 0) {
        const firstCell = filtered[0];
        filtered[0] =
          typeof firstCell === 'object' && firstCell !== null
            ? { ...firstCell, drilldownQuery: allDrilldown }
            : { text: firstCell, drilldownQuery: allDrilldown };
      }
      if (originalRow.sx) filtered.sx = originalRow.sx;
      return filtered;
    });
  }, [tableData, visibleColumnIndices, showColumnSelector]);

  const handleColumnToggle = useCallback((idx) => {
    setVisibleColumnIndices((prev) => {
      if (prev.includes(idx)) {
        if (prev.length <= 1) return prev;
        return prev.filter((i) => i !== idx).sort((a, b) => a - b);
      }
      return [...prev, idx].sort((a, b) => a - b);
    });
  }, []);
  // --- End column visibility feature ---

  // Client-side pagination: auto-paginate when onPageChange is not provided and data exceeds threshold
  const [clientPage, setClientPage] = useState(1);
  const [clientRowPerPage, setClientRowPerPage] = useState(apiUser.getUserPreferencesTablePageSize() ?? 10);
  const isClientPaginated = !onPageChange && (tableData?.length ?? 0) > AUTO_PAGINATE_THRESHOLD;

  useEffect(() => {
    if (isClientPaginated) {
      setCollapsedObj(Object.fromEntries(Array.from({ length: clientRowPerPage }, (_, index) => [index, false])));
    }
  }, [isClientPaginated]);

  const prevDataLengthRef = useRef(tableData?.length);
  useEffect(() => {
    if (prevDataLengthRef.current !== tableData?.length) {
      setClientPage(1);
      prevDataLengthRef.current = tableData?.length;
    }
  }, [tableData]);

  const prevResetPageRef = useRef(resetPage);
  useEffect(() => {
    if (resetPage !== '' && prevResetPageRef.current !== resetPage) {
      setClientPage(1);
    }
    prevResetPageRef.current = resetPage;
  }, [resetPage]);

  useEffect(() => {
    if (loading) {
      const pageSize = isClientPaginated ? clientRowPerPage : rowsPerPage;
      setCollapsedObj(Object.fromEntries(Array.from({ length: pageSize }, (_, index) => [index, false])));
    }
  }, [loading]);

  const handleCollapseOnRow = useCallback((expanded, index) => {
    setCollapsedObj((prevCollapsedObj) => {
      // Create shallow copy to avoid mutation
      const newCollapsedObj = { ...prevCollapsedObj };

      if (expanded) {
        // Set all values to false
        Object.keys(newCollapsedObj).forEach((key) => {
          newCollapsedObj[key] = false;
        });
      }
      // Set the value at the specified index
      newCollapsedObj[index] = expanded;
      return newCollapsedObj;
    });
  }, []);

  const onPageChangeInternal = (page, limit) => {
    onPageChange(page, limit);
    setCollapsedObj(Object.fromEntries(Array.from({ length: rowsPerPage }, (_, index) => [index, false])));
  };

  rowsPerPage = rowsPerPage ?? 5;

  totalRows = totalRows || (tableData?.length ?? 1);

  const visibleTableData = useMemo(() => {
    const sourceData = showColumnSelector ? filteredTableData : tableData;
    if (!isClientPaginated) return sourceData;
    const start = (clientPage - 1) * clientRowPerPage;
    return sourceData.slice(start, start + clientRowPerPage);
  }, [tableData, filteredTableData, clientPage, clientRowPerPage, isClientPaginated, showColumnSelector]);

  const renderVerticalTable = () => {
    const verticalData = showColumnSelector ? filteredTableData : tableData;
    if (!verticalData || verticalData.length !== 1) {
      return null;
    }

    const singleRow = verticalData[0];

    return (
      <TableContainer
        sx={{
          ...sx,
          mt: !showUpdatedTable ? ds.space[4] : ds.space.mul(0, 5),
          mb: 'var(--ds-space-2)',
          borderRadius: rounded || ds.radius.lg,
          '&::-webkit-scrollbar': {
            height: ds.space[1],
          },
        }}
      >
        <Table
          sx={{
            width: '100%',
            tableLayout: 'fixed',
            'td, th': {
              color: 'var(--ds-gray-700)',
              padding: showUpdatedTable ? ds.space[3] : `${ds.space.mul(0, 5)} ${ds.space.mul(0, 5)} ${ds.space.mul(0, 5)} ${ds.space.mul(0, 10)}`,
              '@media (max-width: 1350px)': {
                fontSize: 'var(--ds-text-caption)',
                padding: showUpdatedTable
                  ? `${ds.space[3]} ${ds.space.mul(0, 2)} ${ds.space[3]} ${ds.space[2]}`
                  : `${ds.space.mul(0, 5)} ${ds.space.mul(0, 2)} ${ds.space.mul(0, 5)} ${ds.space[2]}`,
              },
            },
            borderCollapse: 'collapse',
          }}
          aria-label='vertical-table'
          id={`${id}-vertical`}
        >
          <TableHead>
            <TableRow
              sx={{
                th: {
                  fontWeight: showUpdatedTable ? 600 : 700,
                  lineHeight: '17px',
                  background: 'var(--ds-blue-100)',
                  borderBottom: '0px',
                  '&:nth-of-type(1)': {
                    borderRadius: 'var(--ds-radius-lg) 0px 0px 0px',
                  },
                  '&:nth-of-type(2)': {
                    borderRadius: '0px var(--ds-radius-lg) 0px 0px',
                  },
                },
              }}
            >
              <TableCell sx={{ fontWeight: 'var(--ds-font-weight-semibold)', width: '25%' }}>Field</TableCell>
              <TableCell sx={{ fontWeight: 'var(--ds-font-weight-semibold)', width: '75%' }}>Value</TableCell>
            </TableRow>
          </TableHead>
          <TableBody
            sx={{
              background: 'var(--ds-background-100)',
              'tr:last-of-type': {
                'td:first-of-type': {
                  borderRadius: '0 0 0 var(--ds-radius-lg)',
                },
                'td:last-of-type': {
                  borderRadius: '0 0 var(--ds-radius-lg) 0',
                },
              },
            }}
          >
            {filteredHeaders.map((header, index) => {
              const headerName = typeof header === 'string' ? header : header.name || header;
              const cellData = singleRow[index];

              return (
                <TableRow key={headerName}>
                  <TableCell
                    sx={{
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      fontSize: 'var(--ds-text-body-lg)',
                      backgroundColor: 'var(--ds-blue-100)',
                      borderRight: `1px solid ${'var(--ds-gray-400)'}`,
                      width: '25%',
                      '@media (max-width: 1350px)': {
                        fontSize: 'var(--ds-text-small)',
                      },
                    }}
                  >
                    {capitalize(headerName)}
                  </TableCell>
                  <TableCell sx={{ width: '75%', wordBreak: 'break-word' }}>{cellData?.component || cellData?.text || cellData}</TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </TableContainer>
    );
  };
  const handleRequestSort = (h, i) => {
    if (onSortChange) {
      const originalIndex = showColumnSelector ? visibleColumnIndices[i] : i;
      onSortChange(
        {
          name: h.name,
          order: sort?.name === h.name && sort?.order === 'asc' ? 'desc' : 'asc',
        },
        originalIndex
      );
    }
  };

  const renderTableContent = (id) => {
    if (visibleTableData?.length > 0) {
      return (
        <TableBody
          sx={{
            tr: {
              td: {
                '&:nth-of-type(1)': {
                  wordBreak: filteredHeaders[0]?.breakWord,
                  '@media (max-width: 1350px)': {
                    minWidth: timeStampMinWidth ? `${ds.space.mul(0, 40)} !important` : `${ds.space.mul(0, 60)} !important`,
                  },
                },
              },
              [`&:nth-last-of-type(${isExpandableRows ? 2 : 1})`]: {
                td: {
                  '&:nth-of-type(1)': {
                    borderRadius: '0 0 0 var(--ds-radius-lg)',
                  },
                  '&:nth-last-of-type(1)': {
                    borderRadius: '0 0 var(--ds-radius-lg) 0',
                  },
                },
              },
            },
            // Redesigned Table: body surface is always white — the rhythm is
            // carried by whitespace and the 1px row dividers, not a row fill.
            background: ds.background[100],
          }}
          id={`${id}-body`}
        >
          {visibleTableData.map((row, index) => {
            const globalIndex = isClientPaginated ? (clientPage - 1) * clientRowPerPage + index : index;
            return (
              <RowComponent
                itemNo={index}
                key={globalIndex}
                row={row}
                expandable={expandable}
                checkForTabsWithData={checkForTabsWithData}
                showExpandable={showExpandable}
                collapsedObj={collapsedObj}
                handleCollapseOnRow={handleCollapseOnRow}
                onRowClick={onRowClick}
                tabPadding={tabPadding}
                cellAligns={cellAligns}
                cellTruncates={cellTruncates}
              />
            );
          })}
        </TableBody>
      );
    }

    return null;
  };

  // Loading state: render skeleton rows inside the table body so column
  // widths and overall layout don't shift when data arrives. Cap at 10 rows
  // per the Skeleton DS guidance ("5 is enough, don't render >10").
  const renderSkeletonBody = () => {
    const skeletonRowCount = Math.min(rowsPerPage || 5, 10);
    return (
      <TableBody>
        {Array.from({ length: skeletonRowCount }).map((_, rowIdx) => (
          <TableRow key={`skel-${rowIdx}`}>
            {filteredHeaders.map((_h, colIdx) => (
              <TableCell key={colIdx}>
                <Skeleton shape='text' size='text' width='80%' />
              </TableCell>
            ))}
            {isExpandableRows ? <TableCell /> : null}
          </TableRow>
        ))}
      </TableBody>
    );
  };

  const renderEmptyState = () => {
    if (!tableData || tableData.length > 0 || loading) {
      return null;
    }

    let content;
    if (errorMessage) {
      content = errorMessage;
    } else if (showEmptyStateText) {
      content = (
        <Typography
          sx={{
            color: 'var(--ds-gray-600)',
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-regular)',
            fontFamily: 'var(--ds-font-display)',
          }}
        >
          {emptyStateText}
        </Typography>
      );
    } else if (showUpdatedEmptyData) {
      content = <EmptyData img={ThumbsUp} heading='All good here!' subHeading='No recommendations available' />;
    } else {
      content = (
        <EmptyData id={id} img={DataNotAvailable} heading='No Data Available' subHeading='Please check back later or try refreshing the page' />
      );
    }

    return (
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'center',
          alignItems: 'center',
          width: '100%',
          minHeight: showUpdatedEmptyData ? ds.space.mul(0, 150) : ds.space.mul(0, 50),
        }}
      >
        <div
          style={{
            color: 'var(--ds-gray-700)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: !showEmptyStateText && 'center',
          }}
        >
          {content}
        </div>
      </Box>
    );
  };

  const renderPaginationOrViewAll = () => {
    if (tableData?.length === 0 || loading) {
      return null;
    }

    if (showAllLink) {
      if (!linkToShowAll) return null;
      // Footer owns only top breath (separates button from last table row).
      // Bottom padding is left to the wrapping DSCard — adding `pb` here on
      // top of the card's own `pb` visibly stacked ~25-30px of dead space
      // below the button. Consumers that want a tight bottom must override
      // DSCard's `pb` directly.
      return (
        <Box display='flex' justifyContent='flex-end' width='100%' pt={ds.space[2]} pb={0} pr={ds.space[3]}>
          <Button
            tone='link'
            size='sm'
            icon={<ArrowOutwardIcon sx={{ fontSize: 14 }} />}
            iconPlacement='end'
            onClick={() => window.open(linkToShowAll, '_blank')}
          >
            View all
          </Button>
        </Box>
      );
    }

    if (onPageChange) {
      return (
        <CustomTablePagination
          page={totalRows < rowsPerPage ? 1 : pageNumber}
          totalPages={Math.ceil(totalRows / rowsPerPage)}
          rowsPerPage={rowsPerPage}
          onPageChange={onPageChangeInternal}
          totalRows={totalRows}
          hideLeftIcons={true}
        />
      );
    }

    if (isClientPaginated) {
      return (
        <CustomTablePagination
          page={clientPage}
          totalPages={Math.ceil(tableData.length / clientRowPerPage)}
          rowsPerPage={clientRowPerPage}
          onPageChange={(page, limit) => {
            setClientPage(page);
            setClientRowPerPage(limit);
            setCollapsedObj(Object.fromEntries(Array.from({ length: limit }, (_, i) => [i, false])));
          }}
          totalRows={tableData.length}
        />
      );
    }

    return <></>;
  };

  // Return vertical table if conditions are met
  if (renderVertical) {
    return (
      <>
        {renderVerticalTable()}
        {renderEmptyState()}
        {renderPaginationOrViewAll()}
      </>
    );
  }

  // Return normal horizontal table
  return (
    <>
      <TableContainer
        ref={tableContainerRef}
        sx={{
          ...sx,
          mt: !showUpdatedTable ? ds.space[4] : ds.space.mul(0, 5),
          // Redesigned Table: outer borders are top + bottom only — no left/right
          // rules so the table breathes against the page surface. Suppress
          // when the body is empty (and we aren't loading) so the bottom rule
          // doesn't dangle right under the header pill.
          borderBottom: tableData?.length > 0 || loading ? `1px solid ${ds.gray[200]}` : 'none',
          borderRadius: rounded || '',
          '&::-webkit-scrollbar': {
            height: ds.space[1],
          },
          ...(resizeEnabled && { overflowX: 'auto' }),
        }}
      >
        <Table
          sx={{
            ...(resizeEnabled
              ? buildResizeTableSx({ totalTableWidth, isResizing, stickyFirstColumn })
              : buildLegacyTableSx({ showExpandable, stickyColumnIndex })),
            // Redesigned Table: cells top-aligned so two-line content anchors to
            // the row top. Font and padding stay responsive below 1350px.
            'td, th': {
              fontSize: 'var(--ds-text-small)',
              letterSpacing: '-0.01em',
              verticalAlign: 'top',
              '@media (max-width: 1350px)': {
                fontSize: 'var(--ds-text-caption)',
              },
            },
            td: {
              color: 'var(--ds-gray-700)',
              padding: 'var(--ds-space-3) var(--ds-space-4)',
              '@media (max-width: 1350px)': {
                padding: 'var(--ds-space-3) var(--ds-space-2)',
              },
            },
            th: {
              padding: 'var(--ds-space-3) var(--ds-space-4)',
              '@media (max-width: 1350px)': {
                padding: 'var(--ds-space-2) var(--ds-space-2)',
              },
            },
            // Row dividers: 1px gray-200; the last row's bottom rule is
            // suppressed in favour of the container's bottom border.
            'tbody tr td': {
              borderBottom: `1px solid ${ds.gray[200]}`,
            },
            [`tbody tr:nth-last-of-type(${isExpandableRows ? 2 : 1}) td`]: {
              borderBottom: 0,
            },
            borderCollapse: 'collapse',
            'tr:first-of-type th:first-of-type': {
              borderSpacing: 0,
            },
            borderSpacing: { borderSpacing },
            'tbody tr': {
              cursor: showExpandable || onRowClick ? 'pointer' : 'auto',
            },
          }}
          aria-label='table'
          id={id}
        >
          {resizeEnabled && columnWidths.length === filteredHeaders.length && (
            <colgroup data-resizable-colgroup='true'>
              {filteredHeaders.map((head, idx) => {
                const isLast = idx === filteredHeaders.length - 1;
                return <col key={head?.name || head || idx} style={isLast ? undefined : { width: `${columnWidths[idx]}px` }} />;
              })}
              {isExpandableRows ? <col style={{ width: ds.space[7] }} /> : null}
            </colgroup>
          )}
          <TableHead
            sx={{
              ...(hideHeader && {
                display: 'none',
              }),
            }}
          >
            {!!upperHeaders && (
              <TableRow
                sx={{
                  background: 'transparent',
                  th: {
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    lineHeight: '13px',
                    borderBottom: '0',
                    padding: 'var(--ds-space-1) var(--ds-space-3)',
                  },
                }}
              >
                {upperHeaders?.map((head, _idx) =>
                  head?.text ? (
                    <TableCell
                      key={head?.text}
                      align={'center'}
                      colSpan={head.colSpan ?? 1}
                      size='small'
                      sx={{
                        width: 'max-content',
                        backgroundColor: head?.backgroundColor ?? 'var(--ds-gray-100)',
                        borderInline: `1px solid ${'var(--ds-gray-200)'} `,
                        borderTop: `1px solid ${'var(--ds-gray-200)'} `,
                        borderRadius: 'var(--ds-radius-lg) var(--ds-radius-lg) 0 0',
                      }}
                    >
                      {head?.text ?? ''}
                    </TableCell>
                  ) : (
                    <TableCell key={head.id} sx={{ width: 'max-content' }} />
                  )
                )}
              </TableRow>
            )}
            <TableRow
              sx={{
                // Redesigned Table header: tinted teal-100 surface with no
                // bottom rule — the header reads as a standalone bar floating
                // above the body. First/last cells round to --ds-radius-md so
                // the whole row carries a pill-like silhouette.
                th: {
                  fontWeight: 'var(--ds-font-weight-medium)',
                  fontFamily: 'var(--ds-font-display)',
                  lineHeight: '17px',
                  color: ds.brand[600],
                  background: ds.teal[50],
                  // Suppress MUI's default TableCell bottom border — the
                  // outline is painted via box-shadow below so it can follow
                  // the rounded corners (borders on td/th with
                  // border-collapse:collapse don't respect border-radius).
                  borderBottom: 'none',

                  boxShadow: `inset 0 1px 0 0 ${ds.teal[100]}, inset 0 -1px 0 0 ${ds.teal[100]}`,
                  '&:first-of-type': {
                    borderTopLeftRadius: 'var(--ds-radius-xl)',
                    borderBottomLeftRadius: 'var(--ds-radius-xl)',
                    // Add the left edge of the outline; the inset shadow
                    // follows the rounded top-left + bottom-left corners.
                    boxShadow: `inset 1px 0 0 0 ${ds.teal[100]}, inset 0 1px 0 0 ${ds.teal[100]}, inset 0 -1px 0 0 ${ds.teal[100]}`,
                  },
                  '&:last-of-type': {
                    borderTopRightRadius: 'var(--ds-radius-xl)',
                    borderBottomRightRadius: 'var(--ds-radius-xl)',

                    // Add the right edge of the outline; follows the rounded
                    // top-right + bottom-right corners.
                    boxShadow: `inset -1px 0 0 0 ${ds.teal[100]}, inset 0 1px 0 0 ${ds.teal[100]}, inset 0 -1px 0 0 ${ds.teal[100]}`,
                  },
                },
              }}
            >
              {filteredHeaders.map((head, idx) => {
                const isInCenterList = tableHeadingCenter.includes(head.name || head);
                const isLastColumn = idx === filteredHeaders.length - 1;
                const isLastNamedColumn = !isLastColumn && idx === filteredHeaders.findLastIndex((h) => (typeof h === 'string' ? h : h?.name));
                const showColumnIcon = showColumnSelector && (isLastColumn || isLastNamedColumn) && (isLastColumn ? !!(head?.name || head) : true);
                let alignment = head?.align || '';

                if (!alignment) {
                  if (isInCenterList) {
                    alignment = 'center';
                  } else if (isLastColumn && showUpdatedTable && !showColumnSelector) {
                    alignment = 'right';
                  }
                }

                let thWidth;
                if (resizeEnabled) {
                  thWidth = isLastColumn ? undefined : columnWidths[idx] || head?.width || '20%';
                } else {
                  thWidth = head?.width || '20%';
                }
                return (
                  <TableCell
                    key={head?.name || head}
                    align={alignment || 'left'}
                    sx={resizeEnabled ? { position: 'relative', overflow: 'hidden' } : undefined}
                    width={thWidth}
                    data-export-enabled={!!(head.name || head) && head.exportEnabled}
                    data-export-data={head.name || head}
                  >
                    {(sort?.name && head.name == sort?.name && head?.sortEnabled) || head?.sortEnabled ? (
                      <Box
                        component='span'
                        onClick={() => handleRequestSort(head, idx)}
                        sx={{ display: 'inline-flex', alignItems: 'center', cursor: 'pointer', userSelect: 'none', mr: 'var(--ds-space-1)' }}
                      >
                        {head.component ? head.component : capitalize(typeof head === 'string' ? head : head.name)}{' '}
                        <span style={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                          {head.secondryText}
                        </span>
                        <SortIcon active={sort?.name === (head?.name || head)} direction={sort?.order} />
                      </Box>
                    ) : (
                      <>
                        {head.component ? head.component : capitalize(typeof head === 'string' ? head : head.name)}{' '}
                        <span style={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                          {head.secondryText}
                        </span>
                      </>
                    )}
                    {head?.info && (
                      <Tooltip title={head.info} placement={head?.infoPlacement}>
                        <Box component='span' sx={{ position: 'relative', top: ds.space[0], opacity: '50%' }}>
                          <SafeIcon src={infoIcon} alt='info' width={12} height={14} />{' '}
                        </Box>
                      </Tooltip>
                    )}
                    {showColumnIcon && (
                      <Tooltip title='Select columns' ref={columnSelectorBtnRef}>
                        <IconButton
                          size='small'
                          onClick={(e) => {
                            e.stopPropagation();
                            setColumnSelectorOpen(true);
                          }}
                          data-testid='column-selector-btn'
                          sx={{
                            ml: 'var(--ds-space-1)',
                            p: 'var(--ds-space-1)',
                            verticalAlign: 'middle',
                            color: 'var(--ds-gray-500)',
                            '&:hover': { color: 'var(--ds-blue-600)' },
                          }}
                        >
                          <ViewColumnOutlinedIcon sx={{ fontSize: 'var(--ds-text-title)' }} />
                        </IconButton>
                      </Tooltip>
                    )}
                    {resizeEnabled && idx < filteredHeaders.length - 1 && (
                      <Box
                        onMouseDown={(e) => handleResizeStart(idx, e)}
                        sx={{
                          position: 'absolute',
                          right: 0,
                          top: 0,
                          bottom: 0,
                          width: ds.space[2],
                          cursor: 'col-resize',
                          zIndex: 1,
                          display: 'flex',
                          justifyContent: 'center',
                          alignItems: 'center',
                          '&::after': {
                            content: '""',
                            width: '1px',
                            height: '60%',
                            backgroundColor: 'var(--ds-gray-300)',
                            transition: 'background-color 0.15s, width 0.15s',
                          },
                          '&:hover::after': {
                            width: ds.space[0],
                            backgroundColor: 'var(--ds-blue-600)',
                          },
                          '&:active::after': {
                            width: ds.space[0],
                            backgroundColor: 'var(--ds-blue-600)',
                          },
                        }}
                        data-testid={`column-resize-handle-${idx}`}
                      />
                    )}
                  </TableCell>
                );
              })}
              {isExpandableRows ? <TableCell data-export-enabled={false} /> : null}
            </TableRow>
          </TableHead>

          {loading ? renderSkeletonBody() : renderTableContent(id)}
        </Table>
      </TableContainer>

      {showColumnSelector && (
        <Popover
          open={columnSelectorOpen}
          anchorEl={() => columnSelectorBtnRef.current}
          onClose={() => setColumnSelectorOpen(false)}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
          transformOrigin={{ vertical: 'top', horizontal: 'right' }}
          slotProps={{
            paper: { sx: { p: 'var(--ds-space-3)', borderRadius: 'var(--ds-radius-lg)', maxHeight: ds.space.mul(0, 160), overflowY: 'auto' } },
          }}
        >
          <Typography
            sx={{
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              mb: 'var(--ds-space-2)',
              color: 'var(--ds-gray-700)',
            }}
          >
            Show Columns
          </Typography>
          {headers.map((head, idx) => {
            const headerName = typeof head === 'string' ? head : head.name;
            if (!headerName) return null;
            return (
              <Box key={idx} sx={{ display: 'flex', alignItems: 'center' }}>
                <CustomCheckBox
                  checked={visibleColumnIndices.includes(idx)}
                  onChange={() => handleColumnToggle(idx)}
                  text={capitalize(headerName)}
                  checkboxStyle={{ padding: 'var(--ds-space-1)' }}
                />
              </Box>
            );
          })}
        </Popover>
      )}

      {renderEmptyState()}

      {renderPaginationOrViewAll()}
    </>
  );
};

export default CustomTable;

CustomTable.propTypes = {
  rounded: PropTypes.any,
  id: PropTypes.any,
  tableData: PropTypes.array,
  headers: PropTypes.array,
  upperHeaders: PropTypes.array,
  rowComponent: PropTypes.any,
  expandable: PropTypes.any,
  rowsPerPage: PropTypes.number,
  sort: PropTypes.object,
  totalRows: PropTypes.number,
  onPageChange: PropTypes.func,
  onSortChange: PropTypes.func,
  showAllLink: PropTypes.bool,
  borderSpacing: PropTypes.string,
  checkForTabsWithData: PropTypes.func,
  showExpandable: PropTypes.bool,
  loading: PropTypes.bool,
  errorMessage: PropTypes.string,
  pageNumber: PropTypes.number,
  timeStampMinWidth: PropTypes.bool,
  tableHeadingCenter: PropTypes.array,
  stickyColumnIndex: PropTypes.string,
  showEmptyStateText: PropTypes.bool,
  emptyStateText: PropTypes.string,
  showUpdatedEmptyData: PropTypes.bool,
  showUpdatedTable: PropTypes.bool,
  onRowClick: PropTypes.func,
  tabPadding: PropTypes.string,
  sx: PropTypes.object,
  renderVertical: PropTypes.bool,
  hideHeader: PropTypes.bool,
  resizableColumns: PropTypes.bool,
  stickyFirstColumn: PropTypes.bool,
  linkToShowAll: PropTypes.string,
  resetPage: PropTypes.string,
};
