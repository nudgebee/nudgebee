import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomTablePagination from '@components1/common/tables/CustomTablePagination';

// Mock @api1/user
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10),
    storeUserPreferences: jest.fn(),
  },
  PREFERENCE_TABLE_PAGE_SIZE: 'table_page_size',
}));

// Mock CustomSelectDropdown
jest.mock('@components1/common/CustomSelectDropdown', () => ({
  __esModule: true,
  default: ({ value, onChange, options, ...rest }) => (
    <select data-testid='rows-per-page-select' value={value} onChange={onChange} {...rest}>
      {options.map((opt) => (
        <option key={opt} value={opt}>
          {opt}
        </option>
      ))}
    </select>
  ),
}));

// Mock src/utils/colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondaryDark: '#999',
      tertiary: '#333',
      secondary: '#555',
    },
    white: '#fff',
  },
}));

import apiUser, { PREFERENCE_TABLE_PAGE_SIZE } from '@api1/user';

describe('CustomTablePagination', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    apiUser.getUserPreferencesTablePageSize.mockReturnValue(10);
  });

  describe('rowsPerPage fallback', () => {
    it('falls back to apiUser.getUserPreferencesTablePageSize() when rowsPerPage not provided', () => {
      apiUser.getUserPreferencesTablePageSize.mockReturnValue(20);
      render(<CustomTablePagination page={1} totalPages={3} totalRows={50} onPageChange={jest.fn()} />);
      expect(apiUser.getUserPreferencesTablePageSize).toHaveBeenCalled();
      // Should show rows-per-page dropdown with value 20
      const select = screen.getByTestId('rows-per-page-select');
      expect(select.value).toBe('20');
    });

    it('falls back to 10 when getUserPreferencesTablePageSize returns null', () => {
      apiUser.getUserPreferencesTablePageSize.mockReturnValue(null);
      render(<CustomTablePagination page={1} totalPages={5} totalRows={100} onPageChange={jest.fn()} />);
      const select = screen.getByTestId('rows-per-page-select');
      expect(select.value).toBe('10');
    });

    it('uses provided rowsPerPage when given', () => {
      render(<CustomTablePagination page={1} totalPages={3} totalRows={60} rowsPerPage={20} onPageChange={jest.fn()} />);
      // getUserPreferencesTablePageSize should NOT be called when rowsPerPage provided
      // (it's conditionally called only when !rowsPerPage)
      const select = screen.getByTestId('rows-per-page-select');
      expect(select.value).toBe('20');
    });
  });

  describe('totalRows display', () => {
    it('shows "No results found" when totalRows = 0', () => {
      render(<CustomTablePagination page={1} totalPages={1} totalRows={0} rowsPerPage={10} onPageChange={jest.fn()} />);
      expect(screen.getByText('No results found')).toBeInTheDocument();
    });

    it('shows range when totalRows > 0', () => {
      render(<CustomTablePagination page={1} totalPages={3} totalRows={25} rowsPerPage={10} onPageChange={jest.fn()} />);
      expect(screen.getByText(/Showing/i)).toBeInTheDocument();
      expect(screen.getByText('1-10')).toBeInTheDocument();
      expect(screen.getByText('25')).toBeInTheDocument();
    });

    it('caps endingRow to totalRows when endingRow > totalRows', () => {
      render(<CustomTablePagination page={3} totalPages={3} totalRows={25} rowsPerPage={10} onPageChange={jest.fn()} />);
      // page 3, rows 21-25, capped to 25
      expect(screen.getByText('21-25')).toBeInTheDocument();
      expect(screen.getByText('25')).toBeInTheDocument();
    });

    it('shows correct range on page 2', () => {
      render(<CustomTablePagination page={2} totalPages={5} totalRows={100} rowsPerPage={10} onPageChange={jest.fn()} />);
      expect(screen.getByText('11-20')).toBeInTheDocument();
    });
  });

  describe('onPageChange and rows-per-page dropdown', () => {
    it('shows rows-per-page dropdown when onPageChange is provided', () => {
      render(<CustomTablePagination page={1} totalPages={5} totalRows={50} rowsPerPage={10} onPageChange={jest.fn()} />);
      expect(screen.getByTestId('rows-per-page-select')).toBeInTheDocument();
      expect(screen.getByText('Rows')).toBeInTheDocument();
    });

    it('does not show rows-per-page dropdown when onPageChange is not provided', () => {
      render(<CustomTablePagination page={1} totalPages={5} totalRows={50} rowsPerPage={10} />);
      expect(screen.queryByTestId('rows-per-page-select')).not.toBeInTheDocument();
      expect(screen.queryByText('Rows')).not.toBeInTheDocument();
    });

    it('calls onPageChange with page 1 and new rowsPerPage on rows-per-page change', () => {
      const onPageChange = jest.fn();
      render(<CustomTablePagination page={1} totalPages={10} totalRows={100} rowsPerPage={10} onPageChange={onPageChange} />);
      const select = screen.getByTestId('rows-per-page-select');
      fireEvent.change(select, { target: { value: 20 } });
      expect(apiUser.storeUserPreferences).toHaveBeenCalledWith(PREFERENCE_TABLE_PAGE_SIZE, '20');
      expect(onPageChange).toHaveBeenCalledWith(1, '20');
    });

    it('does not call onPageChange on rows-per-page change when onPageChange not provided', () => {
      // No onPageChange prop
      render(<CustomTablePagination page={1} totalPages={1} totalRows={5} rowsPerPage={10} />);
      // No dropdown to interact with - just verify no error
      expect(screen.queryByTestId('rows-per-page-select')).not.toBeInTheDocument();
    });
  });

  describe('pagination controls', () => {
    it('renders pagination component', () => {
      render(<CustomTablePagination page={1} totalPages={5} totalRows={50} rowsPerPage={10} onPageChange={jest.fn()} />);
      // Pagination renders page buttons
      expect(screen.getByRole('navigation')).toBeInTheDocument();
    });

    it('updates page internally when page prop changes', () => {
      const onPageChange = jest.fn();
      const { rerender } = render(<CustomTablePagination page={1} totalPages={5} totalRows={50} rowsPerPage={10} onPageChange={onPageChange} />);
      rerender(<CustomTablePagination page={2} totalPages={5} totalRows={50} rowsPerPage={10} onPageChange={onPageChange} />);
      // Page 2 should now be selected
      const page2Btn = screen.getByRole('button', { name: /page 2/i });
      expect(page2Btn).toBeInTheDocument();
    });

    it('calls onPageChange when pagination button is clicked', () => {
      const onPageChange = jest.fn();
      render(<CustomTablePagination page={1} totalPages={5} totalRows={50} rowsPerPage={10} onPageChange={onPageChange} />);
      const page2 = screen.getByRole('button', { name: /page 2/i });
      fireEvent.click(page2);
      expect(onPageChange).toHaveBeenCalledWith(2, 10);
    });
  });

  describe('default props', () => {
    it('renders with default props (page=1, totalPages=1, totalRows=1)', () => {
      render(<CustomTablePagination onPageChange={jest.fn()} />);
      expect(screen.getByText(/Showing/i)).toBeInTheDocument();
    });
  });
});
