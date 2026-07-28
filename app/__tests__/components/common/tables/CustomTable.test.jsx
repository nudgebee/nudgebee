import { render, screen, fireEvent } from '@testing-library/react';
import CustomTable, { buildResizeTableSx, buildLegacyTableSx } from '@shared/tables/CustomTable';

// ─── mocks ───────────────────────────────────────────────────────────────────

jest.mock('@hooks/useResizableColumns', () => ({
  __esModule: true,
  default: () => ({
    columnWidths: [],
    totalTableWidth: 0,
    isResizing: false,
    handleResizeStart: jest.fn(),
  }),
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10) },
}));

jest.mock('@ui/Skeleton', () => ({
  __esModule: true,
  default: () => <div data-testid='skeleton' />,
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, tone }) => (
    <button data-testid={`btn-${tone}`} onClick={onClick}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Checkbox', () => ({
  Checkbox: ({ checked, onChange, label }) => (
    <input type='checkbox' aria-label={label} checked={checked} onChange={onChange} data-testid='col-checkbox' />
  ),
}));

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <span data-testid='tooltip' data-title={title != null ? String(title) : ''}>
      {children}
    </span>
  ),
}));

jest.mock('@shared/EmptyData', () => ({
  __esModule: true,
  default: ({ heading, subHeading, id }) => (
    <div data-testid='empty-data' id={id}>
      <span>{heading}</span>
      {subHeading && <span>{subHeading}</span>}
    </div>
  ),
}));

jest.mock('@assets', () => ({
  DataNotAvailable: 'data-not-available.svg',
  infoIcon: 'info-icon.svg',
  ThumbsUp: 'thumbs-up.svg',
}));

jest.mock('@utils/colors');

