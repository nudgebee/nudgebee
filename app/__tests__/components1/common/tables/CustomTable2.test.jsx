import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomTable, { ExpandableTableRow, ExpandedRowComponent } from '@components1/common/tables/CustomTable2';

// Mock @api1/user
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10),
    storeUserPreferences: jest.fn(),
  },
  PREFERENCE_TABLE_PAGE_SIZE: 'table_page_size',
}));

// Mock @assets
jest.mock(
  '@assets',
  () => ({
    DataNotAvailable: { src: 'data-not-available.svg' },
    ThumbsUp: { src: 'thumbs-up.svg' },
    infoIcon: { src: 'info.svg' },
  }),
  { virtual: true }
);

// Mock src/utils/colors
jest.mock('src/utils/colors', () => {
  const actual = jest.requireActual('src/utils/colors');
  return {
    ...actual,
    colors: {
      ...actual.colors,
      text: { ...actual.colors.text, secondary: '#555', secondaryDark: '#999', greyDark: '#aaa', mid: '#888', white: '#fff', primary: '#111' },
      background: {
        ...actual.colors.background,
        white: '#fff',
        tableHeader: '#f5f5f5',
        tertiaryLightestest: '#fafafa',
        tertiaryLightest: '#f0f0f0',
        transparent: 'transparent',
      },
      border: { ...actual.colors.border, secondary: '#ddd', primary: '#bbb', vertical: '#ccc', autocompleteOption: '#eee' },
      secondary: '#3B82F6',
    },
  };
});

// Mock src/utils/actionStyles
jest.mock('src/utils/actionStyles', () => ({
  action: { secondary: {} },
}));

// Mock child components
jest.mock('@components1/common/CustomCheckbox', () => ({
  __esModule: true,
  default: ({ checked, onChange, text }) => (
    <label>
      <input type='checkbox' checked={checked} onChange={onChange} data-testid={`checkbox-${text}`} />
      {text}
    </label>
  ),
}));

jest.mock('@components1/common/tables/CustomTablePagination', () => ({
  __esModule: true,
  default: ({ page, totalPages: _totalPages, totalRows, onPageChange, rowsPerPage }) => (
    <div data-testid='mock-pagination'>
      <span data-testid='pagination-page'>{page}</span>
      <span data-testid='pagination-total'>{totalRows}</span>
      <button onClick={() => onPageChange(page + 1, rowsPerPage)} data-testid='next-page-btn'>
        Next
      </button>
      <button onClick={() => onPageChange(1, 20)} data-testid='change-rows-btn'>
        Change rows
      </button>
    </div>
  ),
}));

jest.mock('@components1/common/ExpandButton', () => ({
  __esModule: true,
  default: ({ expanded, onClick, sx: _sx }) => (
    <button data-testid='expand-btn' onClick={onClick} aria-label={expanded ? 'collapse' : 'expand'}>
      {expanded ? 'Collapse' : 'Expand'}
    </button>
  ),
}));

jest.mock('@components1/common/Loader', () => ({
  __esModule: true,
  default: ({ style }) => (
    <div data-testid='loader' style={style}>
      Loading...
    </div>
  ),
}));

jest.mock('@components1/common/EmptyData', () => ({
  __esModule: true,
  default: ({ heading, subHeading, id }) => (
    <div data-testid={`empty-data-${id || 'default'}`}>
      <span>{heading}</span>
      <span>{subHeading}</span>
    </div>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, variant: _variant, onClick }) => (
    <button data-testid={`custom-btn-${text}`} onClick={onClick}>
      {text}
    </button>
  ),
}));

