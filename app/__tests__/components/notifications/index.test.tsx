import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args: any[]) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/notification', () => ({
  __esModule: true,
  default: { getNotificationRules: jest.fn() },
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: { getCloudAccounts: jest.fn() },
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { getAllK8sNamespaces: jest.fn(), getAllK8sWorkload: jest.fn() },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn(() => 10) },
}));

jest.mock('@assets', () => ({
  DeleteIconRed: '/delete.svg',
  writeIconLight: '/edit.svg',
}));

jest.mock('@utils/colors');

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {}, delete: {} },
}));

jest.mock('src/utils/common', () => ({
  safeJSONParse: (val: any) => {
    if (typeof val !== 'string') return val;
    try {
      return JSON.parse(val);
    } catch {
      return null;
    }
  },
  snakeToTitleCase: (s: string) => s,
}));

jest.mock('@shared/format/Text', () => ({
  __esModule: true,
  default: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@shared/format/Datetime', () => ({
  __esModule: true,
  default: ({ value }: any) => <span data-testid='datetime'>{value || '-'}</span>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: any) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@shared/icons/IconTextBadge', () => ({
  PlatformChannelBadge: ({ platform, channelName }: any) => <span data-testid={`badge-${platform}`}>{channelName}</span>,
}));

jest.mock('@ui/Label', () => {
  const TONE_TO_COLOR: Record<string, string> = { success: 'green', critical: 'red', warning: 'yellow', neutral: 'grey' };
  return {
    Label: ({ text, children, tone, variant }: any) => {
      const color = variant || TONE_TO_COLOR[tone] || 'plain';
      return <span data-testid={`label-${color}`}>{children || text}</span>;
    },
  };
});

// DS Button with icon prop (no children for icon-only buttons). Render icon inside
// button body so SafeIcon's testid is still queryable and .closest('button') works.
jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, icon, ['aria-label']: ariaLabel }: any) => (
    <button data-testid={id || `btn-${typeof children === 'string' ? children : ariaLabel || 'icon'}`} onClick={onClick} disabled={disabled}>
      {icon}
      {children}
    </button>
  ),
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

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect, disabled }: any) => {
    if (disabled) {
      return (
        <select data-testid={`filter-disabled-${label}`} disabled>
          <option>{label} (disabled)</option>
        </select>
      );
    }
    const currentValue = typeof value === 'object' && value !== null ? value.value : value;
    return (
      <select
        data-testid={`filter-${label}`}
        value={currentValue || ''}
        onChange={(e) => onSelect?.({ target: { value: e.target.value } }, { value: e.target.value, label: e.target.value })}
      >
        <option value=''>--</option>
        {(options || []).map((opt: any, idx: number) => {
          const v = typeof opt === 'string' ? opt : opt.value;
          const l = typeof opt === 'string' ? opt : opt.label;
          return (
            <option key={(v || '_') + '-' + idx} value={v}>
              {l}
            </option>
          );
        })}
      </select>
    );
  },
}));

jest.mock('@shared/buttons/DownloadButton', () => ({
  __esModule: true,
  default: ({ onClick }: any) => (
    <button data-testid='download-btn' onClick={onClick}>
      DL
    </button>
  ),
}));

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ id, tableData, totalRows, loading, pageNumber, onPageChange }: any) => (
    <div data-testid='custom-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      {(tableData || []).map((row: any, i: number) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell: any, j: number) => (
            <span key={j} data-testid={`cell-${i}-${j}`}>
              {cell.component || cell.text}
            </span>
          ))}
        </div>
      ))}
      <button data-testid='next-page' onClick={() => onPageChange(2, 10)}>
        Next
      </button>
    </div>
  ),
}));

jest.mock('@components/notifications/NotificationRuleModal', () => ({
  __esModule: true,
  default: ({ open, handleClose, notificationRuleObject }: any) =>
    open ? (
      <div data-testid='notification-rule-modal'>
        <span data-testid='modal-rule-id'>{notificationRuleObject?.id || '—'}</span>
        <button data-testid='close-rule-modal' onClick={handleClose}>
          Close
        </button>
      </div>
    ) : null,
}));

