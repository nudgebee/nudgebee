import { Box, IconButton, Popover, Table, TableBody, TableCell, TableContainer, TableHead, TableRow, Collapse, Typography } from '@mui/material';
import ViewColumnOutlinedIcon from '@mui/icons-material/ViewColumnOutlined';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import CustomCheckBox from '@common/CustomCheckbox';
import React, { useEffect, useState, useCallback, memo, useMemo, useRef } from 'react';
import useResizableColumns from '@hooks/useResizableColumns';
import CustomButton from '@common/NewCustomButton';
import CustomTablePagination from './CustomTablePagination';
import ExpandButton from '@components1/common/ExpandButton';
import Loader from '@common/Loader';
import { capitalize } from '@mui/material/utils';
import PropTypes from 'prop-types';
import { action } from 'src/utils/actionStyles';
import EmptyData from '@common/EmptyData';
import { DataNotAvailable, infoIcon, ThumbsUp } from '@assets';
import { colors } from 'src/utils/colors';
import CustomTabs from '@common/CustomTabsForDrilldown';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTooltip from '@common/CustomTooltip';
import apiUser from '@api1/user';

const SortIcon = ({ active, direction }) => {
  const activeColor = '#1a1a1a';
  const inactiveColor = '#C0C0C0';
  return (
    <Box component='span' sx={{ display: 'inline-flex', flexDirection: 'column', alignItems: 'center', ml: '4px', lineHeight: 0 }}>
      <svg width='8' height='6' viewBox='0 0 10 6' style={{ marginBottom: '0px' }}>
        <path
          d='M1 5L5 1L9 5'
          stroke={active && direction === 'asc' ? activeColor : inactiveColor}
          strokeWidth='1.4'
          strokeLinecap='round'
          strokeLinejoin='round'
          fill='none'
        />
      </svg>
      <svg width='8' height='6' viewBox='0 0 10 6' style={{ marginTop: '1px' }}>
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
      background: colors.background.tableHeader,
    },
    'tbody tr td:first-of-type': {
      position: 'sticky',
      left: 0,
      zIndex: 1,
      background: colors.background.white,
    },
  }),
  'thead tr th:last-of-type': {
    position: 'sticky',
    right: 0,
    zIndex: 3,
    background: colors.background.tableHeader,
  },
  'tbody tr td:last-of-type': {
    position: 'sticky',
    right: 0,
    zIndex: 1,
    background: colors.background.white,
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
    right: showExpandable ? '30px' : '0px',
    '@media (max-width: 1380px)': {
      right: showExpandable ? '50px' : '0px',
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

  const handleChangeTab = (e, value) => {
    setTab(value);
  };

  return isExpanded ? (
    <Box
      p='16px'
      sx={{
        boxShadow: '0px 4px 20px 0px #99999940',
        borderBottom: `1px solid ${colors.border.secondary}`,
        borderInline: `1px solid ${colors.border.secondary}`,
        '@media (max-width: 1350px)': {
          p: '7px',
        },
      }}
    >
      <Box mb='15'>
        <CustomTabs padding={tabPadding} options={tabOptions} value={tab} onChange={handleChangeTab} />
      </Box>
      {tabOptions.map((option, tabIndex) => (
        <TabPanel key={option.key || ''} value={tab} index={tabIndex}>
          {isExpanded && option.componentFn ? option.componentFn(option, getDrillDownQuery(row), row) : <></>}
        </TabPanel>
      ))}
    </Box>
  ) : (
    <></>
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
          borderTop: collapsedObj[itemNo] && isExpandable ? `2px solid ${colors.border.primary}` : '',
          borderRight: collapsedObj[itemNo] && isExpandable ? `3px solid ${colors.border.primary}` : '',
          borderLeft: collapsedObj[itemNo] && isExpandable ? `2px solid ${colors.border.primary}` : '',
          borderBottom: '0px',
          cursor: isExpandable || onRowClick ? 'pointer' : 'auto',
          ...(isExpandable && {
            '&:hover': {
              backgroundColor: colors.background.tertiaryLightestest,
            },
          }),
          '& td': {
            transition: 'all ease 0.2s',
            backgroundColor: isExpandable && collapsedObj[itemNo] ? colors.background.white : 'auto',
          },
          ...row.sx,
        }}
        onClick={() => {
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
          const wrapped = tooltipText ? <CustomTooltip title={tooltipText}>{truncated}</CustomTooltip> : truncated;
          return (
            <TableCell
              key={index}
              align={cellAlign}
              sx={{
                fontWeight: showUpdatedTable ? 500 : 400,
                fontSize: showUpdatedTable ? '12px' : 'inherit',
                color: showUpdatedTable && colors.text.secondary,
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
          <TableCell data-export-enabled={false} sx={{ padding: '10px 10px 10px 0px !important' }}>
            <Box display={'flex'} justifyContent={'flex-end'}>
              <ExpandButton
                sx={{ ...action.secondary }}
                expanded={collapsedObj[itemNo]}
                onClick={() => {
                  checkForTabsWithData && checkForTabsWithData(getDrillDownQuery(row));
                  handleCollapseOnRow(!expanded, itemNo);
                  setExpanded(!expanded);
                }}
              />
            </Box>
          </TableCell>
        ) : null}
      </TableRow>
      {isExpandable ? (
        <TableRow
          sx={{
            border: collapsedObj[itemNo] ? `2px solid ${colors.border.primary}` : '0px',
            borderTop: '0px',
          }}
        >
          <TableCell colSpan={15} style={{ padding: 0, border: 0, maxWidth: 0 }} data-export-enabled={false}>
            <Collapse timeout='auto' in={collapsedObj[itemNo]}>
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
  rowBackgroundColor,
  cellFontSize,
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
          mt: !showUpdatedTable ? '16px' : '10px',
          mb: '10px',
          borderRadius: rounded || '8px',
          '&::-webkit-scrollbar': {
            height: '4px',
          },
        }}
      >
        <Table
          sx={{
            width: '100%',
            tableLayout: 'fixed',
            'td, th': {
              color: colors.text.secondary,
              padding: showUpdatedTable ? '12px' : '10px 10px 10px 20px',
              '@media (max-width: 1350px)': {
                fontSize: '10px',
                padding: showUpdatedTable ? '12px 5px 12px 8px' : '10px 5px 10px 8px',
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
                  background: colors.background.tableHeader,
                  borderBottom: '0px',
                  '&:nth-of-type(1)': {
                    borderRadius: '8px 0px 0px 0px',
                  },
                  '&:nth-of-type(2)': {
                    borderRadius: '0px 8px 0px 0px',
                  },
                },
              }}
            >
              <TableCell sx={{ fontWeight: 700, width: '25%' }}>Field</TableCell>
              <TableCell sx={{ fontWeight: 700, width: '75%' }}>Value</TableCell>
            </TableRow>
          </TableHead>
          <TableBody
            sx={{
              background: colors.background.white,
              'tr:last-of-type': {
                'td:first-of-type': {
                  borderRadius: '0 0 0 8px',
                },
                'td:last-of-type': {
                  borderRadius: '0 0 8px 0',
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
                      fontWeight: 600,
                      fontSize: '14px',
                      backgroundColor: colors.background.tableHeader,
                      borderRight: `1px solid ${colors.border.secondary}`,
                      width: '25%',
                      '@media (max-width: 1350px)': {
                        fontSize: '12px',
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
                    minWidth: timeStampMinWidth ? '80px !important' : '120px !important',
                  },
                },
              },
              [`&:nth-last-of-type(${isExpandableRows ? 2 : 1})`]: {
                td: {
                  '&:nth-of-type(1)': {
                    borderRadius: '0 0 0 8px',
                  },
                  '&:nth-last-of-type(1)': {
                    borderRadius: '0 0 8px 0',
                  },
                },
              },
            },
            background: rowBackgroundColor || colors.background.white,
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

  const renderEmptyState = () => {
    if (!tableData || tableData.length > 0 || loading) {
      return null;
    }

    let content;
    if (errorMessage) {
      content = errorMessage;
    } else if (showEmptyStateText) {
      content = (
        <Typography sx={{ color: colors.text.greyDark, fontSize: '14px', fontWeight: 400, fontFamily: 'Poppins' }}>{emptyStateText}</Typography>
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
        sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', width: '100%', minHeight: showUpdatedEmptyData ? '300px' : '100px' }}
      >
        <div
          style={{
            color: colors.text.secondary,
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
      return (
        <Box display='flex' justifyContent='flex-end' width='100%' mt={'0px'} mb={'10px'} pr={'50px'}>
          <CustomButton
            text='View all'
            variant='link'
            startIcon={<VisibilityOutlinedIcon sx={{ fontSize: '12px', verticalAlign: 'middle' }} />}
            sx={{ display: 'flex', alignItems: 'center', gap: '2px' }}
            onClick={() => window.open(linkToShowAll, '_blank')}
          />
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
          mt: !showUpdatedTable ? '16px' : '10px',
          borderRadius: rounded || '',
          '&::-webkit-scrollbar': {
            height: '4px',
          },
          ...(resizeEnabled && { overflowX: 'auto' }),
        }}
      >
        <Table
          sx={{
            ...(resizeEnabled
              ? buildResizeTableSx({ totalTableWidth, isResizing, stickyFirstColumn })
              : buildLegacyTableSx({ showExpandable, stickyColumnIndex })),
            'td, th': {
              color: colors.text.secondary,
              fontSize: cellFontSize || '12px',
              letterSpacing: '-0.01em',
              padding: showUpdatedTable ? '12px' : '10px 10px 10px 20px',
              '@media (max-width: 1350px)': {
                fontSize: '10px',
                padding: showUpdatedTable ? '12px 5px 12px 8px' : '10px 5px 10px 8px',
              },
            },
            'td:last-of-type, th:last-of-type': {
              paddingLeft: showUpdatedTable ? '12px' : '10px',
              '@media (max-width: 1350px)': {
                paddingLeft: '8px',
              },
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
              {isExpandableRows ? <col style={{ width: '48px' }} /> : null}
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
                  background: colors.background.transparent,
                  th: { fontWeight: 700, lineHeight: '13px', borderBottom: '0', padding: '4px 12px' },
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
                        backgroundColor: head?.backgroundColor ?? colors.background.tertiaryLightest,
                        borderInline: `1px solid ${colors.border.vertical} `,
                        borderTop: `1px solid ${colors.border.vertical} `,
                        borderRadius: '8px 8px 0 0',
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
                th: {
                  fontWeight: 500,
                  fontFamily: 'poppins',
                  lineHeight: '17px',
                  background: colors.background.tableHeader,
                  borderBottom: '0px',
                  '&:nth-of-type(1)': {
                    borderRadius: '8px 0px 0px 8px',
                  },
                  '&:nth-last-of-type(1)': {
                    borderRadius: '0px 8px 8px 0px',
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
                        sx={{ display: 'inline-flex', alignItems: 'center', cursor: 'pointer', userSelect: 'none', mr: '4px' }}
                      >
                        {head.component ? head.component : capitalize(typeof head === 'string' ? head : head.name)}{' '}
                        <span style={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>{head.secondryText}</span>
                        <SortIcon active={sort?.name === (head?.name || head)} direction={sort?.order} />
                      </Box>
                    ) : (
                      <>
                        {head.component ? head.component : capitalize(typeof head === 'string' ? head : head.name)}{' '}
                        <span style={{ color: colors.text.secondaryDark, fontSize: '12px', fontWeight: 400 }}>{head.secondryText}</span>
                      </>
                    )}
                    {head?.info && (
                      <CustomTooltip title={head.info} placement={head?.infoPlacement}>
                        <Box component='span' sx={{ position: 'relative', top: '3px', opacity: '50%' }}>
                          <SafeIcon src={infoIcon} alt='info' width={12} height={14} />{' '}
                        </Box>
                      </CustomTooltip>
                    )}
                    {showColumnIcon && (
                      <CustomTooltip title='Select columns' ref={columnSelectorBtnRef}>
                        <IconButton
                          size='small'
                          onClick={(e) => {
                            e.stopPropagation();
                            setColumnSelectorOpen(true);
                          }}
                          data-testid='column-selector-btn'
                          sx={{
                            ml: '4px',
                            p: '2px',
                            verticalAlign: 'middle',
                            color: colors.text.secondaryDark,
                            '&:hover': { color: colors.text.primary },
                          }}
                        >
                          <ViewColumnOutlinedIcon sx={{ fontSize: '16px' }} />
                        </IconButton>
                      </CustomTooltip>
                    )}
                    {resizeEnabled && idx < filteredHeaders.length - 1 && (
                      <Box
                        onMouseDown={(e) => handleResizeStart(idx, e)}
                        sx={{
                          position: 'absolute',
                          right: 0,
                          top: 0,
                          bottom: 0,
                          width: '8px',
                          cursor: 'col-resize',
                          zIndex: 1,
                          display: 'flex',
                          justifyContent: 'center',
                          alignItems: 'center',
                          '&::after': {
                            content: '""',
                            width: '1px',
                            height: '60%',
                            backgroundColor: colors.border.secondaryLightest,
                            transition: 'background-color 0.15s, width 0.15s',
                          },
                          '&:hover::after': {
                            width: '2px',
                            backgroundColor: colors.border.primary,
                          },
                          '&:active::after': {
                            width: '2px',
                            backgroundColor: colors.border.primary,
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

          {!loading && renderTableContent(id)}
        </Table>
        {loading && <Loader style={{ height: '280px', width: '100%' }} />}
      </TableContainer>

      {showColumnSelector && (
        <Popover
          open={columnSelectorOpen}
          anchorEl={() => columnSelectorBtnRef.current}
          onClose={() => setColumnSelectorOpen(false)}
          anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
          transformOrigin={{ vertical: 'top', horizontal: 'right' }}
          slotProps={{ paper: { sx: { p: '12px', borderRadius: '8px', maxHeight: '320px', overflowY: 'auto' } } }}
        >
          <Typography sx={{ fontSize: '13px', fontWeight: 600, mb: '8px', color: colors.text.secondary }}>Show Columns</Typography>
          {headers.map((head, idx) => {
            const headerName = typeof head === 'string' ? head : head.name;
            if (!headerName) return null;
            return (
              <Box key={idx} sx={{ display: 'flex', alignItems: 'center' }}>
                <CustomCheckBox
                  checked={visibleColumnIndices.includes(idx)}
                  onChange={() => handleColumnToggle(idx)}
                  text={capitalize(headerName)}
                  checkboxStyle={{ padding: '4px' }}
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
  rowBackgroundColor: PropTypes.string,
  cellFontSize: PropTypes.string,
  resizableColumns: PropTypes.bool,
  stickyFirstColumn: PropTypes.bool,
  linkToShowAll: PropTypes.string,
  resetPage: PropTypes.string,
};
