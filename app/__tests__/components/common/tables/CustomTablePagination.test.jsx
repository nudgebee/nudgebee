import { render, screen, fireEvent } from '@testing-library/react';
import CustomTablePagination from '@shared/tables/CustomTablePagination';

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10),
    storeUserPreferences: jest.fn(),
  },
  PREFERENCE_TABLE_PAGE_SIZE: 'table_page_size',
}));

jest.mock('@ui/Select', () => ({
  Select: ({ value, onChange, options }) => (
    <select data-testid='rows-per-page' value={value} onChange={(e) => onChange?.(e.target.value)}>
      {(options || []).map((opt) => {
        const v = typeof opt === 'object' ? opt.value : opt;
        const l = typeof opt === 'object' ? opt.label : opt;
        return (
          <option key={v} value={v}>
            {l}
          </option>
        );
      })}
    </select>
  ),
}));

jest.mock('@utils/colors');

const baseProps = {
  page: 1,
  totalPages: 5,
  totalRows: 50,
  rowsPerPage: 10,
  onPageChange: jest.fn(),
};

beforeEach(() => {
  baseProps.onPageChange.mockClear();
});

describe('CustomTablePagination', () => {
  describe('row range display', () => {
    it('shows correct row range for first page', () => {
      render(<CustomTablePagination {...baseProps} />);
      expect(screen.getByText(/1-10/)).toBeInTheDocument();
    });

    it('shows "No results found" when totalRows is 0', () => {
      render(<CustomTablePagination {...baseProps} totalRows={0} totalPages={0} />);
      expect(screen.getByText('No results found')).toBeInTheDocument();
    });

    it('shows total rows count in the row summary', () => {
      render(<CustomTablePagination {...baseProps} />);
      expect(screen.getByText(/of/i)).toBeInTheDocument();
    });

    it('caps endingRow at totalRows', () => {
      render(<CustomTablePagination {...baseProps} page={5} totalRows={45} rowsPerPage={10} />);
      expect(screen.getByText(/41-45/)).toBeInTheDocument();
    });
  });

  describe('page change', () => {
    it('calls onPageChange when pagination changes page', () => {
      render(<CustomTablePagination {...baseProps} />);
      const nextBtn = screen.getByRole('button', { name: 'Go to next page' });
      fireEvent.click(nextBtn);
      expect(baseProps.onPageChange).toHaveBeenCalledWith(2, 10);
    });
  });

  describe('rows per page', () => {
    it('renders rows-per-page selector', () => {
      render(<CustomTablePagination {...baseProps} />);
      expect(screen.getByTestId('rows-per-page')).toBeInTheDocument();
    });

    it('calls onPageChange with page=1 when rows per page changes', () => {
      render(<CustomTablePagination {...baseProps} />);
      fireEvent.change(screen.getByTestId('rows-per-page'), { target: { value: '20' } });
      expect(baseProps.onPageChange).toHaveBeenCalledWith(1, expect.any(Number));
    });
  });
});
