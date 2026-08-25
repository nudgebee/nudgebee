import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import { applyFiltersOnRouter } from '@lib/router';
import BriefingFilters from '@components/troubleshoot/briefing/BriefingFilters';

let mockRouterQuery = {};
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: jest.fn(),
    query: mockRouterQuery,
    pathname: '/troubleshoot',
    asPath: '/troubleshoot',
  }),
}));

jest.mock('@lib/router', () => ({
  applyFiltersOnRouter: jest.fn(),
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getCloudAccounts: jest.fn(() => Promise.resolve([])),
  },
}));

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label }) => <div>{label}</div>,
}));

// The date pickers need a MUI adapter we don't want to boot in jsdom; the
// shortcut rail — the thing this suite is about — is plain buttons either way.
jest.mock('@mui/x-date-pickers/LocalizationProvider', () => ({
  LocalizationProvider: ({ children }) => <>{children}</>,
}));

jest.mock('@mui/x-date-pickers/AdapterDayjs', () => ({
  AdapterDayjs: class {},
}));

jest.mock('@mui/x-date-pickers/DateTimePicker', () => ({
  DateTimePicker: ({ label }) => <input aria-label={label || 'date-picker'} readOnly />,
}));

jest.mock('@assets', () => ({
  calendarViewWeek: { default: { src: 'cal.svg' } },
  MenuArrowDownIcon: 'arrow.svg',
}));

jest.mock('@utils/colors');

const HOUR_MS = 3600000;
const FIXED_NOW = 1700000000000;

const openPicker = () => fireEvent.click(screen.getByRole('button', { name: /-/ }));

beforeEach(() => {
  applyFiltersOnRouter.mockClear();
  mockRouterQuery = { start_time: String(FIXED_NOW - 24 * HOUR_MS), end_time: String(FIXED_NOW) };
});

describe('BriefingFilters range control', () => {
  it('offers an absolute range alongside every shortcut, not just a preset list', async () => {
    // act-wrapped so the accounts fetch settles before we assert.
    await act(async () => {
      render(<BriefingFilters />);
    });
    openPicker();

    await waitFor(() => expect(screen.getByText('Absolute Date Range')).toBeInTheDocument());
    expect(screen.getByLabelText('From')).toBeInTheDocument();
    expect(screen.getByLabelText('To')).toBeInTheDocument();
    // The old control stopped at 30 mins / 7 days; these are the ends of the
    // shared shortcut rail that replaced it.
    expect(screen.getByText('Last 5 Minutes')).toBeInTheDocument();
    expect(screen.getByText('Current Week')).toBeInTheDocument();
    expect(screen.getByText('Last Month')).toBeInTheDocument();
  });

  it('writes the picked window onto the URL the briefing and the events list both read', async () => {
    await act(async () => {
      render(<BriefingFilters />);
    });
    openPicker();

    await waitFor(() => expect(screen.getByText('Last 3 Hours')).toBeInTheDocument());
    fireEvent.click(screen.getByText('Last 3 Hours'));

    expect(applyFiltersOnRouter).toHaveBeenCalledTimes(1);
    const [, filters] = applyFiltersOnRouter.mock.calls[0];
    expect(Number(filters.end_time) - Number(filters.start_time)).toBe(3 * HOUR_MS);
  });

  it('is hidden when the caller only wants the account filter', async () => {
    await act(async () => {
      render(<BriefingFilters showRange={false} />);
    });
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