jest.mock('@components/notifications/DeleteNotificationRuleModal', () => ({
  __esModule: true,
  default: ({ open, handleClose, ruleData }: any) =>
    open ? (
      <div data-testid='delete-rule-modal'>
        <span data-testid='delete-rule-id'>{ruleData?.id || '—'}</span>
        <button data-testid='close-delete-modal' onClick={handleClose}>
          Close
        </button>
      </div>
    ) : null,
}));

import Notifications from '@components/notifications/index';

const apiNotifications = require('@api1/notification').default;
const apiDashboard = require('@api1/home').default;
const apiKubernetes = require('@api1/kubernetes').default;

const sampleAccounts = [
  { id: 'acc-1', account_name: 'AWS Prod' },
  { id: 'acc-2', account_name: 'GCP Dev' },
];

const sampleNamespaces = [
  { cloud_account_id: 'acc-1', namespace_name: 'default' },
  { cloud_account_id: 'acc-1', namespace_name: 'kube-system' },
  { cloud_account_id: 'acc-2', namespace_name: 'apps' },
];

const sampleWorkloads = [{ name: 'web' }, { name: 'api' }];

const sampleRules = [
  {
    id: 'rule-1',
    name: 'Slack on Critical',
    source: 'alertmanager',
    account_id: 'acc-1',
    namespace: 'default',
    workload: 'web',
    is_suppressed: false,
    created_by_display_name: 'Alice',
    created_at: '2026-05-15T10:00:00Z',
    notification_rule_mappings: JSON.stringify([{ platform: 'slack', channels: { name: '#alerts' } }]),
  },
  {
    id: 'rule-2',
    name: 'Email Suppressed',
    source: 'pagerduty',
    account_id: 'acc-2',
    is_suppressed: true,
    created_by_display_name: 'Bob',
    created_at: '2026-05-14T10:00:00Z',
    notification_rule_mappings: JSON.stringify([{ platform: 'email', channels: { emails: ['ops@example.com'] } }]),
  },
];

const mockRulesResponse = (rows = sampleRules, count?: number) => ({
  data: {
    // Source reads `notifications_list_rules.rows` + `notifications_aggregate_rules.rows[0].count`
    // (older keys `admin_get_notification_rules_v2` / `_grouping_v2` were renamed).
    notifications_list_rules: { rows },
    notifications_aggregate_rules: { rows: [{ count: count ?? rows.length }] },
  },
});

