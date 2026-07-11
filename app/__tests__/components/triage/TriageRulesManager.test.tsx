import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockRouterReplace = jest.fn();
let mockRouterQuery: Record<string, any> = {};
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: mockRouterReplace,
    query: mockRouterQuery,
    pathname: '/triage',
    asPath: '/triage',
    route: '/triage',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('@lib/router', () => ({
  applyFiltersOnRouter: jest.fn(),
}));

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args: any[]) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/triage', () => ({
  __esModule: true,
  default: {
    getTriageRules: jest.fn(),
    deleteTriageRule: jest.fn(),
    toggleSystemRuleOverride: jest.fn(),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: jest.fn(() => 10) },
}));

const mockUseK8sEventFilters = jest.fn();
jest.mock('@hooks/useKubernetesEventFilters', () => ({
  __esModule: true,
  default: (...args: any[]) => mockUseK8sEventFilters(...args),
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn(), info: jest.fn() },
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
  default: ({ value }: any) => <span data-testid='datetime'>{value}</span>,
}));

jest.mock('@shared/icons/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }: any) => <span data-testid={`cloud-${cloud_provider}`}>cloud</span>,
}));

jest.mock('@ui/Label', () => ({
  Label: ({ text, children, variant }: any) => <span data-testid={`label-${variant || 'plain'}`}>{children || text}</span>,
}));

jest.mock('@ui/Chip', () => ({
  __esModule: true,
  default: ({ children }: any) => <span data-testid='chip'>{children}</span>,
}));

