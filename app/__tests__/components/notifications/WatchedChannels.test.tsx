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

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value || '—'}</span>,
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

jest.mock('@ui/Checkbox', () => ({
  Checkbox: ({ checked, onChange, disabled, label, description }: any) => (
    <label>
      <input
        type='checkbox'
        data-testid={`pick-${label}`}
        checked={!!checked}
        disabled={!!disabled}
        onChange={(event) => onChange?.(event.target.checked)}
      />
      {label}
      {description ? <span>{description}</span> : null}
    </label>
  ),
}));

jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, children, actionButtons }: any) =>
    open ? (
      <div data-testid={`modal-${title}`}>
        {children}
        {actionButtons}
      </div>
    ) : null,
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
  default: ({ tableData, loading, emptyHeading }: any) => (
    <div data-testid='watched-table'>
      {loading ? (
        'loading'
      ) : tableData?.length ? (
        tableData.map((row: any[], i: number) => (
          <div key={i} data-testid='watched-row'>
            {row.map((cell: any, j: number) => (
              <span key={j}>{cell.component}</span>
            ))}
          </div>
        ))
      ) : (
        <div>{emptyHeading}</div>
      )}
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
    {
      id: 'C1',
      name: 'incidents',
      is_private: false,
      is_member: true,
      watched: true,
      retention_days: 30,
      watched_since: '2026-07-24T10:12:00',
      watched_by: 'Dana',
    },
    {
      id: 'C2',
      name: 'random',
      is_private: false,
      is_member: false,
      watched: false,
      retention_days: null,
      watched_since: null,
      watched_by: null,
    },
    {
      id: 'C3',
      name: 'secret',
      is_private: true,
      is_member: false,
      watched: false,
      retention_days: null,
      watched_since: null,
      watched_by: null,
    },
    {
      id: 'C4',
      name: 'deploys',
      is_private: false,
      is_member: true,
      watched: false,
      retention_days: null,
      watched_since: null,
      watched_by: null,
    },
  ],
  team_id: 'T1',
  partial: false,
};

describe('WatchedChannels (watched-first)', () => {
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

  it('lists only watched channels, with consent metadata', async () => {
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await waitFor(() => expect(listMock).toHaveBeenCalledWith('slack'));
    expect(await screen.findByText('#incidents')).toBeInTheDocument();
    expect(screen.getByText(/by Dana/)).toBeInTheDocument();
    expect(screen.getByText('30d')).toBeInTheDocument();
    expect(screen.queryAllByTestId('watched-row')).toHaveLength(1);
    expect(screen.queryByTestId('modal-Watch channels')).not.toBeInTheDocument();
  });

  it('shows the empty state when nothing is watched', async () => {
    listMock.mockResolvedValue({ ...CHANNELS, data: CHANNELS.data.map((c) => ({ ...c, watched: false })) });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    expect(await screen.findByText('No channels watched yet')).toBeInTheDocument();
  });

  it('opens the picker listing only unwatched channels, disabling uninvited private ones', async () => {
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');

    fireEvent.click(screen.getByTestId('watch-channels-btn'));

    expect(screen.getByTestId('modal-Watch channels')).toBeInTheDocument();
    expect(screen.getByTestId('pick-#random')).toBeInTheDocument();
    expect(screen.queryByTestId('pick-#incidents')).not.toBeInTheDocument();
    expect(screen.getByTestId('pick-#secret')).toBeDisabled();
    expect(screen.getByText('Private — invite @Nubi in Slack first')).toBeInTheDocument();
  });

  it('watches the selected channels and reloads the list', async () => {
    enableMock.mockResolvedValue({ data: { channel_id: 'C2', enabled: true } });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');

    fireEvent.click(screen.getByTestId('watch-channels-btn'));
    fireEvent.click(screen.getByTestId('pick-#random'));
    fireEvent.click(screen.getByTestId('picker-watch-btn'));

    await waitFor(() => expect(enableMock).toHaveBeenCalledWith({ platform: 'slack', channelId: 'C2', teamId: 'T1', channelName: 'random' }));
    await waitFor(() => expect(mockToast.success).toHaveBeenCalled());
    await waitFor(() => expect(listMock).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId('modal-Watch channels')).not.toBeInTheDocument();
  });

  it('keeps the picker open and reports the channel when enabling fails', async () => {
    enableMock.mockResolvedValue({ error: { message: 'Slack rejected the request: is_archived' } });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');

    fireEvent.click(screen.getByTestId('watch-channels-btn'));
    fireEvent.click(screen.getByTestId('pick-#random'));
    fireEvent.click(screen.getByTestId('picker-watch-btn'));

    await waitFor(() => expect(mockToast.error).toHaveBeenCalledWith('#random: Slack rejected the request: is_archived'));
    expect(screen.getByTestId('modal-Watch channels')).toBeInTheDocument();
    expect(listMock).toHaveBeenCalledTimes(1);
  });

  it('keeps only failed channels selected on a mixed batch', async () => {
    enableMock.mockImplementation(({ channelId }: any) =>
      Promise.resolve(channelId === 'C2' ? { data: { channel_id: 'C2', enabled: true } } : { error: { message: 'is_archived' } })
    );
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');

    fireEvent.click(screen.getByTestId('watch-channels-btn'));
    fireEvent.click(screen.getByTestId('pick-#random'));
    fireEvent.click(screen.getByTestId('pick-#deploys'));
    fireEvent.click(screen.getByTestId('picker-watch-btn'));

    await waitFor(() => expect(mockToast.error).toHaveBeenCalledWith('#deploys: is_archived'));
    await waitFor(() => expect(mockToast.success).toHaveBeenCalled());
    await waitFor(() => expect(listMock).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('modal-Watch channels')).toBeInTheDocument();
    expect(screen.getByTestId('pick-#deploys')).toBeChecked();
    expect(screen.getByTestId('pick-#random')).not.toBeChecked();
  });

  it('stops watching from the row switch', async () => {
    disableMock.mockResolvedValue({ data: { channel_id: 'C1', enabled: false } });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');

    fireEvent.click(screen.getByTestId('watch-switch'));

    await waitFor(() => expect(disableMock).toHaveBeenCalledWith({ platform: 'slack', channelId: 'C1', teamId: 'T1' }));
    await waitFor(() => expect(screen.queryByText('#incidents')).not.toBeInTheDocument());
  });

  it('shows the warning banner when the listing is partial', async () => {
    listMock.mockResolvedValue({ ...CHANNELS, partial: true });
    render(<WatchedChannels provider='slack' isConfigured={true} />);
    await screen.findByText('#incidents');
    expect(screen.getByTestId('banner-warning')).toBeInTheDocument();
  });
});
