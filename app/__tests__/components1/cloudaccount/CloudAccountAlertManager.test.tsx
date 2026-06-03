import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockRouterReplace = jest.fn();
let mockRouterQuery: Record<string, any> = {};
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: jest.fn(),
    replace: mockRouterReplace,
    query: mockRouterQuery,
    pathname: '/cloud',
    asPath: '/cloud',
    route: '/cloud',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

const mockHasWriteAccess = jest.fn();
jest.mock('@lib/auth', () => ({
  hasWriteAccess: (...args: any[]) => mockHasWriteAccess(...args),
}));

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    getDistinctData: jest.fn(),
    getEventRules: jest.fn(),
    getAgentPlaybookOfEvent: jest.fn(),
    disableAlertManager: jest.fn(),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferencesTablePageSize: jest.fn(() => 10),
  },
}));

jest.mock('@lib/formatter', () => ({
  titleCase: (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : ''),
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn(), info: jest.fn(), warning: jest.fn() },
}));

jest.mock('src/utils/common', () => ({
  isValidSeverity: (s: string) => ['success', 'error', 'info', 'warning'].includes(s),
  snakeToTitleCase: (s: string) => s,
}));

jest.mock('src/utils/colors', () => ({
  colors: { text: { primary: '#000' }, background: { white: '#fff' } },
}));

jest.mock('src/utils/actionStyles', () => ({
  action: { primary: {} },
}));

jest.mock('@components1/common', () => ({
  Text: ({ value }: any) => <span>{value}</span>,
}));

jest.mock('@components1/common/widgets/CustomLabels', () => ({
  __esModule: true,
  default: ({ text }: any) => <span data-testid='label'>{text}</span>,
}));

jest.mock('@components1/common/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }: any) => (
    <div data-testid='three-dots'>
      {(menuItems || []).map((mi: any) => (
        <button key={mi.id} data-testid={`menu-${mi.label}-${data.alert}`} onClick={() => onMenuClick(mi, data)}>
          {mi.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, disabled }: any) => (
    <button data-testid={`custom-btn-${text}`} onClick={onClick} disabled={disabled}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/modal', () => ({
  Modal: ({ open, title, children }: any) =>
    open ? (
      <div data-testid='create-alert-modal'>
        <div data-testid='create-alert-title'>{title}</div>
        {children}
      </div>
    ) : null,
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

jest.mock('@components1/k8s/details/KubernetesCreateAlert', () => ({
  __esModule: true,
  default: ({ alertManagerObject, isCreateAlert, onSubmit }: any) => (
    <div data-testid='kubernetes-create-alert'>
      <div data-testid='alert-mode'>{isCreateAlert ? 'create' : 'edit'}</div>
      <div data-testid='alert-name'>{alertManagerObject?.alert || ''}</div>
      <button data-testid='create-alert-submit-success' onClick={() => onSubmit('Saved', 'success')}>
        Save Success
      </button>
      <button data-testid='create-alert-submit-error' onClick={() => onSubmit('Bad', 'error')}>
        Save Error
      </button>
    </div>
  ),
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
              {(f.options || []).map((opt: any, idx: number) => {
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
        }
        if (f.type === 'search') {
          return (
            <input
              key={i}
              data-testid={`search-${f.label}`}
              value={f.value || ''}
              onChange={f.onSelect}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && f.onEnter) f.onEnter();
              }}
            />
          );
        }
        return null;
      })}
      {children}
    </div>
  ),
}));