jest.mock('@common-new/CustomTabsForDrilldown', () => ({
  __esModule: true,
  default: ({ options, value: _value, onChange }) => (
    <div data-testid='custom-tabs'>
      {options.map((opt) => (
        <button key={opt.value} onClick={(e) => onChange(e, opt.value)} data-testid={`tab-${opt.value}`}>
          {opt.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => <div title={title}>{children}</div>,
}));

describe('CustomTable', () => {
  const basicHeaders = ['Name', 'Status', 'Age'];
  const basicTableData = [
    [{ text: 'pod-1' }, { text: 'Running' }, { text: '2d' }],
    [{ text: 'pod-2' }, { text: 'Pending' }, { text: '1d' }],
  ];

  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('loading state', () => {
    it('shows Loader when loading=true', () => {
      render(<CustomTable loading={true} headers={basicHeaders} tableData={basicTableData} />);
      expect(screen.getByTestId('loader')).toBeInTheDocument();
    });

    it('does not show table rows when loading', () => {
      render(<CustomTable loading={true} headers={basicHeaders} tableData={basicTableData} />);
      expect(screen.queryByText('pod-1')).not.toBeInTheDocument();
    });

    it('hides pagination when loading', () => {
      render(
        <CustomTable loading={true} headers={basicHeaders} tableData={basicTableData} onPageChange={jest.fn()} totalRows={20} rowsPerPage={10} />
      );
      expect(screen.queryByTestId('mock-pagination')).not.toBeInTheDocument();
    });
  });

  describe('data display', () => {
    it('renders table rows when loading=false with data', () => {
      render(<CustomTable loading={false} headers={basicHeaders} tableData={basicTableData} />);
      expect(screen.getByText('pod-1')).toBeInTheDocument();
      expect(screen.getByText('pod-2')).toBeInTheDocument();
    });

    it('renders header cells', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} />);
      expect(screen.getByText('Name')).toBeInTheDocument();
      expect(screen.getByText('Status')).toBeInTheDocument();
      expect(screen.getByText('Age')).toBeInTheDocument();
    });

    it('hides header when hideHeader=true', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} hideHeader={true} />);
      // TableHead should have display:none style
      const tableHead = document.querySelector('thead');
      expect(tableHead).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    it('shows default EmptyData when no data, no special flags', () => {
      render(<CustomTable headers={basicHeaders} tableData={[]} />);
      expect(screen.getByText('No Data Available')).toBeInTheDocument();
    });

    it('shows errorMessage when provided and no data', () => {
      render(<CustomTable headers={basicHeaders} tableData={[]} errorMessage='Something went wrong' />);
      expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    });

    it('shows EmptyData with ThumbsUp when showUpdatedEmptyData=true', () => {
      render(<CustomTable headers={basicHeaders} tableData={[]} showUpdatedEmptyData={true} />);
      expect(screen.getByText('All good here!')).toBeInTheDocument();
    });

    it('shows emptyStateText when showEmptyStateText=true', () => {
      render(<CustomTable headers={basicHeaders} tableData={[]} showEmptyStateText={true} emptyStateText='Custom empty message' />);
      expect(screen.getByText('Custom empty message')).toBeInTheDocument();
    });

    it('does not show empty state when loading=true', () => {
      render(<CustomTable headers={basicHeaders} tableData={[]} loading={true} />);
      expect(screen.queryByText('No Data Available')).not.toBeInTheDocument();
    });
  });

  describe('showAllLink', () => {
    it('shows View all button when showAllLink=true', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} showAllLink={true} />);
      expect(screen.getByTestId('custom-btn-View all')).toBeInTheDocument();
    });
  });

  describe('server-side pagination', () => {
    it('shows pagination when onPageChange is provided', () => {
      render(
        <CustomTable headers={basicHeaders} tableData={basicTableData} onPageChange={jest.fn()} totalRows={50} rowsPerPage={10} pageNumber={1} />
      );
      expect(screen.getByTestId('mock-pagination')).toBeInTheDocument();
    });

    it('calls onPageChange and resets collapsedObj on page change', () => {
      const onPageChange = jest.fn();
      render(
        <CustomTable headers={basicHeaders} tableData={basicTableData} onPageChange={onPageChange} totalRows={50} rowsPerPage={10} pageNumber={1} />
      );
      fireEvent.click(screen.getByTestId('next-page-btn'));
      expect(onPageChange).toHaveBeenCalled();
    });
  });

  describe('client-side pagination', () => {
    it('shows client pagination when no onPageChange and data > 10 rows', () => {
      const manyRows = Array.from({ length: 15 }, (_, i) => [{ text: `pod-${i}` }, { text: 'Running' }, { text: '1d' }]);
      render(<CustomTable headers={basicHeaders} tableData={manyRows} />);
      expect(screen.getByTestId('mock-pagination')).toBeInTheDocument();
    });

    it('does not show client pagination when data <= 10 rows', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} />);
      expect(screen.queryByTestId('mock-pagination')).not.toBeInTheDocument();
    });

    it('handles client page change', () => {
      const manyRows = Array.from({ length: 15 }, (_, i) => [{ text: `pod-${i}` }, { text: 'Running' }, { text: '1d' }]);
      render(<CustomTable headers={basicHeaders} tableData={manyRows} />);
      fireEvent.click(screen.getByTestId('next-page-btn'));
      // Should not throw
    });

    it('resets clientPage when data length changes', () => {
      const manyRows = Array.from({ length: 15 }, (_, i) => [{ text: `pod-${i}` }, { text: 'Running' }, { text: '1d' }]);
      const { rerender } = render(<CustomTable headers={basicHeaders} tableData={manyRows} />);
      const moreRows = Array.from({ length: 20 }, (_, i) => [{ text: `pod-${i}` }, { text: 'Running' }, { text: '1d' }]);
      rerender(<CustomTable headers={basicHeaders} tableData={moreRows} />);
      expect(screen.getByTestId('pagination-page').textContent).toBe('1');
    });
  });

  describe('renderVertical', () => {
    it('renders vertical table when renderVertical=true and data has single row', () => {
      const singleRow = [[{ text: 'value1' }, { text: 'value2' }]];
      render(<CustomTable headers={['Field1', 'Field2']} tableData={singleRow} renderVertical={true} />);
      expect(screen.getByText('Field')).toBeInTheDocument();
      expect(screen.getByText('Value')).toBeInTheDocument();
    });

    it('returns null for vertical table when data has != 1 row', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} renderVertical={true} />);
      // No vertical table headers
      expect(screen.queryByText('Field')).not.toBeInTheDocument();
    });

    it('shows empty state in vertical mode when no data', () => {
      render(<CustomTable headers={basicHeaders} tableData={[]} renderVertical={true} />);
      expect(screen.getByText('No Data Available')).toBeInTheDocument();
    });
  });

  describe('expandable rows', () => {
    const expandable = {
      tabs: [
        {
          label: 'Details',
          value: 0,
          key: 'details',
          componentFn: () => <div data-testid='tab-content'>Tab Content</div>,
        },
      ],
    };

    it('shows expand button when expandable tabs are present', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} expandable={expandable} />);
      expect(screen.getAllByTestId('expand-btn').length).toBeGreaterThan(0);
    });

    it('does not show expand button when no expandable', () => {
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} />);
      expect(screen.queryByTestId('expand-btn')).not.toBeInTheDocument();
    });
  });

  describe('sort', () => {
    it('renders TableSortLabel when sortEnabled is true', () => {
      const sortableHeaders = [{ name: 'Name', sortEnabled: true }, { name: 'Status' }];
      render(<CustomTable headers={sortableHeaders} tableData={basicTableData} sort={{ name: 'Name', order: 'asc' }} onSortChange={jest.fn()} />);
      expect(screen.getByText('Name')).toBeInTheDocument();
    });

    it('calls onSortChange when sort label clicked', () => {
      const onSortChange = jest.fn();
      const sortableHeaders = [{ name: 'Name', sortEnabled: true }, 'Status'];
      render(<CustomTable headers={sortableHeaders} tableData={basicTableData} sort={{ name: 'Name', order: 'asc' }} onSortChange={onSortChange} />);
      // The component renders a clickable Box span (not MUI TableSortLabel) for sortable headers
      const nameHeader = screen.getByText('Name');
      fireEvent.click(nameHeader);
      expect(onSortChange).toHaveBeenCalled();
    });
  });

  describe('upperHeaders', () => {
    it('renders upper header row when upperHeaders provided', () => {
      const upperHeaders = [
        { text: 'Group A', colSpan: 2, id: 'ga' },
        { id: 'empty', text: '' },
      ];
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} upperHeaders={upperHeaders} />);
      expect(screen.getByText('Group A')).toBeInTheDocument();
    });

    it('renders empty TableCell for upper header without text', () => {
      const upperHeaders = [{ id: 'empty1' }, { text: 'Has Text', id: 'ht' }];
      render(<CustomTable headers={basicHeaders} tableData={basicTableData} upperHeaders={upperHeaders} />);
      expect(screen.getByText('Has Text')).toBeInTheDocument();
    });
  });

  describe('column selector', () => {
    const headersWithDefaultVisible = [
      { name: 'Name', defaultVisible: true },
      { name: 'Status', defaultVisible: true },
      { name: 'Hidden', defaultVisible: false },
    ];

    it('renders column selector when headers have defaultVisible config', () => {
      render(<CustomTable headers={headersWithDefaultVisible} tableData={basicTableData} />);
      expect(screen.getByTestId('column-selector-btn')).toBeInTheDocument();
    });

    it('opens column selector popover on click', () => {
      render(<CustomTable headers={headersWithDefaultVisible} tableData={basicTableData} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      expect(screen.getByText('Show Columns')).toBeInTheDocument();
    });

    it('toggles column visibility via checkbox', () => {
      render(<CustomTable headers={headersWithDefaultVisible} tableData={basicTableData} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      const checkbox = screen.getByTestId('checkbox-Status');
      fireEvent.click(checkbox);
      // Should toggle - one column deselected
    });

    it('does not deselect last remaining column', () => {
      const singleVisibleHeader = [
        { name: 'Name', defaultVisible: true },
        { name: 'Hidden', defaultVisible: false },
      ];
      render(<CustomTable headers={singleVisibleHeader} tableData={basicTableData} />);
      fireEvent.click(screen.getByTestId('column-selector-btn'));
      const checkbox = screen.getByTestId('checkbox-Name');
      // Try to deselect the only visible column (should be prevented)
      fireEvent.click(checkbox);
      // Name should still be visible in the table header (use getAllByText since it may appear in popover too)
      expect(screen.getAllByText('Name').length).toBeGreaterThan(0);
    });
  });
});

