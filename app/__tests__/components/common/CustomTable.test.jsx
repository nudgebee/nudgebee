import { render, screen, fireEvent } from '@testing-library/react';
import CustomTable from '@shared/tables/CustomTable';

// CustomTable reads a persisted page-size preference at mount; stub it so the
// test doesn't touch the real api/user module (which would hit the network).
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: () => 10 },
}));

// Headers exercising the column-selector: mandatory + optional, visible + hidden.
const buildHeaders = () => [
  { name: 'Severity', defaultVisible: true, mandatory: true },
  { name: 'Application', defaultVisible: true },
  { name: 'Event Type', defaultVisible: false },
  { name: 'Action', defaultVisible: true, mandatory: true },
];

// CustomTable renders each cell's `.text` / `.component`, so cells are objects.
const cells = (...vals) => vals.map((text) => ({ text }));
const TABLE_DATA = [cells('high', 'app-1', 'OOMKilled', 'view')];

const renderTable = (headers = buildHeaders()) => render(<CustomTable id='test-table' headers={headers} tableData={TABLE_DATA} />);

const openColumnSelector = () => fireEvent.click(screen.getByTestId('column-selector-btn'));

describe('CustomTable — mandatory columns', () => {
  it('renders the column selector when headers declare defaultVisible', () => {
    renderTable();
    expect(screen.getByTestId('column-selector-btn')).toBeInTheDocument();
  });

  it('renders mandatory column checkboxes as checked and disabled', () => {
    renderTable();
    openColumnSelector();

    const severity = screen.getByRole('checkbox', { name: /severity/i });
    const action = screen.getByRole('checkbox', { name: /action/i });
    expect(severity).toBeChecked();
    expect(severity).toBeDisabled();
    expect(action).toBeChecked();
    expect(action).toBeDisabled();
  });

  it('leaves optional columns toggleable (not disabled)', () => {
    renderTable();
    openColumnSelector();

    expect(screen.getByRole('checkbox', { name: /application/i })).toBeEnabled();
    expect(screen.getByRole('checkbox', { name: /event type/i })).toBeEnabled();
  });

  it('hides an optional column when its checkbox is toggled off', () => {
    renderTable();
    expect(screen.getByText('app-1')).toBeInTheDocument();

    openColumnSelector();
    fireEvent.click(screen.getByRole('checkbox', { name: /application/i }));

    expect(screen.queryByText('app-1')).not.toBeInTheDocument();
  });

  it('keeps a mandatory column visible even when its checkbox is clicked', () => {
    renderTable();
    expect(screen.getByText('high')).toBeInTheDocument();

    openColumnSelector();
    // Disabled inputs swallow the click, but assert the guard holds regardless.
    fireEvent.click(screen.getByRole('checkbox', { name: /severity/i }));

    expect(screen.getByText('high')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /severity/i })).toBeChecked();
  });

  it('keeps a mandatory column visible even when it is not defaultVisible', () => {
    const headers = [
      { name: 'Severity', defaultVisible: true },
      { name: 'Application', defaultVisible: false },
      // Mandatory but hidden-by-default: must still render.
      { name: 'Action', defaultVisible: false, mandatory: true },
    ];
    render(<CustomTable id='test-table-2' headers={headers} tableData={[cells('high', 'app-1', 'view')]} />);

    // Application (defaultVisible:false, not mandatory) is hidden.
    expect(screen.queryByText('app-1')).not.toBeInTheDocument();
    // Action (mandatory) is visible despite defaultVisible:false.
    expect(screen.getByText('view')).toBeInTheDocument();
  });
});
