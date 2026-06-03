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
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

const mockUseK8sEventFilters = jest.fn();
jest.mock('@hooks/useKubernetesEventFilters', () => ({
  __esModule: true,
  default: (...args: any[]) => mockUseK8sEventFilters(...args),
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn(), info: jest.fn() },
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000', tertiary: '#999' }, background: { white: '#fff' } },
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@components1/common/CloudIcon', () => ({
  __esModule: true,
  default: ({ cloud_provider }: any) => <span data-testid={`cloud-${cloud_provider}`}>cloud</span>,
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text, variant }: any) => <span data-testid={`label-${variant || 'plain'}`}>{text}</span>,
}));

jest.mock('@components1/common/ThreeDotsMenu', () => ({
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

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick }: any) => (
    <button data-testid={`custom-btn-${text}`} onClick={onClick}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/modal/NDialog', () => ({
  __esModule: true,
  default: ({ open, dialogTitle, handleSubmit, handleClose, buttonText }: any) =>
    open ? (
      <div data-testid='ndialog'>
        <div data-testid='ndialog-title'>{dialogTitle}</div>
        <button data-testid='ndialog-submit' onClick={handleSubmit}>
          {buttonText}
        </button>
        <button data-testid='ndialog-close' onClick={handleClose}>
          Cancel
        </button>
      </div>
    ) : null,
}));

jest.mock('@components1/common/BoxLayout2', () => ({
  __esModule: true,
  default: ({ children, filterOptions = [], extraOptions = [] }: any) => (
    <div data-testid='box-layout'>
      <div data-testid='extras'>{extraOptions}</div>
      {filterOptions.map((f: any, i: number) => {
        if (f.type === 'dropdown') {
          return (
            <select key={i} data-testid={`filter-${f.label}`} value={f.value || ''} onChange={f.onSelect}>
              <option value=''>--</option>
              {(f.options || []).map((opt: any) => {
                const v = opt.value || opt;
                const l = opt.label || opt;
                return (
                  <option key={v} value={v}>
                    {l}
                  </option>
                );
              })}
            </select>
          );
        }
        if (f.type === 'multi-dropdown') {
          return (
            <button
              key={i}
              data-testid={`multi-${f.label}`}
              onClick={() => {
                const first = (f.options || [])[0];
                if (first) f.onSelect({}, [first]);
              }}
            >
              {f.label}
            </button>
          );
        }
        return null;
      })}
      {children}
    </div>
  ),
}));

jest.mock('@components1/k8s/common/KubernetesTable2', () => ({
  __esModule: true,
  default: ({ id, data, headers, totalRows, loading, pageNumber }: any) => (
    <div data-testid='k8s-table' id={id}>
      {loading && <div data-testid='loading'>loading</div>}
      <div data-testid='total'>{totalRows}</div>
      <div data-testid='page'>{pageNumber}</div>
      <div data-testid='headers'>{headers.map((h: any) => h.name).join('|')}</div>
      {(data || []).map((row: any, i: number) => (
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

jest.mock('@components1/triage/TriageRuleModal', () => ({
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

import TriageRulesManager from '@components1/triage/TriageRulesManager';

const apiTriage = require('@api1/triage').default;
const { snackbar } = require('@components1/common/snackbarService');
const { applyFiltersOnRouter } = require('@lib/router');

const sampleAccounts = [{ id: 'acc-1', account_name: 'AWS Prod', label: 'AWS Prod', cloud_provider: 'aws' }];

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
    expect(screen.getByTestId('label-blue')).toHaveTextContent('System');
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
    await waitFor(() => expect(screen.getByTestId('custom-btn-Create Rule')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('custom-btn-Create Rule'));

    expect(screen.getByTestId('rule-modal')).toBeInTheDocument();
    expect(screen.getByTestId('rule-modal-mode')).toHaveTextContent('create');
  });

  it('hides Create Rule button when no write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<TriageRulesManager accountId='acc-1' />);
    await waitFor(() => expect(apiTriage.getTriageRules).toHaveBeenCalled());
    expect(screen.queryByTestId('custom-btn-Create Rule')).not.toBeInTheDocument();
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
    expect(screen.getByTestId('ndialog-title')).toHaveTextContent('Suppress Dev Alerts');

    fireEvent.click(screen.getByTestId('ndialog-submit'));

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
    fireEvent.click(screen.getByTestId('ndialog-close'));

    expect(screen.queryByTestId('ndialog')).not.toBeInTheDocument();
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

    await waitFor(() => expect(screen.getByText('No triage rules found')).toBeInTheDocument());
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