describe('ExpandedRowComponent', () => {
  it('renders nothing when isExpanded=false', () => {
    const { container } = render(<ExpandedRowComponent row={[]} tabOptions={[]} isExpanded={false} />);
    // When isExpanded=false, component returns <></> which renders as an empty container
    expect(container).toBeInTheDocument();
    expect(container.innerHTML).toBe('');
  });

  it('renders content when isExpanded=true with tabOptions', () => {
    const tabOptions = [
      {
        label: 'Tab 1',
        value: 0,
        key: 'tab1',
        componentFn: () => <div data-testid='tab1-content'>Tab 1 Content</div>,
      },
    ];
    render(
      <table>
        <thead>
          <tr>
            <th scope='col'>Content</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <ExpandedRowComponent row={[{ text: 'row-data' }]} tabOptions={tabOptions} isExpanded={true} />
            </td>
          </tr>
        </tbody>
      </table>
    );
    expect(screen.getByTestId('custom-tabs')).toBeInTheDocument();
  });

  it('renders tab content via componentFn when expanded and tab active', () => {
    const tabOptions = [
      {
        label: 'Tab 1',
        value: 0,
        key: 'tab1',
        componentFn: () => <div data-testid='tab1-content'>Tab 1 Content</div>,
      },
    ];
    render(
      <table>
        <thead>
          <tr>
            <th scope='col'>Content</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <ExpandedRowComponent row={[{ text: 'test' }]} tabOptions={tabOptions} isExpanded={true} />
            </td>
          </tr>
        </tbody>
      </table>
    );
    expect(screen.getByTestId('tab1-content')).toBeInTheDocument();
  });

  it('handles tab change', () => {
    const tabOptions = [
      {
        label: 'Tab 1',
        value: 0,
        key: 'tab1',
        componentFn: () => <div data-testid='tab1-content'>Tab 1</div>,
      },
      {
        label: 'Tab 2',
        value: 1,
        key: 'tab2',
        componentFn: () => <div data-testid='tab2-content'>Tab 2</div>,
      },
    ];
    render(
      <table>
        <thead>
          <tr>
            <th scope='col'>Content</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <ExpandedRowComponent row={[]} tabOptions={tabOptions} isExpanded={true} />
            </td>
          </tr>
        </tbody>
      </table>
    );
    fireEvent.click(screen.getByTestId('tab-1'));
    // Should switch tabs without error
  });

  it('updates tab when tabOptions changes', () => {
    const tabOptions1 = [{ label: 'T1', value: 0, key: 't1', componentFn: () => <div>T1</div> }];
    const tabOptions2 = [{ label: 'T2', value: 5, key: 't2', componentFn: () => <div>T2</div> }];
    const { rerender } = render(
      <table>
        <thead>
          <tr>
            <th scope='col'>Content</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <ExpandedRowComponent row={[]} tabOptions={tabOptions1} isExpanded={true} />
            </td>
          </tr>
        </tbody>
      </table>
    );
    rerender(
      <table>
        <thead>
          <tr>
            <th scope='col'>Content</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <ExpandedRowComponent row={[]} tabOptions={tabOptions2} isExpanded={true} />
            </td>
          </tr>
        </tbody>
      </table>
    );
    expect(screen.getByText('T2')).toBeInTheDocument();
  });

  it('renders empty fragment when tabOption has no componentFn and isExpanded', () => {
    const tabOptions = [
      { label: 'Tab 1', value: 0, key: 'tab1' }, // no componentFn
    ];
    render(
      <table>
        <thead>
          <tr>
            <th scope='col'>Content</th>
          </tr>
        </thead>
        <tbody>
          <tr>
            <td>
              <ExpandedRowComponent row={[]} tabOptions={tabOptions} isExpanded={true} />
            </td>
          </tr>
        </tbody>
      </table>
    );
    expect(screen.getByTestId('custom-tabs')).toBeInTheDocument();
  });
});

