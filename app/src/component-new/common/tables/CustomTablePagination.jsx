import { Box, Typography, Pagination, PaginationItem } from '@mui/material';
import ChevronLeftRoundedIcon from '@mui/icons-material/ChevronLeftRounded';
import ChevronRightRoundedIcon from '@mui/icons-material/ChevronRightRounded';
import { useState, useEffect } from 'react';
import apiUser, { PREFERENCE_TABLE_PAGE_SIZE } from '@api1/user';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { Select } from '@components1/ds/Select';

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
        gap: '16px',
        width: '100%',
        p: '20px 8px',
      }}
    >
      {/* Left Section - Row Range */}
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'flex-start' }}>
        <Typography
          sx={{
            color: colors.text.secondaryDark,
            fontSize: 'var(--ds-text-small)',
            fontWeight: 400,
            minWidth: 'fit-content',
            whiteSpace: 'nowrap',
            fontFamily: 'Poppins',
          }}
        >
          {totalRows === 0 ? (
            'No results found'
          ) : (
            <>
              Showing{' '}
              <span style={{ fontWeight: 600, color: colors.text.tertiary }}>
                {startingRow.toLocaleString()}-{endingRow.toLocaleString()}
              </span>{' '}
              of <span style={{ fontWeight: 600, color: colors.text.tertiary }}>{totalRows.toLocaleString()}</span> results
            </>
          )}
        </Typography>
      </Box>

      {/* Center Section - Pagination */}
      <Box
        sx={{
          backgroundColor: 'var(--ds-gray-100)',
          borderRadius: 'var(--ds-radius-pill)',
          padding: '6px 8px',
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
              color: colors.text.tertiary,
              border: 'none',
              borderRadius: '36%',
              minWidth: '28px',
              height: '28px',
              margin: '0 2px',
              fontSize: 'var(--ds-text-body)',
              fontWeight: 'var(--ds-font-weight-medium)',
              '&:hover': {
                backgroundColor: 'var(--ds-gray-200)',
              },
              '&.Mui-selected': {
                backgroundColor: colors.text.secondary,
                color: colors.white,
              },
            },
            '& .MuiPaginationItem-previousNext': {
              color: colors.text.tertiary,
              '&:hover': {
                backgroundColor: 'transparent',
              },
            },
            '& .MuiPaginationItem-ellipsis': {
              color: colors.text.tertiary,
              pointerEvents: 'none',
              minWidth: 'unset',
              padding: 0,
              position: 'relative',
              top: '7px',
            },
          }}
        />
      </Box>

      {/* Right Section - Show per Page */}
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', gap: '10px', whiteSpace: 'nowrap', justifyContent: 'flex-end' }}>
        {onPageChange && (
          <>
            <Typography sx={{ color: colors.text.secondaryDark, fontSize: 'var(--ds-text-small)', fontWeight: 400, fontFamily: 'Poppins' }}>
              Rows
            </Typography>
            <Box sx={{ width: '72px', paddingRight: 2, flexShrink: 0 }}>
              <Select
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
