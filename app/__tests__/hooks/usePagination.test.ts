import { renderHook, act } from '@testing-library/react';
import { usePagination } from '@hooks/usePagination';

describe('usePagination', () => {
  it('initialises with default values (page=0, rowsPerPage=10)', () => {
    const { result } = renderHook(() => usePagination());
    expect(result.current.page).toBe(0);
    expect(result.current.rowsPerPage).toBe(10);
  });

  it('accepts a custom initialRowsPerPage', () => {
    const { result } = renderHook(() => usePagination(25));
    expect(result.current.rowsPerPage).toBe(25);
  });

  it('changePage sets page to newPage - 1', () => {
    const { result } = renderHook(() => usePagination());
    act(() => result.current.changePage(3));
    expect(result.current.page).toBe(2);
  });

  it('changePage resets page to 0 and updates rowsPerPage when limit differs', () => {
    const { result } = renderHook(() => usePagination(10));
    act(() => result.current.changePage(3));
    expect(result.current.page).toBe(2);

    act(() => result.current.changePage(3, 20));
    expect(result.current.rowsPerPage).toBe(20);
    expect(result.current.page).toBe(0);
  });

  it('changePage does NOT reset page when limit equals current rowsPerPage', () => {
    const { result } = renderHook(() => usePagination(10));
    act(() => result.current.changePage(3));
    act(() => result.current.changePage(3, 10));
    expect(result.current.page).toBe(2);
    expect(result.current.rowsPerPage).toBe(10);
  });

  it('setPage directly updates page', () => {
    const { result } = renderHook(() => usePagination());
    act(() => result.current.setPage(5));
    expect(result.current.page).toBe(5);
  });

  it('setRowsPerPage directly updates rowsPerPage', () => {
    const { result } = renderHook(() => usePagination());
    act(() => result.current.setRowsPerPage(50));
    expect(result.current.rowsPerPage).toBe(50);
  });

  it('changePage to page 1 sets page to 0', () => {
    const { result } = renderHook(() => usePagination());
    act(() => result.current.changePage(1));
    expect(result.current.page).toBe(0);
  });
});
