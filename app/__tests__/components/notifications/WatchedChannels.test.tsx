import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockHasWriteAccess = jest.fn(() => true);
const mockHasFeatureAccessCached = jest.fn(() => true);
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args: any[]) => mockHasWriteAccess(...args),
  fetchFeatureFlagsForTenant: () => Promise.resolve(),
  hasFeatureAccessCached: (...args: any[]) => mockHasFeatureAccessCached(...args),
}));

jest.mock('@api1/notification', () => ({
  __esModule: true,
  default: {
    listWatchableChannels: jest.fn(),
    enableChannelWatch: jest.fn(),
    disableChannelWatch: jest.fn(),
  },
}));

const mockToast = { success: jest.fn(), error: jest.fn() };
jest.mock('@ui/Toast', () => ({
  toast: { success: (...a: any[]) => mockToast.success(...a), error: (...a: any[]) => mockToast.error(...a) },
}));

jest.mock('@utils/colors');

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }: any) => (
    <button data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Switch', () => ({
  Switch: ({ checked, onChange, disabled, loading }: any) => (
    <input
      type='checkbox'
      data-testid='watch-switch'
      checked={!!checked}
      disabled={disabled || loading}
      onChange={(event) => onChange?.(event, event.target.checked)}
    />
  ),
}));

jest.mock('@ui/Banner', () => ({
  Banner: ({ tone, message }: any) => <div data-testid={`banner-${tone}`}>{message}</div>,
}));

jest.mock('@ui/SearchInput', () => ({
  __esModule: true,
  default: ({ value, onChange, label }: any) => (
    <input data-testid='search-channels' placeholder={label} value={value} onChange={(e) => onChange?.(e.target.value)} />
  ),
}));

jest.mock('@ui/ListingLayout', () => {
  const Layout: any = ({ children }: any) => <div>{children}</div>;
  Layout.Toolbar = ({ children, actions }: any) => (
    <div>
      {children}
      {actions}
    </div>
  );
  Layout.Body = ({ children }: any) => <div>{children}</div>;
  return { ListingLayout: Layout };
});

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData, loading }: any) => (
    <div data-testid='watched-table'>
      {loading
        ? 'loading'
        : (tableData || []).map((row: any[], i: number) => (
            <div key={i} data-testid='watched-row'>
              {row.map((cell: any, j: number) => (
                <span key={j}>{cell.component}</span>
              ))}
            </div>
          ))}
    </div>
  ),
}));

import apiNotifications from '@api1/notification';
import WatchedChannels from '@components/notifications/WatchedChannels';

const listMock = apiNotifications.listWatchableChannels as jest.Mock;
const enableMock = apiNotifications.enableChannelWatch as jest.Mock;
const disableMock = apiNotifications.disableChannelWatch as jest.Mock;

const CHANNELS = {
  data: [
    { id: 'C1', name: 'incidents', is_private: false, is_member: true, watched: true, retention_days: 30 },
    { id: 'C2', name: 'random', is_private: false, is_member: false, watched: false, retention_days: null },
  ],
  team_id: 'T1',
  partial: false,
};

describe('WatchedChannels', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockHasWriteAccess.mockReturnValue(true);
    mockHasFeatureAccessCached.mockReturnValue(true);
    listMock.mockResolvedValue(CHANNELS);
  });

  it('renders nothing when the tenant feature flag is off', async () => {
    mockHasFeatureAccessCached.mockReturnValue(false);
    const { container } = render(<WatchedChannels provider='slack' isConfigured={true} />);
    await waitFor(() => expect(mockHasFeatureAccessCached).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
    expect(listMock).not.toHaveBeenCalled();
  });

  it('lists channels with their watch state when enabled', async () => {
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await waitFor(() => expect(listMock).toHaveBeenCalledWith('slack'));
    expect(await screen.findByText('#incidents')).toBeInTheDocument();
    expect(screen.getByText('#random')).toBeInTheDocument();
    const switches = screen.getAllByTestId('watch-switch');
    expect(switches[0]).toBeChecked();
    expect(switches[1]).not.toBeChecked();
  });

  it('enables watching through the API and reflects the new state', async () => {
    enableMock.mockResolvedValue({ data: { channel_id: 'C2', enabled: true } });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#random');

    fireEvent.click(screen.getAllByTestId('watch-switch')[1]);

    await waitFor(() => expect(enableMock).toHaveBeenCalledWith({ platform: 'slack', channelId: 'C2', teamId: 'T1', channelName: 'random' }));
    await waitFor(() => expect(mockToast.success).toHaveBeenCalled());
    expect(screen.getAllByTestId('watch-switch')[1]).toBeChecked();
  });

  it('surfaces the server error message when enabling fails', async () => {
    enableMock.mockResolvedValue({ error: { message: 'Invite @Nubi in Slack first, then try again.' } });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#random');

    fireEvent.click(screen.getAllByTestId('watch-switch')[1]);

    await waitFor(() => expect(mockToast.error).toHaveBeenCalledWith('Invite @Nubi in Slack first, then try again.'));
    expect(screen.getAllByTestId('watch-switch')[1]).not.toBeChecked();
  });

  it('disables watching through the API', async () => {
    disableMock.mockResolvedValue({ data: { channel_id: 'C1', enabled: false } });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');

    fireEvent.click(screen.getAllByTestId('watch-switch')[0]);

    await waitFor(() => expect(disableMock).toHaveBeenCalledWith({ platform: 'slack', channelId: 'C1', teamId: 'T1' }));
    expect(screen.getAllByTestId('watch-switch')[0]).not.toBeChecked();
  });

  it('shows the warning banner when the listing is partial', async () => {
    listMock.mockResolvedValue({ ...CHANNELS, partial: true });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');
    expect(screen.getByTestId('banner-warning')).toBeInTheDocument();
    expect(screen.queryByTestId('banner-info')).not.toBeInTheDocument();
  });
});
