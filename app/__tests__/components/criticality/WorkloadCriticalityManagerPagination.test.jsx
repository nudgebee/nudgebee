/**
 * Regression test for #34613 — "page size / row filter not working" on the
 * Service Criticality tab.
 *
 * WorkloadCriticalityManager loads the workload inventory and renders it via
 * KubernetesTable with NO onPageChange/rowsPerPage, which opts CustomTable into
 * client-side pagination. The bug was a no-op onPageChange that forced
 * server-pagination mode, leaving the "Rows" page-size selector inert.
 *
 * This test exercises the real path end-to-end: WorkloadCriticalityManager ->
 * (real) CustomTable -> (real) CustomTablePagination -> page-size <select>.
 * Only KubernetesTable's heavy, unrelated internals (charts/logs/etc.) are
 * stubbed — replaced with a faithful thin forwarder to the real CustomTable,
 * matching how the real KubernetesTable forwards these props.
 */
import { render, screen, fireEvent } from '@testing-library/react';
import WorkloadCriticalityManager from '@components/criticality/WorkloadCriticalityManager';

// KubernetesTable -> faithful forwarder to the REAL CustomTable (data -> tableData),
// so client-pagination + the real page-size selector are exercised, not mocked away.
jest.mock('@components/k8s/common/KubernetesTable', () => {
  const RealCustomTable = require('@shared/tables/CustomTable').default;
  return {
    __esModule: true,
    default: ({ data, ...rest }) => <RealCustomTable tableData={data} {...rest} />,
  };
});

// ─── WorkloadCriticalityManager leaf deps (not under test) ────────────────────
jest.mock('@shared/format/Text', () => ({ __esModule: true, default: ({ value }) => <span>{value}</span> }));
jest.mock('@ui/Chip', () => ({ __esModule: true, default: ({ children }) => <span>{children}</span> }));
jest.mock('@ui/DropdownMenu', () => ({ __esModule: true, DropdownMenu: () => null }));
jest.mock('@ui/Switch', () => ({ __esModule: true, Switch: () => null }));
jest.mock('@ui/Banner', () => ({ __esModule: true, Banner: () => null }));
jest.mock('@ui/FilterDropdown', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/SearchInput', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/k8s/common/SeverityInfographic', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/ListingLayout', () => {
  const L = ({ children }) => <div>{children}</div>;
  L.Toolbar = ({ children, actions }) => (
    <div>
      {actions}
      {children}
    </div>
  );
  L.Body = ({ children }) => <div>{children}</div>;
  return { __esModule: true, ListingLayout: L, default: L };
});
jest.mock('@lib/auth', () => ({ __esModule: true, hasWriteAccess: () => false }));
jest.mock('@ui/Toast', () => ({ __esModule: true, toast: { error: jest.fn(), success: jest.fn() } }));

const listMock = jest.fn();
jest.mock('@api1/criticality', () => ({
  __esModule: true,
  default: {
    list: (...args) => listMock(...args),
    upsert: jest.fn(),
    remove: jest.fn(),
  },
}));

// ─── CustomTable / CustomTablePagination leaf deps ────────────────────────────
jest.mock('@hooks/useResizableColumns', () => ({
  __esModule: true,
  default: () => ({ columnWidths: [], totalTableWidth: 0, isResizing: false, handleResizeStart: jest.fn() }),
}));
jest.mock('@ui/Skeleton', () => ({ __esModule: true, default: () => <div data-testid='skeleton' /> }));
jest.mock('@ui/Button', () => ({ __esModule: true, Button: ({ children }) => <button>{children}</button> }));
jest.mock('@ui/Checkbox', () => ({ __esModule: true, Checkbox: () => <input type='checkbox' /> }));
jest.mock('@ui/Tooltip', () => ({ __esModule: true, default: ({ children }) => <span>{children}</span> }));
jest.mock('@shared/EmptyData', () => ({ __esModule: true, default: ({ heading }) => <div>{heading}</div> }));
jest.mock('@shared/navigation/TabsForDrilldown', () => ({ __esModule: true, default: () => <div /> }));
jest.mock('@shared/icons/SafeIcon', () => ({ __esModule: true, default: () => <span /> }));
jest.mock('@assets', () => ({ DataNotAvailable: 'x.svg', infoIcon: 'i.svg', ThumbsUp: 't.svg' }));
jest.mock('@utils/colors');

// Real CustomTablePagination reads the default page size from user prefs (10).
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10), storeUserPreferences: jest.fn() },
  PREFERENCE_TABLE_PAGE_SIZE: 'table_page_size',
}));

// Real CustomTablePagination renders the page-size selector via @ui/Select — a
// native <select> stub matching the DS single-select onChange(value) contract.
jest.mock('@ui/Select', () => ({
  Select: ({ value, onChange, options }) => (
    <select data-testid='rows-per-page' value={value} onChange={(e) => onChange?.(e.target.value)}>
      {(options || []).map((opt) => {
        const v = typeof opt === 'object' ? opt.value : opt;
        return (
          <option key={v} value={v}>
            {v}
          </option>
        );
      })}
    </select>
  ),
}));

const makeItems = (n) =>
  Array.from({ length: n }, (_, i) => ({
    cloud_resource_id: `id-${i + 1}`,
    namespace: `ns-${i + 1}`,
    name: `Item ${i + 1}`,
    kind: 'Deployment',
    criticality: 'medium',
    source: 'default',
    confidence: 0,
    rationale: '',
    is_user_override: false,
  }));

describe('WorkloadCriticalityManager — page size', () => {
  beforeEach(() => {
    listMock.mockReset();
    listMock.mockResolvedValue({ items: makeItems(25), total: 25 });
  });

  it('re-slices the visible rows when the page size changes', async () => {
    render(<WorkloadCriticalityManager accountId='acc-1' />);

    // Default page size 10 -> first 10 rows only.
    expect(await screen.findByText('Item 1')).toBeInTheDocument();
    expect(screen.getByText('Item 10')).toBeInTheDocument();
    expect(screen.queryByText('Item 20')).not.toBeInTheDocument();

    // Change the real "Rows" selector to 20 -> rows past the first page appear.
    fireEvent.change(screen.getByTestId('rows-per-page'), { target: { value: '20' } });

    expect(screen.getByText('Item 20')).toBeInTheDocument();
  });
});
