import { Box, Typography, Pagination, PaginationItem } from '@mui/material';
import ChevronLeftRoundedIcon from '@mui/icons-material/ChevronLeftRounded';
import ChevronRightRoundedIcon from '@mui/icons-material/ChevronRightRounded';
import { useState, useEffect } from 'react';
import apiUser, { PREFERENCE_TABLE_PAGE_SIZE } from '@api1/user';
import PropTypes from 'prop-types';
import { Select } from '@components1/ds/Select';
import { ds } from '@utils/colors';

const CustomTablePagination = ({ page = 1, totalPages = 1, totalRows = 1, rowsPerPage, onPageChange }) => {
  if (!rowsPerPage) {
    rowsPerPage = apiUser.getUserPreferencesTablePageSize() ?? 10;
  }

  const [rowsPerPageState, setRowsPerPageState] = useState(rowsPerPage);
  const [pageInternal, setPageInternal] = useState(page);
  const rowsPerPageOptions = [5, 10, 20, 50, 100];

  useEffect(() => {
    setPageInternal(page);
  }, [page]);

  // Calculate rows displayed
  const startingRow = (page - 1) * rowsPerPageState + 1;
  let endingRow = page * rowsPerPageState;
  if (endingRow > totalRows) {
    endingRow = totalRows;
  }

  function onRowsPerPageChangeFn(value) {
    setRowsPerPageState(value);
    setPageInternal(1);
    apiUser.storeUserPreferences(PREFERENCE_TABLE_PAGE_SIZE, value);
    if (onPageChange) {
      onPageChange(1, value);
    }
  }

  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        gap: 'var(--ds-space-4)',
        width: '100%',
        p: 'var(--ds-space-4) var(--ds-space-2)',
      }}
    >
      {/* Left Section - Row Range */}
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'flex-start' }}>
        <Typography
          sx={{
            color: 'var(--ds-gray-500)',
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-regular)',
            minWidth: 'fit-content',
            whiteSpace: 'nowrap',
            fontFamily: 'var(--ds-font-display)',
          }}
        >
          {totalRows === 0 ? (
            'No results found'
          ) : (
            <>
              Showing{' '}
              <span style={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-600)' }}>
                {startingRow.toLocaleString()}-{endingRow.toLocaleString()}
              </span>{' '}
              of <span style={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-600)' }}>{totalRows.toLocaleString()}</span>{' '}
              results
            </>
          )}
        </Typography>
      </Box>

      {/* Center Section - Pagination */}
      <Box
        sx={{
          backgroundColor: 'var(--ds-gray-100)',
          borderRadius: 'var(--ds-radius-pill)',
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          flexShrink: 0,
        }}
      >
        <Pagination
          size='small'
          color='primary'
          shape='rounded'
          page={pageInternal}
          onChange={(_event, value) => {
            setPageInternal(value);
            onPageChange(value, rowsPerPageState);
          }}
          count={totalPages}
          renderItem={(item) => <PaginationItem slots={{ previous: ChevronLeftRoundedIcon, next: ChevronRightRoundedIcon }} {...item} />}
          sx={{
            '& .MuiPagination-ul': {
              flexWrap: 'nowrap',
            },
            '& .MuiPaginationItem-root': {
              color: 'var(--ds-gray-600)',
              border: 'none',
              borderRadius: '36%',
              minWidth: ds.space.mul(0, 14),
              height: ds.space.mul(0, 14),
              margin: '0 var(--ds-space-1)',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              '&:hover': {
                backgroundColor: 'var(--ds-gray-200)',
              },
              '&.Mui-selected': {
                backgroundColor: 'var(--ds-gray-700)',
                color: 'var(--ds-background-100)',
              },
            },
            '& .MuiPaginationItem-previousNext': {
              color: 'var(--ds-gray-600)',
              '&:hover': {
                backgroundColor: 'transparent',
              },
            },
            '& .MuiPaginationItem-ellipsis': {
              color: 'var(--ds-gray-600)',
              pointerEvents: 'none',
              minWidth: 'unset',
              padding: 0,
              position: 'relative',
              top: ds.space.mul(0, 3),
            },
          }}
        />
      </Box>

      {/* Right Section - Show per Page */}
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', whiteSpace: 'nowrap', justifyContent: 'flex-end' }}>
        {onPageChange && (
          <>
            <Typography
              sx={{
                color: 'var(--ds-gray-500)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                fontFamily: 'var(--ds-font-display)',
              }}
            >
              Rows
            </Typography>
            <Box sx={{ width: ds.space.mul(0, 36), paddingRight: ds.space[4], flexShrink: 0 }}>
              <Select
                required
                value={String(rowsPerPageState)}
                onChange={(next) => onRowsPerPageChangeFn(Number(next))}
                options={rowsPerPageOptions.map((n) => String(n))}
                size='sm'
                minWidth='0'
              />
            </Box>
          </>
        )}
      </Box>
    </Box>
  );
};

CustomTablePagination.propTypes = {
  page: PropTypes.number,
  totalPages: PropTypes.number,
  totalRows: PropTypes.number,
  rowsPerPage: PropTypes.number,
  onPageChange: PropTypes.func,
};

export default CustomTablePagination;