describe('Notifications (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockHasWriteAccess.mockReturnValue(true);
    apiDashboard.getCloudAccounts.mockResolvedValue(sampleAccounts);
    apiKubernetes.getAllK8sNamespaces.mockResolvedValue({ data: sampleNamespaces });
    apiKubernetes.getAllK8sWorkload.mockResolvedValue({ data: sampleWorkloads });
    apiNotifications.getNotificationRules.mockResolvedValue(mockRulesResponse());
  });

  afterEach(async () => {
    for (let i = 0; i < 5; i++) {
      await act(async () => {
        await new Promise((r) => setTimeout(r, 0));
      });
    }
  });

  it('fetches clusters + namespaces on mount, then notification rules', async () => {
    render(<Notifications />);

    await waitFor(() => {
      expect(apiDashboard.getCloudAccounts).toHaveBeenCalledTimes(1);
      expect(apiKubernetes.getAllK8sNamespaces).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => expect(apiNotifications.getNotificationRules).toHaveBeenCalled());
    const [query, limit, offset] = apiNotifications.getNotificationRules.mock.calls[0];
    expect(query).toEqual({});
    expect(limit).toBe(10);
    expect(offset).toBe(0);
  });

  it('renders notification rule rows with source + cluster + status', async () => {
    render(<Notifications />);

    await waitFor(() => expect(screen.getByText('Slack on Critical')).toBeInTheDocument());
    expect(screen.getByText('Email Suppressed')).toBeInTheDocument();
    expect(screen.getAllByText('AWS Prod').length).toBeGreaterThan(0);
    expect(screen.getAllByText('GCP Dev').length).toBeGreaterThan(0);
    // Active = tone='success' → label-green; Suppressed = tone='neutral' → label-grey
    expect(screen.getByTestId('label-green')).toHaveTextContent('Active');
    expect(screen.getByTestId('label-grey')).toHaveTextContent('Suppressed');
  });

  it('renders platform-specific channel badges', async () => {
    render(<Notifications />);

    await waitFor(() => expect(screen.getByTestId('badge-slack')).toHaveTextContent('#alerts'));
    expect(screen.getByTestId('badge-email')).toHaveTextContent('ops@example.com');
  });

  it('shows Create Rule button only with write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<Notifications />);
    await waitFor(() => expect(apiNotifications.getNotificationRules).toHaveBeenCalled());
    expect(screen.queryByTestId('notification-rule')).not.toBeInTheDocument();
  });

  it('opens NotificationRuleModal on Create Rule click', async () => {
    render(<Notifications />);
    await waitFor(() => expect(screen.getByTestId('notification-rule')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('notification-rule'));

    expect(screen.getByTestId('notification-rule-modal')).toBeInTheDocument();
    expect(screen.getByTestId('modal-rule-id')).toHaveTextContent('—');
  });

  it('opens edit modal on row edit button click with rule data', async () => {
    render(<Notifications />);

    await waitFor(() => expect(screen.getAllByTestId(/icon-edit/).length).toBeGreaterThan(0));

    fireEvent.click(screen.getAllByTestId(/icon-edit/)[0].closest('button')!);

    expect(screen.getByTestId('notification-rule-modal')).toBeInTheDocument();
    expect(screen.getByTestId('modal-rule-id')).toHaveTextContent('rule-1');
  });

  it('opens delete modal on row delete button click', async () => {
    render(<Notifications />);

    await waitFor(() => expect(screen.getByTestId('Slack on Critical-delete')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('Slack on Critical-delete'));

    expect(screen.getByTestId('delete-rule-modal')).toBeInTheDocument();
    expect(screen.getByTestId('delete-rule-id')).toHaveTextContent('rule-1');
  });

  it('refetches rules when cluster filter changes', async () => {
    render(<Notifications />);
    await waitFor(() => expect(apiNotifications.getNotificationRules).toHaveBeenCalled());
    apiNotifications.getNotificationRules.mockClear();

    fireEvent.change(screen.getByTestId('filter-Cluster'), { target: { value: 'AWS Prod' } });

    await waitFor(() => expect(apiNotifications.getNotificationRules).toHaveBeenCalled());
    expect(apiNotifications.getNotificationRules.mock.calls[0][0].accountId).toBe('acc-1');
  });

  it('disables Namespace + Application filters until cluster selected', async () => {
    render(<Notifications />);

    await waitFor(() => expect(screen.getByTestId('filter-Cluster')).toBeInTheDocument());
    expect(screen.getByTestId('filter-disabled-Namespace')).toBeInTheDocument();
    expect(screen.getByTestId('filter-disabled-Application')).toBeInTheDocument();
  });

  it('paginates and adjusts offset on next page', async () => {
    render(<Notifications />);
    await waitFor(() => expect(apiNotifications.getNotificationRules).toHaveBeenCalled());
    apiNotifications.getNotificationRules.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(apiNotifications.getNotificationRules).toHaveBeenCalled());
    const [, , offset] = apiNotifications.getNotificationRules.mock.calls[0];
    expect(offset).toBe(10);
  });

  it('shows loading state during rules fetch', async () => {
    let resolveFn: any;
    apiNotifications.getNotificationRules.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<Notifications />);

    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockRulesResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles empty rules list gracefully', async () => {
    apiNotifications.getNotificationRules.mockResolvedValue(mockRulesResponse([], 0));

    render(<Notifications />);

    await waitFor(() => expect(screen.getByTestId('total')).toHaveTextContent('0'));
    expect(screen.queryByTestId('row-0')).not.toBeInTheDocument();
  });
});