// DropdownMenu item.id is `triage-action-{rule.id}-{m.id}` (rule.id can contain '-').
// Use a non-greedy regex to recover rule.id so tests query `menu-{label}-{rule.id}`.
jest.mock('@ui/DropdownMenu', () => ({
  DropdownMenu: ({ items, trigger }: any) => (
    <div data-testid='three-dots'>
      {trigger}
      {(items || []).map((it: any) => {
        const m = String(it.id || '').match(/^triage-action-(.+?)-([\w_]+)$/);
        const ruleId = m ? m[1] : it.id;
        return (
          <button key={it.id} data-testid={`menu-${it.label}-${ruleId}`} onClick={() => it.onSelect?.()} disabled={it.disabled}>
            {it.label}
          </button>
        );
      })}
    </div>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }: any) => (
    <button data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

// @ui/Modal — render confirm button using `confirmText`/`onConfirm` props (used
// for the delete-rule confirmation).
jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, handleClose, children, confirmText, onConfirm }: any) =>
    open ? (
      <div data-testid='modal'>
        <div data-testid='modal-title'>{title}</div>
        {children}
        {confirmText && onConfirm && (
          <button data-testid='modal-confirm' onClick={onConfirm}>
            {confirmText}
          </button>
        )}
        <button data-testid='modal-close' onClick={handleClose}>
          Close
        </button>
      </div>
    ) : null,
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
  default: ({ label, options = [], value, onSelect, multiple }: any) => {
    if (multiple) {
      return (
        <button data-testid={`multi-${label}`} onClick={() => onSelect?.(null, (options || []).slice(0, 1))}>
          {label}
        </button>
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

jest.mock('@shared/EmptyData', () => ({
  __esModule: true,
  default: ({ heading }: any) => <div data-testid='empty-data'>{heading}</div>,
}));

jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ id, tableData, headers, totalRows, loading, pageNumber }: any) => (
    <div data-testid='k8s-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{headers.map((h: any) => h.name).join('|')}</div>
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

jest.mock('@components/k8s/common/KubernetesTable', () => ({
  __esModule: true,
  TriageRuleEventsTable: () => <div data-testid='triage-rule-events-table' />,
}));

jest.mock('@components/triage/TriageRuleModal', () => ({
  __esModule: true,
  default: ({ open, handleClose, isCreate, onSuccess, rule }: any) =>
    open ? (
      <div data-testid='rule-modal'>
        <div data-testid='rule-modal-mode'>{isCreate ? 'create' : 'edit'}</div>
        <div data-testid='rule-modal-rule-id'>{rule?.id || ''}</div>
        <button data-testid='rule-modal-close' onClick={handleClose}>
          Close
        </button>
        <button data-testid='rule-modal-save' onClick={onSuccess}>
          Save
        </button>
      </div>
    ) : null,
}));

import TriageRulesManager from '@components/triage/TriageRulesManager';

const apiTriage = require('@api1/triage').default;
const { toast: snackbar } = require('@ui/Toast');
const { applyFiltersOnRouter } = require('@lib/router');

const sampleAccounts = [{ id: 'acc-1', account_name: 'AWS Prod', label: 'AWS Prod', value: 'acc-1', cloud_provider: 'aws' }];

const sampleRules = [
  {
    id: 'rule-1',
    name: 'Suppress Dev Alerts',
    rule_type: 'suppression',
    action: 'drop',
    action_value: null,
    priority: 10,
    enabled: true,
    match_count: 42,
    created_at: '2026-05-15T10:00:00Z',
    account_id: 'acc-1',
    is_editable: true,
    is_system_rule: false,
    is_overridden: false,
    match_namespace: 'dev',
  },
  {
    id: 'rule-2',
    name: 'Classify Critical',
    rule_type: 'classification',
    action: 'critical',
    priority: 5,
    enabled: false,
    match_count: 7,
    created_at: '2026-05-14T10:00:00Z',
    account_id: 'acc-1',
    is_editable: true,
    is_system_rule: false,
    is_overridden: false,
  },
  {
    id: 'sys-1',
    name: 'System Duplicate Filter',
    rule_type: 'suppression',
    action: 'drop',
    priority: 1,
    enabled: true,
    match_count: 100,
    created_at: '2026-05-01T10:00:00Z',
    account_id: null,
    is_editable: false,
    is_system_rule: true,
    is_overridden: false,
    match_occurrence_greater_than: 5,
  },
];

describe('TriageRulesManager (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    mockUseK8sEventFilters.mockReturnValue({ accounts: sampleAccounts });
    mockHasWriteAccess.mockReturnValue(true);
    apiTriage.getTriageRules.mockResolvedValue({ rules: sampleRules });
    apiTriage.deleteTriageRule.mockResolvedValue({ success: true });
    apiTriage.toggleSystemRuleOverride.mockResolvedValue({ success: true });
  });

  it('fetches triage rules on mount (single-account view)', async () => {
    render(<TriageRulesManager accountId='acc-1' />);

    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    const call = apiTriage.getTriageRules.mock.calls[0][0];
    expect(call).toMatchObject({
      cloud_account_id: 'acc-1',
      cloud_account_ids: undefined,
      rule_type: undefined,
      enabled: undefined,
    });
  });

  it('renders Account column in multi-account view, hidden in single', async () => {
    const { rerender } = render(<TriageRulesManager />);
    await waitFor(() => expect(screen.getByTestId('headers')).toHaveTextContent(/^Account Name\|Name\|/));

    rerender(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(screen.getByTestId('headers')).toHaveTextContent(/^Name\|Type\|/);
  });

  it('renders rules with name + status + System tag for system rules', async () => {
    render(<TriageRulesManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('Suppress Dev Alerts')).toBeInTheDocument());
    expect(screen.getByText('Classify Critical')).toBeInTheDocument();
    expect(screen.getByText('System Duplicate Filter')).toBeInTheDocument();
    // System tag now uses ds/Chip (tone='info'), not Label. Just look for the text.
    expect(screen.getAllByText('System').length).toBeGreaterThan(0);
    expect(screen.getAllByTestId('label-green').length).toBeGreaterThan(0);
  });

  it('refetches with rule_type when type dropdown changes', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    apiTriage.getTriageRules.mockClear();

    fireEvent.change(screen.getByTestId('filter-Rule Type'), { target: { value: 'suppression' } });

    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(apiTriage.getTriageRules.mock.calls[0][0].rule_type).toBe('suppression');
  });

  it('refetches with enabled=true on Status=Enabled', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    apiTriage.getTriageRules.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Enabled' } });

    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(apiTriage.getTriageRules.mock.calls[0][0].enabled).toBe(true);
  });

  it('refetches with enabled=false on Status=Disabled', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    apiTriage.getTriageRules.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Disabled' } });

    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(apiTriage.getTriageRules.mock.calls[0][0].enabled).toBe(false);
  });

  it('initializes account filter from router query in multi-account view', async () => {
    mockRouterQuery = { accountId: 'acc-1' };
    render(<TriageRulesManager />);

    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(apiTriage.getTriageRules.mock.calls[0][0].cloud_account_ids).toEqual(['acc-1']);
  });

  it('opens Create modal when Create Rule button clicked (write access)', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('create-rule-btn')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('create-rule-btn'));

    expect(screen.getByTestId('rule-modal')).toBeInTheDocument();
    expect(screen.getByTestId('rule-modal-mode')).toHaveTextContent('create');
  });

  it('hides Create Rule button when no write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(screen.queryByTestId('create-rule-btn')).not.toBeInTheDocument();
  });

  it('opens Edit modal via menu click on user rule', async () => {
    render(<TriageRulesManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('menu-Edit-rule-1')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Edit-rule-1'));

    expect(screen.getByTestId('rule-modal')).toBeInTheDocument();
    expect(screen.getByTestId('rule-modal-mode')).toHaveTextContent('edit');
    expect(screen.getByTestId('rule-modal-rule-id')).toHaveTextContent('rule-1');
  });

  it('confirm-deletes a rule and refetches', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Delete-rule-1')).toBeInTheDocument());
    apiTriage.getTriageRules.mockClear();

    fireEvent.click(screen.getByTestId('menu-Delete-rule-1'));
    expect(screen.getByTestId('modal-title')).toHaveTextContent('Suppress Dev Alerts');

    fireEvent.click(screen.getByTestId('modal-confirm'));

    await waitFor(() =>
      expect(apiTriage.deleteTriageRule).toHaveBeenCalledWith({
        cloud_account_id: 'acc-1',
        rule_id: 'rule-1',
        hard_delete: true,
      })
    );
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(snackbar.success).toHaveBeenCalledWith(expect.stringContaining('deleted'));
  });

  it('cancels delete dialog without calling API', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Delete-rule-1')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Delete-rule-1'));
    fireEvent.click(screen.getByTestId('modal-close'));

    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
    expect(apiTriage.deleteTriageRule).not.toHaveBeenCalled();
  });

  it('toggles system rule override and refetches', async () => {
    render(<TriageRulesManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('menu-Disable for this Account-sys-1')).toBeInTheDocument());
    apiTriage.getTriageRules.mockClear();

    fireEvent.click(screen.getByTestId('menu-Disable for this Account-sys-1'));

    await waitFor(() =>
      expect(apiTriage.toggleSystemRuleOverride).toHaveBeenCalledWith({
        cloud_account_id: 'acc-1',
        system_rule_id: 'sys-1',
        disabled: true,
      })
    );
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
  });

  it('disables (soft-delete) a rule via Disable menu', async () => {
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Disable-rule-1')).toBeInTheDocument());
    apiTriage.getTriageRules.mockClear();

    fireEvent.click(screen.getByTestId('menu-Disable-rule-1'));

    await waitFor(() =>
      expect(apiTriage.deleteTriageRule).toHaveBeenCalledWith({
        cloud_account_id: 'acc-1',
        rule_id: 'rule-1',
        hard_delete: false,
      })
    );
    expect(snackbar.success).toHaveBeenCalledWith(expect.stringContaining('disabled'));
  });

  it('shows empty state when no rules', async () => {
    apiTriage.getTriageRules.mockResolvedValue({ rules: [] });

    render(<TriageRulesManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('No Data Available')).toBeInTheDocument());
    expect(screen.queryByTestId('k8s-table')).not.toBeInTheDocument();
  });

  it('handles API rejection without crashing', async () => {
    const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => {});
    apiTriage.getTriageRules.mockRejectedValue(new Error('network'));

    render(<TriageRulesManager accountId='acc-1' />);

    await waitFor(() => expect(snackbar.error).toHaveBeenCalledWith('Failed to fetch triage rules'));
    errorSpy.mockRestore();
  });

  it('updates router on account multi-dropdown selection', async () => {
    render(<TriageRulesManager />);
    await waitFor(() => expect(screen.getByTestId('multi-Account')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('multi-Account'));

    await waitFor(() => expect(applyFiltersOnRouter).toHaveBeenCalledWith(expect.anything(), { accountId: 'acc-1' }));
  });
});
