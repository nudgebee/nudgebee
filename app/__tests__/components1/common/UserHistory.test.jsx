import React from 'react';
import { render, screen, waitFor, act } from '@testing-library/react';
import UserHistoryButton, { UserHistory, UserHistoryPopup } from '@components1/common/UserHistory';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
      disabled: '#9CA3AF',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

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

jest.mock('@components1/CustomIconButton', () => ({
  __esModule: true,
  default: ({ children, onClick, variant: _variant }) => (
    <button data-testid='icon-button' onClick={onClick}>
      {children}
    </button>
  ),
}));

jest.mock('@components1/common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, id }) => <div data-testid={id || 'box-layout'}>{children}</div>,
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
  __esModule: true,
  default: ({ headers: _headers, tableData, loading }) => (
    <div data-testid='custom-table'>
      {loading && <span data-testid='loading-spinner'>Loading...</span>}
      <div data-testid='table-rows'>{tableData?.length ?? 0} rows</div>
    </div>
  ),
}));

jest.mock('@components1/common/modal', () => ({
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

jest.mock('@components1/common/CopyableText', () => ({
  __esModule: true,
  default: ({ copyableText, onCopy }) => (
    <button data-testid='copyable-text' onClick={() => onCopy?.(copyableText)}>
      Copy
    </button>
  ),
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
    const button = screen.getByTestId('icon-button');
    act(() => {
      button.click();
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
