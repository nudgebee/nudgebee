import { useState } from 'react';

interface UsePaginationReturn {
  page: number;
  rowsPerPage: number;
  changePage: (newPage: number, limit?: number) => void;
  setPage: (page: number) => void;
  setRowsPerPage: (rowsPerPage: number) => void;
}

/**
 * Custom hook for managing pagination state
 * @param initialRowsPerPage - Initial number of rows per page (default: 10)
 * @returns Object containing pagination state and handlers
 */
export function usePagination(initialRowsPerPage: number = 10): UsePaginationReturn {
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(initialRowsPerPage);

  const changePage = (newPage: number, limit?: number) => {
    if (limit && limit !== rowsPerPage) {
      setRowsPerPage(limit);
      setPage(0);
    } else {
      setPage(newPage - 1);
    }
  };

  return { page, rowsPerPage, changePage, setPage, setRowsPerPage };
}