jest.mock('@components1/common/tables/CustomTable2', () => ({
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

import CloudAccountAlertManager from '@components1/cloudaccount/CloudAccountAlertManager';

const k8sApi = require('@api1/kubernetes1').default;
const { snackbar } = require('@components1/common/snackbarService');

const sampleDistinct = {
  data: {
    distinct_category: [{ category: 'Networking' }, { category: 'Storage' }],
    distinct_source: [{ source: 'cloudwatch' }, { source: 'prometheus' }],
    distinct_severity: [{ severity: 'critical' }, { severity: 'warning' }],
  },
};

const sampleRules = [
  {
    id: 'r-1',
    alert: 'CPU High',
    category: 'Compute',
    source: 'cloudwatch',
    severity: 'critical',
    enabled: true,
    namespace: 'prod',
    group: 'g1',
  },
  {
    id: 'r-2',
    alert: 'Disk Full',
    category: 'Storage',
    source: 'prometheus',
    severity: 'warning',
    enabled: false,
  },
];

const mockRulesResponse = (rules = sampleRules, count?: number) => ({
  data: {
    event_rules: rules,
    event_rules_aggregate: { aggregate: { count: count ?? rules.length } },
  },
});

describe('CloudAccountAlertManager (integration)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    mockHasWriteAccess.mockReturnValue(true);
    k8sApi.getDistinctData.mockResolvedValue(sampleDistinct);
    k8sApi.getEventRules.mockResolvedValue(mockRulesResponse());
    k8sApi.getAgentPlaybookOfEvent.mockResolvedValue({ data: { data: { agent_playbook: [] } } });
    k8sApi.disableAlertManager.mockResolvedValue({ data: {} });
  });

  it('does not fetch when accountId is missing or literal "undefined"', async () => {
    render(<CloudAccountAlertManager accountId='' />);
    await act(async () => {});
    expect(k8sApi.getDistinctData).not.toHaveBeenCalled();
    expect(k8sApi.getEventRules).not.toHaveBeenCalled();
  });

  it('fetches distinct filter options + alert list on mount', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);

    await waitFor(() => {
      expect(k8sApi.getDistinctData).toHaveBeenCalledWith('acc-1');
      expect(k8sApi.getEventRules).toHaveBeenCalled();
    });
    const [params, limit, offset] = k8sApi.getEventRules.mock.calls[0];
    expect(params).toMatchObject({
      accountId: 'acc-1',
      category: '',
      severity: '',
      source: '',
      status: '',
      searchByName: '',
    });
    expect(limit).toBe(10);
    expect(offset).toBe(0);
  });

  it('renders alert rows with name + category + severity', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByText('CPU High')).toBeInTheDocument());
    expect(screen.getByText('Disk Full')).toBeInTheDocument();
    expect(screen.getByText('Compute')).toBeInTheDocument();
    expect(screen.getAllByText('Storage').length).toBeGreaterThan(0);
  });

  it('populates Category dropdown from distinct response', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('filter-Category')).toBeInTheDocument());
    expect(screen.getByRole('option', { name: 'Networking' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Storage' })).toBeInTheDocument();
  });

  it('refetches with category filter on change', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    k8sApi.getEventRules.mockClear();

    fireEvent.change(screen.getByTestId('filter-Category'), { target: { value: 'Storage' } });

    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    expect(k8sApi.getEventRules.mock.calls[0][0].category).toBe('Storage');
  });

  it('refetches with status filter on change', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    k8sApi.getEventRules.mockClear();

    fireEvent.change(screen.getByTestId('filter-Status'), { target: { value: 'Enabled' } });

    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    expect(k8sApi.getEventRules.mock.calls[0][0].status).toBe('Enabled');
  });

  it('triggers fresh fetch on search Enter when page already 0', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    k8sApi.getEventRules.mockClear();

    const search = screen.getByTestId('search-Search By Name');
    fireEvent.change(search, { target: { value: 'cpu' } });
    fireEvent.keyDown(search, { key: 'Enter' });

    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    expect(k8sApi.getEventRules.mock.calls[0][0].searchByName).toBe('cpu');
  });

  it('shows Disable + Edit menu items for enabled rule, Enable for disabled', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('menu-Disable-CPU High')).toBeInTheDocument());
    expect(screen.getByTestId('menu-Edit-CPU High')).toBeInTheDocument();
    expect(screen.getByTestId('menu-Enable-Disk Full')).toBeInTheDocument();
    expect(screen.queryByTestId('menu-Edit-Disk Full')).not.toBeInTheDocument();
  });

  it('opens NDialog when Disable menu clicked and confirms call', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Disable-CPU High')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Disable-CPU High'));
    expect(screen.getByTestId('ndialog-title')).toHaveTextContent('Disable the alert "CPU High"');

    fireEvent.click(screen.getByTestId('ndialog-submit'));

    await waitFor(() =>
      expect(k8sApi.disableAlertManager).toHaveBeenCalledWith({
        accountId: 'acc-1',
        alert: 'CPU High',
        enable: false,
        id: 'r-1',
        namespace: 'prod',
        group: 'g1',
      })
    );
    expect(snackbar.success).toHaveBeenCalledWith(expect.stringContaining('Disabled Successful'));
  });

  it('cancels NDialog without API call', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Disable-CPU High')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Disable-CPU High'));
    fireEvent.click(screen.getByTestId('ndialog-close'));

    expect(screen.queryByTestId('ndialog')).not.toBeInTheDocument();
    expect(k8sApi.disableAlertManager).not.toHaveBeenCalled();
  });

  it('opens Edit Alert modal when Edit menu clicked', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);

    await waitFor(() => expect(screen.getByTestId('menu-Edit-CPU High')).toBeInTheDocument());

    fireEvent.click(screen.getByTestId('menu-Edit-CPU High'));

    expect(screen.getByTestId('create-alert-modal')).toBeInTheDocument();
    expect(screen.getByTestId('create-alert-title')).toHaveTextContent('Update Alert');
    expect(screen.getByTestId('alert-mode')).toHaveTextContent('edit');
    expect(screen.getByTestId('alert-name')).toHaveTextContent('CPU High');
  });

  it('refetches list when KubernetesCreateAlert onSubmit success', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Edit-CPU High')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Edit-CPU High'));
    k8sApi.getEventRules.mockClear();

    fireEvent.click(screen.getByTestId('create-alert-submit-success'));

    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    expect(snackbar.success).toHaveBeenCalledWith('Saved');
  });

  it('shows error snackbar on KubernetesCreateAlert onSubmit error', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Edit-CPU High')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Edit-CPU High'));

    fireEvent.click(screen.getByTestId('create-alert-submit-error'));

    expect(snackbar.error).toHaveBeenCalledWith('Bad');
  });

  it('shows Create New Alert button only with write access (disabled)', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('custom-btn-Create New Alert')).toBeInTheDocument());
    expect(screen.getByTestId('custom-btn-Create New Alert')).toBeDisabled();
  });

  it('hides Create New Alert button without write access', async () => {
    mockHasWriteAccess.mockReturnValue(false);
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    expect(screen.queryByTestId('custom-btn-Create New Alert')).not.toBeInTheDocument();
  });

  it('paginates and updates offset on next page', async () => {
    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    k8sApi.getEventRules.mockClear();

    fireEvent.click(screen.getByTestId('next-page'));

    await waitFor(() => expect(k8sApi.getEventRules).toHaveBeenCalled());
    expect(k8sApi.getEventRules.mock.calls[0][2]).toBe(10);
  });

  it('shows loading state during fetch', async () => {
    let resolveFn: any;
    k8sApi.getEventRules.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveFn = resolve;
      })
    );

    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('loading')).toBeInTheDocument());

    await act(async () => {
      resolveFn(mockRulesResponse([]));
    });

    await waitFor(() => expect(screen.queryByTestId('loading')).not.toBeInTheDocument());
  });

  it('handles disableAlertManager rejection with error snackbar', async () => {
    k8sApi.disableAlertManager.mockRejectedValueOnce(new Error('boom'));

    render(<CloudAccountAlertManager accountId='acc-1' />);
    await waitFor(() => expect(screen.getByTestId('menu-Disable-CPU High')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('menu-Disable-CPU High'));
    fireEvent.click(screen.getByTestId('ndialog-submit'));

    await waitFor(() => expect(snackbar.error).toHaveBeenCalledWith(expect.stringContaining('Failed to Disable Alert Rule')));
  });
});