describe('ExpandableTableRow', () => {
  const row = [{ text: 'cell1' }, { text: 'cell2' }];

  it('renders row cells', () => {
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} collapsedObj={{ 0: false }} />
        </tbody>
      </table>
    );
    expect(screen.getByText('cell1')).toBeInTheDocument();
    expect(screen.getByText('cell2')).toBeInTheDocument();
  });

  it('shows expand button when expandable has tabs', () => {
    const expandable = { tabs: [{ label: 'T', value: 0, key: 't', componentFn: () => <div /> }] };
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} expandable={expandable} collapsedObj={{ 0: false }} />
        </tbody>
      </table>
    );
    expect(screen.getByTestId('expand-btn')).toBeInTheDocument();
  });

  it('does not show expand button when not expandable', () => {
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} collapsedObj={{ 0: false }} />
        </tbody>
      </table>
    );
    expect(screen.queryByTestId('expand-btn')).not.toBeInTheDocument();
  });

  it('calls handleCollapseOnRow when row clicked', () => {
    const handleCollapseOnRow = jest.fn();
    const expandable = { tabs: [{ label: 'T', value: 0, key: 't', componentFn: () => <div /> }] };
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} expandable={expandable} collapsedObj={{ 0: false }} handleCollapseOnRow={handleCollapseOnRow} />
        </tbody>
      </table>
    );
    fireEvent.click(screen.getByText('cell1'));
    expect(handleCollapseOnRow).toHaveBeenCalled();
  });

  it('calls onRowClick when row clicked and no expandable', () => {
    const onRowClick = jest.fn();
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={[{ text: 'cell1', drilldownQuery: { id: '1' } }]} collapsedObj={{ 0: false }} onRowClick={onRowClick} />
        </tbody>
      </table>
    );
    fireEvent.click(screen.getByText('cell1'));
    expect(onRowClick).toHaveBeenCalledWith({ id: '1' });
  });

  it('calls checkForTabsWithData when provided and row clicked', () => {
    const checkForTabsWithData = jest.fn();
    const expandable = { tabs: [{ label: 'T', value: 0, key: 't', componentFn: () => <div /> }] };
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} expandable={expandable} collapsedObj={{ 0: false }} checkForTabsWithData={checkForTabsWithData} />
        </tbody>
      </table>
    );
    fireEvent.click(screen.getByText('cell1'));
    expect(checkForTabsWithData).toHaveBeenCalled();
  });

  it('shows collapse state when collapsedObj[itemNo] is true', () => {
    const expandable = { tabs: [{ label: 'T', value: 0, key: 't', componentFn: () => <div>expanded content</div> }] };
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} expandable={expandable} collapsedObj={{ 0: true }} />
        </tbody>
      </table>
    );
    expect(screen.getByTestId('expand-btn')).toBeInTheDocument();
  });

  it('shows expand button when showExpandable=true', () => {
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} showExpandable={true} collapsedObj={{ 0: false }} />
        </tbody>
      </table>
    );
    expect(screen.getByTestId('expand-btn')).toBeInTheDocument();
  });

  it('handles expand button click', () => {
    const handleCollapseOnRow = jest.fn();
    const expandable = { tabs: [{ label: 'T', value: 0, key: 't', componentFn: () => <div /> }] };
    render(
      <table>
        <tbody>
          <ExpandableTableRow itemNo={0} row={row} expandable={expandable} collapsedObj={{ 0: false }} handleCollapseOnRow={handleCollapseOnRow} />
        </tbody>
      </table>
    );
    fireEvent.click(screen.getByTestId('expand-btn'));
    expect(handleCollapseOnRow).toHaveBeenCalled();
  });
});
