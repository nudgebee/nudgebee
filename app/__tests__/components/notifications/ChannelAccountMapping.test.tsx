import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args: any[]) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/notification', () => ({
  __esModule: true,
  default: {
    listChannelAccountMappings: jest.fn(),
    deleteChannelAccountMapping: jest.fn(),
    updateChannelAccountMapping: jest.fn(),
    insertChannelAccountMapping: jest.fn(),
  },
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: { getCloudAccounts: jest.fn() },
}));

jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getNotificationChannelList: jest.fn(),
    getMessagingPlatform: jest.fn(),
  },
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@utils/colors');

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value || '—'}</span>,
}));

jest.mock('@ui/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }: any) => (
    <div data-testid='three-dots'>
      {(menuItems || []).map((mi: any) => (
        <button key={mi.id} data-testid={`menu-${mi.label}-${data.id}`} onClick={() => onMenuClick(mi, data)}>
          {mi.label}
        </button>
      ))}
    </div>
  ),
}));

// @ui/Select with onChange(value:string) signature — different from @ui/FilterDropdown.
jest.mock('@ui/Select', () => ({
  Select: ({ label, value, options, onChange, disabled }: any) => (
    <select data-testid={`dropdown-${label}`} value={value || ''} disabled={disabled} onChange={(e) => onChange?.(e.target.value)}>
      <option value=''>--</option>
      {(options || []).map((opt: any) => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }: any) => (
    <button data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

// Title-keyed Modal mock so tests can distinguish Add Mapping vs Delete Mapping
// (both use the same DS Modal). actionButtons stays available for the buttons.
jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, handleClose, children, actionButtons }: any) => {
    if (!open) return null;
    const key = String(title || '')
      .toLowerCase()
      .replace(/\s+/g, '-');
    return (
      <div data-testid={`modal-${key}`}>
        <div data-testid='modal-title'>{title}</div>
        <button data-testid={`modal-close-${key}`} onClick={handleClose}>
          Close
        </button>
        {children}
        {actionButtons}
      </div>
    );
  },
}));

jest.mock('@ui/ListingLayout', () => {
  const ListingLayout = ({ children, id }: any) => (
    <div data-testid='listing-layout' id={id}>
      {children}
    </div>
  );
  ListingLayout.Toolbar = ({ children, actions }: any) => (
    <div data-testid='toolbar'>
      <div data-testid='toolbar-actions'>{actions}</div>
      {children}
    </div>
  );
  ListingLayout.Body = ({ children }: any) => <div data-testid='body'>{children}</div>;
  return { __esModule: true, ListingLayout, default: ListingLayout };
});

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ id, tableData, headers, loading }: any) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='headers'>{headers.join('|')}</div>
      {(tableData || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component}
            </span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

import ChannelAccountMapping from '@components/notifications/ChannelAccountMapping';

const apiNotifications = require('@api1/notification').default;
const apiDashboard = require('@api1/home').default;
const apiAccount = require('@api1/account').default;
const { toast: snackbar } = require('@ui/Toast');

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod', cloud_provider: 'aws' },
  { id: 'acc-2', account_name: 'GCP Dev', cloud_provider: 'gcp' },
];

const sampleSlackChannels = [
  { id: 'ch-1', name: 'alerts' },
  { id: 'ch-2', name: 'general' },
];

const sampleTeams = [
  {
    id: 'team-1',
    name: 'Eng',
    channels: [
      { id: 'tch-1', name: 'eng-alerts' },
      { id: 'tch-2', name: 'eng-general' },
    ],
  },
];

const sampleMappings = [
  {
    id: 'map-1',
    cloud_account: { account_name: 'AWS Prod' },
    account_id: 'acc-1',
    channel_id: 'ch-1',
    team_id: '',
    created_at: '2026-05-15T10:00:00Z',
    user_created_by: { display_name: 'Alice' },
  },
];