jest.mock('@shared/navigation/TabsForDrilldown', () => ({
  __esModule: true,
  default: ({ options, onChange }) => (
    <div data-testid='custom-tabs'>
      {(options || []).map((opt, i) => (
        <button key={i} data-testid={`tab-option-${i}`} onClick={(e) => onChange(e, i)}>
          {opt.label || String(opt.value)}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid='safe-icon' aria-label={alt} />,
}));

jest.mock('@shared/tables/CustomTablePagination', () => ({
  __esModule: true,
  default: ({ onPageChange, page, rowsPerPage, totalRows }) => (
    <div data-testid='pagination' data-page={String(page)} data-total={String(totalRows)}>
      <button data-testid='next-page' onClick={() => onPageChange(page + 1, rowsPerPage)}>
        Next
      </button>
    </div>
  ),
}));

// ─── helpers ─────────────────────────────────────────────────────────────────

const HEADERS = ['Name', 'Status', 'Count'];

const makeRows = (n) => Array.from({ length: n }, (_, i) => [{ text: `Item ${i + 1}` }, { text: 'Active' }, { text: String(i + 1) }]);

// ─── tests ───────────────────────────────────────────────────────────────────

describe('CustomTable', () => {
  // ─── basic rendering ────────────────────────────────────────────────────────

  describe('basic rendering', () => {
    it('renders a table with aria-label="table"', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} />);
      expect(screen.getByRole('table', { name: 'table' })).toBeInTheDocument();
    });

    it('renders string headers', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} />);
      expect(screen.getByText('Name')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
      expect(screen.getByText('Count')).toBeInTheDocument();
    });

    it('renders object headers using .name', () => {
      const headers = [
        { name: 'Pod', width: '50%' },
        { name: 'Namespace', width: '50%' },
      ];
      render(<CustomTable headers={headers} tableData={makeRows(1)} />);
      expect(screen.getByText('Pod')).toBeInTheDocument();
      expect(screen.getByText('Namespace')).toBeInTheDocument();
    });

    it('renders header JSX when header has .component', () => {
      const headers = [{ name: 'Status', component: <span data-testid='custom-header'>Custom</span> }];
      render(<CustomTable headers={headers} tableData={[[{ text: 'val' }]]} />);
      expect(screen.getByTestId('custom-header')).toBeInTheDocument();
    });

    it('renders cell text from { text } objects', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(3)} />);
      expect(screen.getByText('Item 1')).toBeInTheDocument();
      expect(screen.getByText('Item 2')).toBeInTheDocument();
      expect(screen.getByText('Item 3')).toBeInTheDocument();
    });

    it('renders cell JSX from { component } objects', () => {
      const rows = [[{ component: <span data-testid='jsx-cell'>JSX</span> }, { text: 'B' }]];
      render(<CustomTable headers={['A', 'B']} tableData={rows} />);
      expect(screen.getByTestId('jsx-cell')).toBeInTheDocument();
    });

    it('renders info tooltip icon when header has .info', () => {
      const headers = [{ name: 'CPU', info: 'CPU usage in cores' }];
      render(<CustomTable headers={headers} tableData={[[{ text: '2' }]]} />);
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });

    it('renders secondryText in the header', () => {
      const headers = [{ name: 'CPU', secondryText: '(cores)' }];
      render(<CustomTable headers={headers} tableData={[[{ text: '2' }]]} />);
      expect(screen.getByText('(cores)')).toBeInTheDocument();
    });

    it('capitalizes lowercase string headers', () => {
      render(<CustomTable headers={['name', 'status']} tableData={makeRows(1)} />);
      expect(screen.getByText('Name')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
    });
  });

  // ─── empty state ─────────────────────────────────────────────────────────────

  describe('empty state', () => {
    it('shows default EmptyData when tableData is empty', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} />);
      expect(screen.getByTestId('empty-data')).toBeInTheDocument();
      expect(screen.getByText('No Data Available')).toBeInTheDocument();
    });

    it('shows errorMessage text instead of EmptyData when provided', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} errorMessage='Server error' />);
      expect(screen.getByText('Server error')).toBeInTheDocument();
      expect(screen.queryByTestId('empty-data')).not.toBeInTheDocument();
    });

    it('shows emptyStateText when showEmptyStateText=true', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} showEmptyStateText={true} emptyStateText='Nothing here' />);
      expect(screen.getByText('Nothing here')).toBeInTheDocument();
    });

    it('shows default "No Data Available" when showEmptyStateText=true without emptyStateText prop', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} showEmptyStateText={true} />);
      expect(screen.getByText('No Data Available')).toBeInTheDocument();
    });

    it('shows "All good here!" EmptyData when showUpdatedEmptyData=true', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} showUpdatedEmptyData={true} />);
      expect(screen.getByText('All good here!')).toBeInTheDocument();
    });

    it('does not show empty state when loading=true', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} loading={true} />);
      expect(screen.queryByTestId('empty-data')).not.toBeInTheDocument();
    });

    it('does not show empty state when data is non-empty', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} />);
      expect(screen.queryByTestId('empty-data')).not.toBeInTheDocument();
    });
  });

  // ─── loading state ────────────────────────────────────────────────────────────

  describe('loading state', () => {
    it('renders skeleton cells when loading=true', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} loading={true} rowsPerPage={2} />);
      // 2 skeleton rows × 3 headers = 6 skeletons
      expect(screen.getAllByTestId('skeleton')).toHaveLength(6);
    });

    it('does not render data rows when loading=true', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} loading={true} />);
      expect(screen.queryByText('Item 1')).not.toBeInTheDocument();
    });

    it('caps skeleton rows at 10', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} loading={true} rowsPerPage={20} />);
      // min(20, 10) = 10 rows × 3 headers = 30 skeletons
      expect(screen.getAllByTestId('skeleton')).toHaveLength(30);
    });

    it('defaults to 5 skeleton rows when rowsPerPage is not set', () => {
      render(<CustomTable headers={['A', 'B']} tableData={[]} loading={true} />);
      // 5 rows × 2 headers = 10 skeletons
      expect(screen.getAllByTestId('skeleton')).toHaveLength(10);
    });

    it('caps skeleton rows at 5 when stickyHeader=true', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} loading={true} rowsPerPage={20} stickyHeader={true} />);
      // min(20, 5) = 5 rows × 3 headers = 15 skeletons
      expect(screen.getAllByTestId('skeleton')).toHaveLength(15);
    });

    it('still caps at 10 when stickyHeader=false and rowsPerPage exceeds 10', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} loading={true} rowsPerPage={20} stickyHeader={false} />);
      // min(20, 10) = 10 rows × 3 headers = 30 skeletons
      expect(screen.getAllByTestId('skeleton')).toHaveLength(30);
    });

    it('respects rowsPerPage below the cap when stickyHeader=true', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} loading={true} rowsPerPage={3} stickyHeader={true} />);
      // min(3, 5) = 3 rows × 3 headers = 9 skeletons
      expect(screen.getAllByTestId('skeleton')).toHaveLength(9);
    });
  });

  // ─── sorting ─────────────────────────────────────────────────────────────────

  describe('sorting', () => {
    it('calls onSortChange with asc when column is not currently sorted', () => {
      const onSortChange = jest.fn();
      const headers = [{ name: 'Count', sortEnabled: true }];
      render(<CustomTable headers={headers} tableData={makeRows(1)} onSortChange={onSortChange} />);
      fireEvent.click(screen.getByText('Count'));
      expect(onSortChange).toHaveBeenCalledWith({ name: 'Count', order: 'asc' }, 0);
    });

    it('calls onSortChange with desc when column is already sorted asc', () => {
      const onSortChange = jest.fn();
      const headers = [{ name: 'Count', sortEnabled: true }];
      render(<CustomTable headers={headers} tableData={makeRows(1)} onSortChange={onSortChange} sort={{ name: 'Count', order: 'asc' }} />);
      fireEvent.click(screen.getByText('Count'));
      expect(onSortChange).toHaveBeenCalledWith({ name: 'Count', order: 'desc' }, 0);
    });

    it('calls onSortChange with asc when column sorted desc (toggle to asc)', () => {
      const onSortChange = jest.fn();
      const headers = [{ name: 'Count', sortEnabled: true }];
      render(<CustomTable headers={headers} tableData={makeRows(1)} onSortChange={onSortChange} sort={{ name: 'Count', order: 'desc' }} />);
      fireEvent.click(screen.getByText('Count'));
      expect(onSortChange).toHaveBeenCalledWith({ name: 'Count', order: 'asc' }, 0);
    });

    it('does not throw when onSortChange is not provided', () => {
      const headers = [{ name: 'Count', sortEnabled: true }];
      render(<CustomTable headers={headers} tableData={makeRows(1)} />);
      expect(() => fireEvent.click(screen.getByText('Count'))).not.toThrow();
    });
  });

  // ─── row interaction ─────────────────────────────────────────────────────────

  describe('row interaction', () => {
    it('calls onRowClick when a data cell is clicked', () => {
      const onRowClick = jest.fn();
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} onRowClick={onRowClick} />);
      fireEvent.click(screen.getByText('Item 1'));
      expect(onRowClick).toHaveBeenCalledTimes(1);
    });

    it('passes drilldownQuery to onRowClick', () => {
      const onRowClick = jest.fn();
      const rows = [[{ text: 'row-cell', drilldownQuery: { id: '42' } }, { text: 'extra' }]];
      render(<CustomTable headers={['ColA', 'ColB']} tableData={rows} onRowClick={onRowClick} />);
      fireEvent.click(screen.getByText('row-cell'));
      expect(onRowClick).toHaveBeenCalledWith({ id: '42' });
    });

    it('merges drilldownQuery from multiple cells in the same row', () => {
      const onRowClick = jest.fn();
      const rows = [
        [
          { text: 'cell-1', drilldownQuery: { ns: 'default' } },
          { text: 'cell-2', drilldownQuery: { pod: 'my-pod' } },
        ],
      ];
      render(<CustomTable headers={['ColA', 'ColB']} tableData={rows} onRowClick={onRowClick} />);
      fireEvent.click(screen.getByText('cell-1'));
      expect(onRowClick).toHaveBeenCalledWith({ ns: 'default', pod: 'my-pod' });
    });
  });

  // ─── expandable rows ──────────────────────────────────────────────────────────

  describe('expandable rows', () => {
    it('shows expand button when showExpandable=true', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} showExpandable={true} />);
      expect(screen.getByLabelText('Expand row')).toBeInTheDocument();
    });

    it('expand button has aria-expanded=false initially', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} showExpandable={true} />);
      expect(screen.getByLabelText('Expand row')).toHaveAttribute('aria-expanded', 'false');
    });

    it('clicking expand button changes label to "Collapse row"', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} showExpandable={true} />);
      fireEvent.click(screen.getByLabelText('Expand row'));
      expect(screen.getByLabelText('Collapse row')).toBeInTheDocument();
    });

    it('clicking "Collapse row" re-collapses the row', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} showExpandable={true} />);
      fireEvent.click(screen.getByLabelText('Expand row'));
      fireEvent.click(screen.getByLabelText('Collapse row'));
      expect(screen.getByLabelText('Expand row')).toBeInTheDocument();
    });

    it('shows one expand button per row', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(3)} showExpandable={true} />);
      expect(screen.getAllByLabelText('Expand row')).toHaveLength(3);
    });

    it('expanding a second row collapses the first', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} showExpandable={true} />);
      // Expand row 0
      fireEvent.click(screen.getAllByLabelText('Expand row')[0]);
      expect(screen.getByLabelText('Collapse row')).toBeInTheDocument();
      // Expand row 1 — row 0 should auto-collapse
      fireEvent.click(screen.getByLabelText('Expand row'));
      expect(screen.getAllByLabelText('Expand row')).toHaveLength(1);
      expect(screen.getByLabelText('Collapse row')).toBeInTheDocument();
    });

    it('shows expand button when expandable.tabs are provided', () => {
      const expandable = { tabs: [{ label: 'Details', value: 0, componentFn: () => <div>Detail</div> }] };
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} expandable={expandable} />);
      expect(screen.getByLabelText('Expand row')).toBeInTheDocument();
    });
  });

  // ─── pagination ───────────────────────────────────────────────────────────────

  describe('pagination', () => {
    it('renders CustomTablePagination when onPageChange is provided', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} onPageChange={jest.fn()} rowsPerPage={10} totalRows={50} />);
      expect(screen.getByTestId('pagination')).toBeInTheDocument();
    });

    it('calls onPageChange when the paginator triggers a page change', () => {
      const onPageChange = jest.fn();
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} onPageChange={onPageChange} rowsPerPage={10} totalRows={50} pageNumber={1} />);
      fireEvent.click(screen.getByTestId('next-page'));
      expect(onPageChange).toHaveBeenCalledWith(2, 10);
    });

    it('auto-paginates when data length exceeds 10 rows (AUTO_PAGINATE_THRESHOLD)', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(11)} />);
      expect(screen.getByTestId('pagination')).toBeInTheDocument();
    });

    it('does not auto-paginate when data is exactly 10 rows', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(10)} />);
      expect(screen.queryByTestId('pagination')).not.toBeInTheDocument();
    });

    it('shows only first page data when auto-paginating', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(11)} />);
      expect(screen.getByText('Item 1')).toBeInTheDocument();
      expect(screen.queryByText('Item 11')).not.toBeInTheDocument();
    });

    it('navigates to next page showing the next batch of rows', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(11)} />);
      fireEvent.click(screen.getByTestId('next-page'));
      expect(screen.getByText('Item 11')).toBeInTheDocument();
      expect(screen.queryByText('Item 1')).not.toBeInTheDocument();
    });

    it('does not show pagination when data is empty', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} />);
      expect(screen.queryByTestId('pagination')).not.toBeInTheDocument();
    });

    it('does not show pagination when loading=true', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} loading={true} onPageChange={jest.fn()} />);
      expect(screen.queryByTestId('pagination')).not.toBeInTheDocument();
    });
  });

  // ─── showAllLink ──────────────────────────────────────────────────────────────

  describe('showAllLink', () => {
    it('renders "View all" button when showAllLink=true and linkToShowAll is set', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} showAllLink={true} linkToShowAll='https://example.com' />);
      expect(screen.getByText('View all')).toBeInTheDocument();
    });

    it('does not render "View all" when linkToShowAll is empty', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} showAllLink={true} linkToShowAll='' />);
      expect(screen.queryByText('View all')).not.toBeInTheDocument();
    });

    it('opens link in new tab when "View all" is clicked', () => {
      const openSpy = jest.spyOn(window, 'open').mockImplementation(() => null);
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} showAllLink={true} linkToShowAll='https://example.com' />);
      fireEvent.click(screen.getByText('View all'));
      expect(openSpy).toHaveBeenCalledWith('https://example.com', '_blank');
      openSpy.mockRestore();
    });
  });

  // ─── hideHeader ───────────────────────────────────────────────────────────────

  describe('hideHeader', () => {
    it('still renders the table body when hideHeader=true', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} hideHeader={true} />);
      expect(screen.getByText('Item 1')).toBeInTheDocument();
    });

    it('table element is still present when hideHeader=true', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} hideHeader={true} />);
      expect(screen.getByRole('table')).toBeInTheDocument();
    });
  });

  // ─── renderVertical ────────────────────────────────────────────────────────────

  describe('renderVertical', () => {
    it('renders a vertical table when renderVertical=true and data has exactly 1 row', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} renderVertical={true} />);
      expect(screen.getByRole('table', { name: 'vertical-table' })).toBeInTheDocument();
    });

    it('renders "Field" and "Value" column headers in vertical mode', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} renderVertical={true} />);
      expect(screen.getByText('Field')).toBeInTheDocument();
      expect(screen.getByText('Value')).toBeInTheDocument();
    });

    it('renders header names as row labels in vertical mode', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} renderVertical={true} />);
      // capitalize('name') = 'Name', etc.
      expect(screen.getByText('Name')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
    });

    it('renders cell data values in vertical mode', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} renderVertical={true} />);
      expect(screen.getByText('Item 1')).toBeInTheDocument();
    });

    it('does not render a vertical table when renderVertical=false', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} renderVertical={false} />);
      expect(screen.queryByRole('table', { name: 'vertical-table' })).not.toBeInTheDocument();
    });

    it('does not render a vertical table when data has more than 1 row', () => {
      render(<CustomTable headers={HEADERS} tableData={makeRows(2)} renderVertical={true} />);
      expect(screen.queryByRole('table', { name: 'vertical-table' })).not.toBeInTheDocument();
    });

    it('shows empty state in vertical mode when tableData is empty', () => {
      render(<CustomTable headers={HEADERS} tableData={[]} renderVertical={true} />);
      expect(screen.getByTestId('empty-data')).toBeInTheDocument();
    });
  });

  // ─── upperHeaders ─────────────────────────────────────────────────────────────

  describe('upperHeaders', () => {
    it('renders upper header text when upperHeaders are provided', () => {
      const upperHeaders = [{ text: 'Group A', colSpan: 2 }, { id: 'empty' }];
      render(<CustomTable headers={['Col1', 'Col2', 'Col3']} tableData={makeRows(1)} upperHeaders={upperHeaders} />);
      expect(screen.getByText('Group A')).toBeInTheDocument();
    });

    it('table renders without errors for upperHeaders items without .text', () => {
      const upperHeaders = [{ id: 'spacer', colSpan: 3 }];
      render(<CustomTable headers={HEADERS} tableData={makeRows(1)} upperHeaders={upperHeaders} />);
      expect(screen.getByRole('table')).toBeInTheDocument();
    });
  });

  // ─── column selector ──────────────────────────────────────────────────────────

  describe('column selector', () => {
    const colHeaders = [
      { name: 'Name', defaultVisible: true },
      { name: 'Status', defaultVisible: true },
      { name: 'Count', defaultVisible: false },
    ];

    it('hides column with defaultVisible=false from the table header', () => {
      render(<CustomTable headers={colHeaders} tableData={makeRows(1)} />);
      expect(screen.queryByText('Count')).not.toBeInTheDocument();
    });

    it('shows column selector button when defaultVisible config is present', () => {
      render(<CustomTable headers={colHeaders} tableData={makeRows(1)} />);
      expect(screen.getByTestId('column-selector-btn')).toBeInTheDocument();
    });

    it('does not show column selector when upperHeaders are provided', () => {
      const upperHeaders = [{ text: 'Group', colSpan: 3 }];
      render(<CustomTable headers={colHeaders} tableData={makeRows(1)} upperHeaders={upperHeaders} />);
      expect(screen.queryByTestId('column-selector-btn')).not.toBeInTheDocument();
    });

    it('opens column selector popover with all checkboxes', () => {
      render(<CustomTable headers={colHeaders} tableData={makeRows(1)} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      expect(screen.getAllByTestId('col-checkbox')).toHaveLength(3);
    });

    it('visible columns are checked and hidden column is unchecked', () => {
      render(<CustomTable headers={colHeaders} tableData={makeRows(1)} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      const checkboxes = screen.getAllByTestId('col-checkbox');
      expect(checkboxes[0]).toBeChecked(); // Name
      expect(checkboxes[1]).toBeChecked(); // Status
      expect(checkboxes[2]).not.toBeChecked(); // Count
    });

    it('toggling a hidden column makes it appear in the header', () => {
      render(<CustomTable headers={colHeaders} tableData={makeRows(1)} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      const checkboxes = screen.getAllByTestId('col-checkbox');
      fireEvent.click(checkboxes[2]); // Toggle Count on
      expect(screen.getByText('Count')).toBeInTheDocument();
    });

    it('cannot hide the last remaining visible column', () => {
      const singleVisible = [
        { name: 'Name', defaultVisible: true },
        { name: 'Status', defaultVisible: false },
      ];
      render(<CustomTable headers={singleVisible} tableData={makeRows(1)} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      const checkboxes = screen.getAllByTestId('col-checkbox');
      fireEvent.click(checkboxes[0]); // Try to hide the only visible column
      expect(screen.getByText('Name')).toBeInTheDocument(); // Still visible
    });
  });

  // ─── resetPage ────────────────────────────────────────────────────────────────

  describe('resetPage', () => {
    it('resets to page 1 when resetPage prop value changes', () => {
      const { rerender } = render(<CustomTable headers={HEADERS} tableData={makeRows(11)} resetPage='v1' />);
      fireEvent.click(screen.getByTestId('next-page'));
      // Now on page 2, Item 1 is not shown
      expect(screen.queryByText('Item 1')).not.toBeInTheDocument();
      // Changing resetPage triggers reset to page 1
      rerender(<CustomTable headers={HEADERS} tableData={makeRows(11)} resetPage='v2' />);
      expect(screen.getByText('Item 1')).toBeInTheDocument();
    });
  });

  // ─── sticky z-index escalation ──────────────────────────────────────────────
  // Regression: sticky cells with a real z-index are each their own stacking
  // context, and every row shares the same one, so a popover overflowing past
  // its row (e.g. ThreeDotsMenu without a portal) painted *behind* the next
  // row's sticky cell instead of above it. Fixed with a `:has(.MuiModal-root
  // :not(.MuiModal-hidden))` rule that outranks siblings for as long as that
  // row's popover is actually on screen.
  //
  // Matched via the Modal's own visibility class rather than the trigger's
  // `aria-expanded`, for two reasons:
  //  1. aria-expanded flips to false the instant React state changes, but
  //     MUI keeps the Menu mounted and visually closing (exit transition,
  //     ~150ms) after that — an aria-expanded-based rule would withdraw the
  //     escalation before the popover finished animating out, flashing the
  //     bug on close. MUI only adds `.MuiModal-hidden` once the exit
  //     transition truly completes (`!open && exited`), so `:not(.MuiModal-
  //     hidden)` stays true for the popover's whole visible lifetime.
  //  2. It also naturally avoids CustomTable's own row-expand chevron
  //     (`showExpandable`), which sets aria-expanded on its IconButton for
  //     an unrelated reason — it's a plain button + Collapse, not a Modal,
  //     so it can never match `.MuiModal-root`.
  const MENU_ROW_HTML = `
    <tr id="open-row"><td><div class="MuiModal-root"><div role="menu">open menu</div></div></td></tr>
    <tr id="closing-row"><td><div class="MuiModal-root"><div role="menu">closing menu</div></div></td></tr>
    <tr id="hidden-row"><td><div class="MuiModal-root MuiModal-hidden"><div role="menu">kept-mounted, closed</div></div></td></tr>
    <tr id="chevron-row"><td><button aria-label="Expand row" aria-expanded="true">chevron</button></td></tr>
  `;

  describe('buildResizeTableSx sticky z-index escalation', () => {
    it('escalates the last-of-type sticky cell only while its row has a visible popover', () => {
      const sx = buildResizeTableSx({ totalTableWidth: 0, isResizing: false, stickyFirstColumn: false });
      expect(sx['tbody tr td:last-of-type']).toMatchObject({ position: 'sticky', zIndex: 1 });
      expect(sx['tbody tr:has(.MuiModal-root:not(.MuiModal-hidden)) td:last-of-type']).toEqual({ zIndex: 10 });
    });

    it('omits the first-of-type escalation rule when stickyFirstColumn is off', () => {
      const sx = buildResizeTableSx({ totalTableWidth: 0, isResizing: false, stickyFirstColumn: false });
      expect(sx['tbody tr:has(.MuiModal-root:not(.MuiModal-hidden)) td:first-of-type']).toBeUndefined();
    });

    it('escalates the first-of-type sticky cell too when stickyFirstColumn is on', () => {
      const sx = buildResizeTableSx({ totalTableWidth: 0, isResizing: false, stickyFirstColumn: true });
      expect(sx['tbody tr td:first-of-type']).toMatchObject({ position: 'sticky', zIndex: 1 });
      expect(sx['tbody tr:has(.MuiModal-root:not(.MuiModal-hidden)) td:first-of-type']).toEqual({ zIndex: 10 });
    });

    // Real DOM-matching proof, not just a string check: covers open, still-
    // closing (no .MuiModal-hidden yet), fully-hidden/kept-mounted, and the
    // unrelated row-expand chevron.
    it('matches an open or closing popover but not a hidden one or the row-expand chevron', () => {
      document.body.innerHTML = `<table><tbody>${MENU_ROW_HTML}</tbody></table>`;
      const [selector] = Object.keys(buildResizeTableSx({ totalTableWidth: 0, isResizing: false, stickyFirstColumn: false })).filter((key) =>
        key.includes(':has(')
      );
      const rowSelector = selector.split(' td:')[0]; // 'tbody tr:has(...)'
      expect(document.querySelector('#open-row').matches(rowSelector)).toBe(true);
      expect(document.querySelector('#closing-row').matches(rowSelector)).toBe(true);
      expect(document.querySelector('#hidden-row').matches(rowSelector)).toBe(false);
      expect(document.querySelector('#chevron-row').matches(rowSelector)).toBe(false);
    });
  });

  // Same underlying bug, a different code path: tables that don't opt into
  // `resizableColumns` (e.g. KubernetesReplicaRightSizing, KubernetesBestPractices,
  // KubernetesSpotRecommendation) go through buildLegacyTableSx instead of
  // buildResizeTableSx, so it needs the identical escalation rule.
  describe('buildLegacyTableSx sticky z-index escalation', () => {
    it('escalates the stickyColumnIndex cell only while its row has a visible popover', () => {
      const sx = buildLegacyTableSx({ showExpandable: true, stickyColumnIndex: '9' });
      expect(sx['tbody tr td:nth-of-type(9)']).toMatchObject({ position: 'sticky' });
      expect(sx['tbody tr:has(.MuiModal-root:not(.MuiModal-hidden)) td:nth-of-type(9)']).toEqual({ zIndex: 10 });
    });

    it('omits the escalation rule when stickyColumnIndex is not set', () => {
      const sx = buildLegacyTableSx({ showExpandable: true, stickyColumnIndex: '' });
      expect(Object.keys(sx).some((key) => key.includes(':has('))).toBe(false);
    });

    it('matches an open or closing popover but not a hidden one or the row-expand chevron', () => {
      document.body.innerHTML = `<table><tbody>${MENU_ROW_HTML}</tbody></table>`;
      const sx = buildLegacyTableSx({ showExpandable: true, stickyColumnIndex: '9' });
      const [selector] = Object.keys(sx).filter((key) => key.includes(':has('));
      const rowSelector = selector.split(' td:')[0];
      expect(document.querySelector('#open-row').matches(rowSelector)).toBe(true);
      expect(document.querySelector('#closing-row').matches(rowSelector)).toBe(true);
      expect(document.querySelector('#hidden-row').matches(rowSelector)).toBe(false);
      expect(document.querySelector('#chevron-row').matches(rowSelector)).toBe(false);
    });
  });
});
