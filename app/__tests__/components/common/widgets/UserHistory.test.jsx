import React from 'react';
import { render, screen, waitFor, act } from '@testing-library/react';
import UserHistoryButton, { UserHistory, UserHistoryPopup } from '@shared/widgets/UserHistory';

jest.mock('@utils/colors');

const mockGetHistory = jest.fn().mockResolvedValue({ data: { users_create_history: [] } });

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getHistory: (...args) => mockGetHistory(...args),
    getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10),
    getUserPreferences: jest.fn().mockReturnValue({}),
    storeUserPreferences: jest.fn(),
  },
}));

jest.mock('@ui/ListingLayout', () => {
  const Body = ({ children }) => <div data-testid='listing-body'>{children}</div>;
  const ListingLayout = ({ children, id }) => <div data-testid={id || 'listing-layout'}>{children}</div>;
  ListingLayout.Body = Body;
  ListingLayout.Toolbar = ({ children }) => <div>{children}</div>;
  ListingLayout.ToolbarSpacer = () => null;
  ListingLayout.Footer = ({ children }) => <div>{children}</div>;
  return { ListingLayout };
});

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, 'aria-label': ariaLabel }) => (
    <button data-testid='icon-button' aria-label={ariaLabel} onClick={onClick}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Modal', () => ({
  Modal: ({ children, open, title, handleClose }) =>
    open ? (
      <div data-testid='modal'>
        <div>{title}</div>
        <button data-testid='modal-close' onClick={handleClose}>
          Close
        </button>
        {children}
      </div>
    ) : null,
}));

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ headers: _headers, tableData, loading }) => (
    <div data-testid='custom-table'>
      {loading && <span data-testid='loading-spinner'>Loading...</span>}
      <div data-testid='table-rows'>{tableData?.length ?? 0} rows</div>
    </div>
  ),
}));

jest.mock('@shared/buttons/CopyButton', () => ({
  __esModule: true,
  default: ({ text }) => <button data-testid='copy-button'>{text}</button>,
}));

jest.mock('@ui/Label', () => ({
  Label: ({ text }) => <span data-testid='label'>{text}</span>,
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('src/utils/common', () => ({
  safeJSONParse: jest.fn((str) => {
    try {
      return JSON.parse(str);
    } catch {
      return null;
    }
  }),
}));

describe('UserHistory', () => {
  beforeEach(() => {
    mockGetHistory.mockResolvedValue({ data: { users_create_history: [] } });
  });

  it('renders UserHistory without crashing', async () => {
    await act(async () => {
      render(<UserHistory accountId='acc-1' module='logs' />);
    });
    expect(screen.getByTestId('userHistory')).toBeInTheDocument();
  });

  it('renders the custom table', async () => {
    await act(async () => {
      render(<UserHistory accountId='acc-1' module='logs' />);
    });
    expect(screen.getByTestId('custom-table')).toBeInTheDocument();
  });

  it('calls getHistory with correct params', async () => {
    await act(async () => {
      render(<UserHistory accountId='acc-123' module='test-module' />);
    });
    await waitFor(() => {
      expect(mockGetHistory).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc-123', module: 'test-module' }));
    });
  });

  it('renders UserHistoryButton without crashing', () => {
    render(<UserHistoryButton accountId='acc-1' module='logs' />);
    expect(screen.getByTestId('icon-button')).toBeInTheDocument();
  });

  it('opens modal when icon button is clicked', async () => {
    await act(async () => {
      render(<UserHistoryButton accountId='acc-1' module='logs' />);
    });
    act(() => {
      screen.getByTestId('icon-button').click();
    });
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });
  });

  it('renders UserHistoryPopup in closed state', () => {
    render(<UserHistoryPopup accountId='acc-1' module='logs' isOpen={false} onClose={jest.fn()} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders UserHistoryPopup in open state', async () => {
    await act(async () => {
      render(<UserHistoryPopup accountId='acc-1' module='logs' isOpen={true} onClose={jest.fn()} />);
    });
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('History')).toBeInTheDocument();
  });

  it('does not fetch when accountId is missing', async () => {
    mockGetHistory.mockClear();
    await act(async () => {
      render(<UserHistory accountId='' module='logs' />);
    });
    expect(mockGetHistory).not.toHaveBeenCalled();
  });
});