describe('ChannelAccountMapping (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockHasWriteAccess.mockReturnValue(true);
    apiDashboard.getCloudAccounts.mockResolvedValue(sampleAccounts);
    apiAccount.getNotificationChannelList.mockResolvedValue({ data: { data: sampleSlackChannels } });
    apiAccount.getMessagingPlatform.mockResolvedValue({ data: [{ id: 'mp-1', team_id: 'workspace-1' }] });
    apiNotifications.listChannelAccountMappings.mockResolvedValue({ data: sampleMappings });
    apiNotifications.insertChannelAccountMapping.mockResolvedValue({});
    apiNotifications.updateChannelAccountMapping.mockResolvedValue({});
    apiNotifications.deleteChannelAccountMapping.mockResolvedValue({});
  });

  it('renders null when isConfigured is false', async () => {
    const { container } = render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={false} />);
    await act(async () => {});
    expect(container.firstChild).toBeNull();
    expect(apiNotifications.listChannelAccountMappings).not.toHaveBeenCalled();
  });

  it('loads accounts on mount even when not configured (cloud account list is for the modal)', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={false} />);
    await waitFor(() => expect(apiDashboard.getCloudAccounts).toHaveBeenCalledTimes(1));
  });

  it('loads accounts + channels + platform + mappings when configured', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);

    await waitFor(() => {
      expect(apiDashboard.getCloudAccounts).toHaveBeenCalled();
      expect(apiAccount.getNotificationChannelList).toHaveBeenCalledWith('slack');
      expect(apiAccount.getMessagingPlatform).toHaveBeenCalledWith('slack');
      expect(apiNotifications.listChannelAccountMappings).toHaveBeenCalledWith('slack');
    });
  });

  it('renders Slack headers (no Team column)', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);

    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent(/^Cloud Account\|Channel\|Created At\|Created By\|$/));
  });

  it('renders MS Teams headers (with Team column)', async () => {
    apiAccount.getNotificationChannelList.mockResolvedValue({ data: { data: sampleTeams } });
    apiNotifications.listChannelAccountMappings.mockResolvedValue({
      data: [
        {
          id: 'map-t',
          cloud_account: { account_name: 'AWS Prod' },
          account_id: 'acc-1',
          team_id: 'team-1',
          channel_id: 'tch-1',
          created_at: '2026-05-15T10:00:00Z',
          user_created_by: { display_name: 'Bob' },
        },
      ],
    });

    render(<ChannelAccountMapping provider='ms_teams' displayName='MS Teams' isConfigured={true} />);

    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent(/^Cloud Account\|Team\|Channel\|Created At\|Created By\|$/));
  });

  it('resolves channel name via channelNameMap from channel list', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);

    await waitFor(() => expect(screen.getByText('alerts')).toBeInTheDocument());
    expect(screen.getByText('AWS Prod')).toBeInTheDocument();
    expect(screen.getByText('Alice')).toBeInTheDocument();
  });

  it('shows Add Mapping button only with write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(apiNotifications.listChannelAccountMappings).toHaveBeenCalled());
    expect(screen.queryByTestId('add-mapping-btn')).not.toBeInTheDocument();
  });

  it('opens Add Mapping modal in create mode', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('add-mapping-btn')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('add-mapping-btn'));

    expect(screen.getByTestId('modal-add-mapping')).toBeInTheDocument();
    expect(screen.getByTestId('dropdown-Cloud Account')).toBeInTheDocument();
    expect(screen.getByTestId('dropdown-Channel')).toBeInTheDocument();
    expect(screen.queryByTestId('dropdown-Team')).not.toBeInTheDocument();
  });

  it('opens Add Mapping modal with Team dropdown for ms_teams', async () => {
    apiAccount.getNotificationChannelList.mockResolvedValue({ data: { data: sampleTeams } });
    render(<ChannelAccountMapping provider='ms_teams' displayName='MS Teams' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('add-mapping-btn')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('add-mapping-btn'));

    expect(screen.getByTestId('dropdown-Team')).toBeInTheDocument();
    expect(screen.getByTestId('dropdown-Channel')).toBeDisabled();
  });

  it('Save button disabled when account+channel empty', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('add-mapping-btn')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('add-mapping-btn'));
    expect(screen.getByTestId('save-btn')).toBeDisabled();
  });

  it('saves a new Slack mapping with platform.team_id', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('add-mapping-btn')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('add-mapping-btn'));

    fireEvent.change(screen.getByTestId('dropdown-Cloud Account'), { target: { value: 'acc-1' } });
    fireEvent.change(screen.getByTestId('dropdown-Channel'), { target: { value: 'ch-1' } });

    fireEvent.click(screen.getByTestId('save-btn'));

    await waitFor(() =>
      expect(apiNotifications.insertChannelAccountMapping).toHaveBeenCalledWith({
        ac_id: 'acc-1',
        platform: 'slack',
        team_id: 'workspace-1',
        channel_id: 'ch-1',
      })
    );
    expect(snackbar.success).toHaveBeenCalledWith('Mapping saved');
  });

  it('updates existing mapping via Edit menu', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('menu-Edit-map-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Edit-map-1'));
    expect(screen.getByTestId('modal-edit-mapping')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('save-btn'));

    await waitFor(() =>
      expect(apiNotifications.updateChannelAccountMapping).toHaveBeenCalledWith({
        id: 'map-1',
        account_id: 'acc-1',
        team_id: 'workspace-1',
        channel_id: 'ch-1',
      })
    );
    expect(snackbar.success).toHaveBeenCalledWith('Mapping updated');
  });

  it('confirms delete via confirm dialog and refetches mappings', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('menu-Delete-map-1')).toBeInTheDocument());
    apiNotifications.listChannelAccountMappings.mockClear();

    fireEvent.click(screen.getByTestId('menu-Delete-map-1'));
    expect(screen.getByTestId('modal-delete-mapping')).toBeInTheDocument();

    // The Delete confirmation button inside the modal — no id, just children='Delete'
    fireEvent.click(screen.getByTestId('btn-Delete'));

    await waitFor(() => expect(apiNotifications.deleteChannelAccountMapping).toHaveBeenCalledWith('map-1'));
    await waitFor(() => expect(apiNotifications.listChannelAccountMappings).toHaveBeenCalled());
    expect(snackbar.success).toHaveBeenCalledWith('Mapping deleted');
  });

  it('cancels delete dialog without API call', async () => {
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(screen.getByTestId('menu-Delete-map-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Delete-map-1'));
    expect(screen.getByTestId('modal-delete-mapping')).toBeInTheDocument();

    // Cancel button inside the modal — no id, children='Cancel'
    fireEvent.click(screen.getByTestId('btn-Cancel'));

    expect(screen.queryByTestId('modal-delete-mapping')).not.toBeInTheDocument();
    expect(apiNotifications.deleteChannelAccountMapping).not.toHaveBeenCalled();
  });

  it('shows error snackbar when listChannelAccountMappings fails', async () => {
    const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    apiNotifications.listChannelAccountMappings.mockRejectedValueOnce(new Error('boom'));

    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);

    await waitFor(() => expect(snackbar.error).toHaveBeenCalledWith('Failed to load mappings'));
    consoleErrorSpy.mockRestore();
  });

  it('does not include menu items without write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<ChannelAccountMapping provider='slack' displayName='Slack' isConfigured={true} />);
    await waitFor(() => expect(apiNotifications.listChannelAccountMappings).toHaveBeenCalled());
    expect(screen.queryByTestId('menu-Edit-map-1')).not.toBeInTheDocument();
    expect(screen.queryByTestId('menu-Delete-map-1')).not.toBeInTheDocument();
  });
});
