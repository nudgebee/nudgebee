import { Box, Typography, Pagination, PaginationItem } from '@mui/material';
import ChevronLeftRoundedIcon from '@mui/icons-material/ChevronLeftRounded';
import ChevronRightRoundedIcon from '@mui/icons-material/ChevronRightRounded';
import React, { useState, useEffect } from 'react';
import apiUser, { PREFERENCE_TABLE_PAGE_SIZE } from '@api1/user';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import CustomSelectDropdown from '@components1/common/CustomSelectDropdown';

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
        p: 'var(--ds-space-4) var(--ds-space-2) var(--ds-space-2) var(--ds-space-2)',
      }}
    >
      {/* Left Section - Row Range */}
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'flex-start' }}>
        <Typography
          sx={{
            color: colors.text.secondaryDark,
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-regular)',
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
              <span style={{ fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.tertiary }}>
                {startingRow.toLocaleString()}-{endingRow.toLocaleString()}
              </span>{' '}
              of <span style={{ fontWeight: 'var(--ds-font-weight-semibold)', color: colors.text.tertiary }}>{totalRows.toLocaleString()}</span>{' '}
              results
            </>
          )}
        </Typography>
      </Box>

      {/* Center Section - Pagination */}
      <Box
        sx={{
          backgroundColor: '#f3f3f3b9',
          borderRadius: 'var(--ds-radius-xl)',
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
              color: colors.text.tertiary,
              border: 'none',
              borderRadius: '36%',
              minWidth: '32px',
              height: '32px',
              margin: '0 var(--ds-space-1)',
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
      <Box sx={{ flex: 1, display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', whiteSpace: 'nowrap', justifyContent: 'flex-end' }}>
        {onPageChange && (
          <>
            <Typography
              sx={{
                color: colors.text.secondaryDark,
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                fontFamily: 'Poppins',
              }}
            >
              Rows
            </Typography>
            <CustomSelectDropdown
              value={rowsPerPageState}
              onChange={(e) => onRowsPerPageChangeFn(e.target.value)}
              options={rowsPerPageOptions}
              minWidth='65px'
              height='34px'
              fontSize='13px'
            />
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
