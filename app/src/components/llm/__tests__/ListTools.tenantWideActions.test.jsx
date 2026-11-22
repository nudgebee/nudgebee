import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import ListTools from '@components/llm/ListTools';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listTools: jest.fn(),
    deleteTool: jest.fn(),
    setToolAccountOverride: jest.fn(),
    updateToolVisibility: jest.fn(),
    listToolAccountOverrides: jest.fn(),
  },
}));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn(), isTenantAdmin: jest.fn() }));
jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ baseTitle: 'Nudgebee' }),
  useBrandingConfig: () => ({ title: 'Nudgebee' }),
}));
jest.mock('@components/llm/ScopeChip', () => ({ __esModule: true, default: () => null, HybridScopeChips: () => null }));
jest.mock('@components/llm/CreateTool', () => ({
  __esModule: true,
  default: ({ accountId, scopeOptions, initialVisibility }) => (
    <div
      data-testid='create-tool-form'
      data-account-id={accountId}
      data-scopes={(scopeOptions || []).map((o) => o.value).join(',')}
      data-initial-visibility={initialVisibility}
    />
  ),
}));
jest.mock('@components/llm/KnowledgeBaseSharingModal', () => ({
  __esModule: true,
  default: ({ open, item, noun, accountOption }) =>
    open ? (
      <div data-testid='sharing-modal' data-item-id={item?.id} data-noun={noun} data-target-account={accountOption?.targetAccountId || ''} />
    ) : null,
}));
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ headers, tableData }) => (
    <div>
      {headers.map((h) => (
        <span key={h.name} data-testid='header'>
          {h.name}
        </span>
      ))}
      {tableData.map((row, i) => (
        <div key={i} data-testid={`row-${row[0].rawData.name}`}>
          {row.map((cell, j) => (
            <span key={j}>{cell.component ?? cell.text}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const systemTool = { name: 'k8s_logs', type: 'system', status: 'enabled', nb_tool_type: 'tool', description: 'System tool', approval: 'never' };
const customTool = {
  name: 'my_tool',
  type: 'custom',
  status: 'enabled',
  nb_tool_type: 'tool',
  description: 'Custom tool',
  account_id: 'acct-9',
  account_name: 'Prod',
  approval: 'always',
};

const renderList = (accountId) => render(<ListTools accountId={accountId} />);

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockReturnValue(true);
  isTenantAdmin.mockReturnValue(false);
  apiAskNudgebee.listTools.mockResolvedValue({ data: { data: { ai_list_tools: { data: [systemTool, customTool] } } } });
  apiAskNudgebee.deleteTool.mockResolvedValue({ data: { data: { ai_delete_tool: { data: { status: 'ok' } } } } });
});

describe('ListTools tenant-wide Actions column', () => {
  it('shows the Actions column with Edit / Delete on user-created tools only', async () => {
    renderList('');

    const customRow = await screen.findByTestId('row-my_tool');
    expect(screen.getAllByTestId('header').map((h) => h.textContent)).toEqual(['Name', 'Description', 'Status', 'Approval', 'Account', 'Actions']);
    expect(within(customRow).getByRole('button', { name: 'Edit tool' })).toBeInTheDocument();
    expect(within(customRow).getByRole('button', { name: 'Delete tool' })).toBeInTheDocument();
    expect(within(screen.getByTestId('row-k8s_logs')).queryByRole('button', { name: 'Edit tool' })).not.toBeInTheDocument();
  });

  it('shows when each tool asks for approval (#40399)', async () => {
    renderList('');

    expect(within(await screen.findByTestId('row-my_tool')).getByText('Always asks')).toBeInTheDocument();
    expect(within(screen.getByTestId('row-k8s_logs')).getByText('Never asks')).toBeInTheDocument();
    // Agents are on the Agents tab, so the list asks the API for tools only.
    expect(apiAskNudgebee.listTools).toHaveBeenCalledWith({ accountId: '', nbToolType: 'tool', includeSwitchedOff: true });
  });

  it("hides Edit / Delete when the caller cannot write to the tool's account", async () => {
    hasWriteAccess.mockImplementation((id) => id === 'acct-other');
    renderList('');

    const customRow = await screen.findByTestId('row-my_tool');
    expect(within(customRow).queryByRole('button', { name: 'Edit tool' })).not.toBeInTheDocument();
    expect(hasWriteAccess).toHaveBeenCalledWith('acct-9');
  });

  it("deletes a user-created tool against the tool's own account", async () => {
    renderList('');

    fireEvent.click(within(await screen.findByTestId('row-my_tool')).getByRole('button', { name: 'Delete tool' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(apiAskNudgebee.deleteTool).toHaveBeenCalledWith('acct-9', 'my_tool'));
  });

  it("opens Edit Tool scoped to the tool's own account", async () => {
    renderList('');

    fireEvent.click(within(await screen.findByTestId('row-my_tool')).getByRole('button', { name: 'Edit tool' }));

    expect(await screen.findByTestId('create-tool-form')).toHaveAttribute('data-account-id', 'acct-9');
  });

  it('keeps acting on the selected account when one is selected', async () => {
    renderList('acct-1');

    fireEvent.click(within(await screen.findByTestId('row-my_tool')).getByRole('button', { name: 'Delete tool' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(apiAskNudgebee.deleteTool).toHaveBeenCalledWith('acct-1', 'my_tool'));
  });
});

// #41073: organisation custom tools.
const orgTool = {
  id: 'tool-org',
  name: 'org_tool',
  type: 'custom',
  status: 'enabled',
  nb_tool_type: 'tool',
  description: 'Organisation tool',
  account_id: '',
  visibility: 'organisation',
};
const listing = (tools) => ({ data: { data: { ai_list_tools: { data: tools } } } });

describe('ListTools organisation tools', () => {
  it('shows an account admin the badge and a switch, but no Edit / Delete', async () => {
    apiAskNudgebee.listTools.mockResolvedValue(listing([orgTool, { ...orgTool, id: 'tool-sel', name: 'sel_tool', visibility: 'selected_accounts' }]));
    apiAskNudgebee.setToolAccountOverride.mockResolvedValue({ data: true, errors: [] });
    renderList('acct-1');

    const row = await screen.findByTestId('row-org_tool');
    expect(within(row).getByTestId('tool-org-badge')).toHaveTextContent('Organisation');
    expect(within(screen.getByTestId('row-sel_tool')).getByTestId('tool-org-badge')).toHaveTextContent('Selected accounts');
    expect(within(row).queryByRole('button', { name: 'Edit tool' })).not.toBeInTheDocument();
    expect(within(row).queryByRole('button', { name: 'Delete tool' })).not.toBeInTheDocument();
    expect(within(screen.getByTestId('row-sel_tool')).getByRole('checkbox')).toBeDisabled();

    const toggle = within(row).getByRole('checkbox');
    expect(toggle).toBeChecked();
    fireEvent.click(toggle);

    await waitFor(() => expect(apiAskNudgebee.setToolAccountOverride).toHaveBeenCalledWith('acct-1', 'tool-org', false));
    await waitFor(() => expect(within(row).getByRole('checkbox')).not.toBeChecked());
  });

  it('shows a switched-off organisation tool with its switch off so it can be switched back on', async () => {
    apiAskNudgebee.listTools.mockResolvedValue(listing([{ ...orgTool, switched_off: true }]));
    apiAskNudgebee.setToolAccountOverride.mockResolvedValue({ data: true, errors: [] });
    renderList('acct-1');

    const toggle = within(await screen.findByTestId('row-org_tool')).getByRole('checkbox');
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);

    await waitFor(() => expect(apiAskNudgebee.setToolAccountOverride).toHaveBeenCalledWith('acct-1', 'tool-org', true));
  });

  it('puts the switch back when the backend refuses it', async () => {
    apiAskNudgebee.listTools.mockResolvedValue(listing([orgTool]));
    apiAskNudgebee.setToolAccountOverride.mockResolvedValue({ data: null, errors: [{ message: 'nope' }] });
    renderList('acct-1');

    const row = await screen.findByTestId('row-org_tool');
    fireEvent.click(within(row).getByRole('checkbox'));

    await waitFor(() => expect(apiAskNudgebee.setToolAccountOverride).toHaveBeenCalled());
    await waitFor(() => expect(within(row).getByRole('checkbox')).toBeChecked());
  });

  it('lets a tenant admin open "Who can use it" and delete an organisation tool through its owner', async () => {
    isTenantAdmin.mockReturnValue(true);
    apiAskNudgebee.listTools.mockResolvedValue(listing([orgTool]));
    renderList('acct-1');

    const row = await screen.findByTestId('row-org_tool');
    fireEvent.click(within(row).getByRole('button', { name: 'Who can use it' }));
    const modal = await screen.findByTestId('sharing-modal');
    expect(modal).toHaveAttribute('data-item-id', 'tool-org');
    expect(modal).toHaveAttribute('data-noun', 'tool');
    expect(modal).toHaveAttribute('data-target-account', 'acct-1');

    fireEvent.click(within(row).getByRole('button', { name: 'Delete tool' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(apiAskNudgebee.deleteTool).toHaveBeenCalledWith('', 'org_tool'));
  });

  it('opens Edit Tool for an organisation tool with no account', async () => {
    isTenantAdmin.mockReturnValue(true);
    apiAskNudgebee.listTools.mockResolvedValue(listing([orgTool]));
    renderList('');

    fireEvent.click(within(await screen.findByTestId('row-org_tool')).getByRole('button', { name: 'Edit tool' }));
    expect(await screen.findByTestId('create-tool-form')).toHaveAttribute('data-account-id', '');
  });

  it('hides Create in the tenant-wide view from non-tenant-admins', async () => {
    renderList('');
    await screen.findByTestId('row-my_tool');
    expect(screen.queryByRole('button', { name: /Create Tool/ })).not.toBeInTheDocument();
  });

  it('gives a tenant admin Create in the tenant-wide view with the organisation scopes only', async () => {
    isTenantAdmin.mockReturnValue(true);
    renderList('');
    await screen.findByTestId('row-my_tool');

    fireEvent.click(screen.getByRole('button', { name: /Create Tool/ }));
    const form = await screen.findByTestId('create-tool-form');
    expect(form).toHaveAttribute('data-scopes', 'organisation,selected_accounts');
    expect(form).toHaveAttribute('data-initial-visibility', 'organisation');
  });
});
