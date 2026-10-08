import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import KnowledgeBaseTab from '@components/llm/KnowledgeBaseTab';
import apiKnowledgeBase from '@api1/knowledge-base';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  KB_AGENT_WILDCARD: '*',
  default: {
    getKnowledgeBases: jest.fn(),
    getKnowledgeBase: jest.fn(),
    getKBAgents: jest.fn(),
    updateKnowledgeBase: jest.fn(),
    createKnowledgeBase: jest.fn(),
    deleteKnowledgeBase: jest.fn(),
    retriggerKB: jest.fn(),
    setKnowledgeBaseEnabled: jest.fn(),
    setKnowledgeBaseAccountOverride: jest.fn(),
    updateKnowledgeBaseVisibility: jest.fn(),
    listKnowledgeBaseAccountOverrides: jest.fn(),
    mapKnowledgeBaseToAgent: jest.fn(),
    unmapKnowledgeBaseFromAgent: jest.fn(),
    getKBLoadHistory: jest.fn(),
    testRetrieval: jest.fn(),
  },
}));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { listAgents: jest.fn(), listTools: jest.fn() } }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { getK8sWorkloadNames: jest.fn(), getK8sNamespaceNames: jest.fn() } }));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn(), isTenantAdmin: jest.fn() }));
jest.mock('@components/llm/ScopeChip', () => ({ __esModule: true, default: () => null, HybridScopeChips: () => null }));
jest.mock('@components/llm/KnowledgePolicySettings', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/KBDocumentsModal', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/KnowledgeBaseFormModal', () => ({
  __esModule: true,
  default: ({ open, onSubmit, scopeOptions }) =>
    open ? (
      <div>
        <span>{`scopes:${(scopeOptions || []).map((o) => o.value).join(',')}`}</span>
        <button type='button' onClick={() => onSubmit({ name: 'Runbooks', agentIds: [] })}>
          Submit KB form
        </button>
        <button type='button' onClick={() => onSubmit({ name: 'Runbooks', agentIds: ['*'], visibility: 'organisation', accountIds: [] })}>
          Submit KB form for the organisation
        </button>
        <button type='button' onClick={() => onSubmit({ name: 'Runbooks', agentIds: [], visibility: 'account', accountIds: [] })}>
          Submit KB form for this account
        </button>
      </div>
    ) : null,
}));
jest.mock('@components/llm/MemoryTable', () => ({
  MemoryTable: ({ columns, rows }) => (
    <div>
      {rows.map((row) => (
        <div key={row.id} data-testid={`row-${row.name}`}>
          {columns.map((c) => (
            <span key={c.key}>{c.render ? c.render(row) : row[c.key]}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));
jest.mock('@shared/navigation/Tabs', () => ({
  __esModule: true,
  default: ({ options, onChange }) => (
    <div>
      {options.tabOptions.map((t) => (
        <button key={t.value} type='button' onClick={() => onChange(t.value)}>
          {`tab:${t.value}`}
        </button>
      ))}
    </div>
  ),
}));
jest.mock('@ui/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }) => (
    <div data-testid={`menu-${data.name}`}>
      {menuItems.map((mi) => (
        <button key={mi.value} type='button' disabled={mi.disabled} onClick={() => onMenuClick(mi, data)}>
          {`menu:${mi.label}`}
        </button>
      ))}
    </div>
  ),
}));
jest.mock('@ui/Switch', () => ({
  Switch: ({ checked, disabled, onChange, 'aria-label': ariaLabel }) => (
    <input type='checkbox' aria-label={ariaLabel} checked={checked} disabled={disabled} onChange={(e) => onChange(e, e.target.checked)} />
  ),
}));

// Tenant-wide raw list: a user KB in one account, an integration KB provisioned
// in two accounts (grouped into one row), and an integration KB in one account.
const kbs = [
  { id: 'kb-m', name: 'Runbooks', kb_type: 'manual', status: 'active', enabled: true, account_id: 'acct-9', account_name: 'Prod' },
  {
    id: 'kb-i1',
    name: 'Confluence',
    kb_type: 'integration',
    integration_id: 'int-1',
    status: 'active',
    enabled: true,
    account_id: 'acct-9',
    account_name: 'Prod',
  },
  {
    id: 'kb-i2',
    name: 'Confluence',
    kb_type: 'integration',
    integration_id: 'int-1',
    status: 'active',
    enabled: true,
    account_id: 'acct-2',
    account_name: 'Stage',
  },
  {
    id: 'kb-s',
    name: 'Jira',
    kb_type: 'integration',
    integration_id: 'int-2',
    status: 'active',
    enabled: true,
    account_id: 'acct-9',
    account_name: 'Prod',
  },
];

const ok = (data = {}) => ({ data, errors: [] });

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockImplementation((id) => id === 'acct-9' || id === 'acct-1');
  isTenantAdmin.mockReturnValue(false);
  apiKnowledgeBase.setKnowledgeBaseAccountOverride.mockResolvedValue(ok());
  apiKnowledgeBase.updateKnowledgeBaseVisibility.mockResolvedValue(ok());
  apiKnowledgeBase.listKnowledgeBaseAccountOverrides.mockResolvedValue(ok([]));
  apiKnowledgeBase.createKnowledgeBase.mockResolvedValue(ok({ id: 'kb-new' }));
  apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok(kbs));
  apiKnowledgeBase.getKnowledgeBase.mockResolvedValue(ok({ id: 'kb-m', name: 'Runbooks', kb_type: 'manual' }));
  apiKnowledgeBase.getKBAgents.mockResolvedValue(ok([]));
  apiKnowledgeBase.updateKnowledgeBase.mockResolvedValue(ok({ id: 'kb-m' }));
  apiKnowledgeBase.deleteKnowledgeBase.mockResolvedValue(ok());
  apiKnowledgeBase.retriggerKB.mockResolvedValue(ok());
  apiKnowledgeBase.setKnowledgeBaseEnabled.mockResolvedValue(ok());
  apiAskNudgebee.listAgents.mockResolvedValue({ data: { data: { ai_list_agents: { data: [] } } } });
  apiAskNudgebee.listTools.mockResolvedValue({ data: { data: { ai_list_tools: { data: [] } } } });
});

describe('KnowledgeBaseTab tenant-wide row actions', () => {
  it('gives a user KB in one account its actions and switch, and keeps Add hidden', async () => {
    render(<KnowledgeBaseTab accountId='' />);

    const row = await screen.findByTestId('row-Runbooks');
    expect(within(row).getByRole('button', { name: 'Edit knowledge base' })).toBeInTheDocument();
    expect(within(row).getByText('menu:Delete')).toBeEnabled();
    expect(within(row).getByRole('checkbox')).toBeEnabled();
    expect(screen.queryByRole('button', { name: 'Add Knowledge Base' })).not.toBeInTheDocument();
  });

  it('keeps an integration KB shared by several accounts read-only, but not one in a single account', async () => {
    render(<KnowledgeBaseTab accountId='' />);
    fireEvent.click(await screen.findByText('tab:integration'));

    const shared = await screen.findByTestId('row-Confluence');
    expect(within(shared).queryByTestId('menu-Confluence')).not.toBeInTheDocument();
    expect(within(shared).getByRole('checkbox')).toBeDisabled();
    const single = screen.getByTestId('row-Jira');
    expect(within(single).getByText('menu:Retrigger')).toBeEnabled();
    expect(within(single).getByRole('checkbox')).toBeEnabled();
  });

  it("runs delete, retrigger and the enable switch against the row's own account", async () => {
    render(<KnowledgeBaseTab accountId='' />);

    const row = await screen.findByTestId('row-Runbooks');
    fireEvent.click(within(row).getByRole('checkbox'));
    await waitFor(() => expect(apiKnowledgeBase.setKnowledgeBaseEnabled).toHaveBeenCalledWith('acct-9', 'kb-m', false));

    fireEvent.click(within(row).getByText('menu:Delete'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(apiKnowledgeBase.deleteKnowledgeBase).toHaveBeenCalledWith('acct-9', 'kb-m'));

    fireEvent.click(screen.getByText('tab:integration'));
    fireEvent.click(within(await screen.findByTestId('row-Jira')).getByText('menu:Retrigger'));
    await waitFor(() => expect(apiKnowledgeBase.retriggerKB).toHaveBeenCalledWith('acct-9', 'kb-s'));
  });

  it("loads the edit form's agents for the row's account and saves to it", async () => {
    render(<KnowledgeBaseTab accountId='' />);
    // Nothing account-scoped loads until an edit opens.
    await screen.findByTestId('row-Runbooks');
    expect(apiAskNudgebee.listAgents).not.toHaveBeenCalled();

    fireEvent.click(within(screen.getByTestId('row-Runbooks')).getByRole('button', { name: 'Edit knowledge base' }));

    await waitFor(() => expect(apiKnowledgeBase.getKnowledgeBase).toHaveBeenCalledWith('acct-9', 'kb-m'));
    expect(apiKnowledgeBase.getKBAgents).toHaveBeenCalledWith('acct-9', 'kb-m');
    await waitFor(() => expect(apiAskNudgebee.listAgents).toHaveBeenCalledWith({ accountId: 'acct-9' }));
    fireEvent.click(await screen.findByText('Submit KB form'));

    await waitFor(() =>
      expect(apiKnowledgeBase.updateKnowledgeBase).toHaveBeenCalledWith('acct-9', 'kb-m', expect.objectContaining({ name: 'Runbooks' }))
    );
  });

  it("disables a row's writes when the caller cannot write to its account", async () => {
    hasWriteAccess.mockImplementation((id) => id === 'acct-other');
    render(<KnowledgeBaseTab accountId='' />);

    const row = await screen.findByTestId('row-Runbooks');
    expect(within(row).queryByRole('button', { name: 'Edit knowledge base' })).not.toBeInTheDocument();
    expect(within(row).getByText('menu:Delete')).toBeDisabled();
    expect(within(row).getByRole('checkbox')).toBeDisabled();
  });

  it('keeps acting on the selected account when one is selected', async () => {
    render(<KnowledgeBaseTab accountId='acct-1' />);

    const row = await screen.findByTestId('row-Runbooks');
    await waitFor(() => expect(apiAskNudgebee.listAgents).toHaveBeenCalledWith({ accountId: 'acct-1' }));
    fireEvent.click(within(row).getByText('menu:Delete'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(apiKnowledgeBase.deleteKnowledgeBase).toHaveBeenCalledWith('acct-1', 'kb-m'));
  });
});

// Organisation KBs: no account_id; visibility says who sees them.
const orgKb = { id: 'kb-o', name: 'Failover', kb_type: 'manual', status: 'active', enabled: true, account_id: '', visibility: 'organisation' };
const selectedKb = { ...orgKb, id: 'kb-sel', name: 'Payments', visibility: 'selected_accounts' };

describe('KnowledgeBaseTab organisation KBs', () => {
  it("gives an account admin only this account's switch, read-only otherwise", async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok([orgKb, { ...selectedKb }]));
    render(<KnowledgeBaseTab accountId='acct-1' />);

    const row = await screen.findByTestId('row-Failover');
    expect(within(row).getByText('Organisation')).toBeInTheDocument();
    expect(within(row).queryByRole('button', { name: 'Edit knowledge base' })).not.toBeInTheDocument();
    expect(within(row).getByText('menu:Delete')).toBeDisabled();
    expect(within(row).queryByText('menu:Turn off for all accounts')).not.toBeInTheDocument();

    fireEvent.click(within(row).getByRole('checkbox'));
    await waitFor(() => expect(apiKnowledgeBase.setKnowledgeBaseAccountOverride).toHaveBeenCalledWith('acct-1', 'kb-o', false));
    expect(apiKnowledgeBase.setKnowledgeBaseEnabled).not.toHaveBeenCalled();

    // Only a tenant admin changes which accounts use a selected-accounts KB.
    expect(within(screen.getByTestId('row-Payments')).getByRole('checkbox')).toBeDisabled();
  });

  it('shows a KB this account switched off with its switch off, so it can be switched back on', async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok([{ ...orgKb, switched_off: true }]));
    render(<KnowledgeBaseTab accountId='acct-1' />);

    const box = within(await screen.findByTestId('row-Failover')).getByRole('checkbox');
    expect(box).not.toBeChecked();
    fireEvent.click(box);
    await waitFor(() => expect(apiKnowledgeBase.setKnowledgeBaseAccountOverride).toHaveBeenCalledWith('acct-1', 'kb-o', true));
  });

  it('lets a tenant admin edit, delete and turn off an organisation KB for all accounts from an account view', async () => {
    isTenantAdmin.mockReturnValue(true);
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok([orgKb]));
    apiKnowledgeBase.getKnowledgeBase.mockResolvedValue(ok({ ...orgKb }));
    render(<KnowledgeBaseTab accountId='acct-1' />);

    const row = await screen.findByTestId('row-Failover');
    fireEvent.click(within(row).getByText('menu:Turn off for all accounts'));
    await waitFor(() => expect(apiKnowledgeBase.setKnowledgeBaseEnabled).toHaveBeenCalledWith('', 'kb-o', false));

    fireEvent.click(within(screen.getByTestId('row-Failover')).getByRole('button', { name: 'Edit knowledge base' }));
    await waitFor(() => expect(apiKnowledgeBase.getKnowledgeBase).toHaveBeenCalledWith('', 'kb-o'));
    expect(apiKnowledgeBase.getKBAgents).not.toHaveBeenCalled();
    expect(await screen.findByText('scopes:account,organisation,selected_accounts')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Submit KB form for the organisation'));
    await waitFor(() => expect(apiKnowledgeBase.updateKnowledgeBase).toHaveBeenCalledWith('', 'kb-o', expect.any(Object)));
    expect(apiKnowledgeBase.updateKnowledgeBaseVisibility).not.toHaveBeenCalled();
    expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).not.toHaveBeenCalled();

    fireEvent.click(within(screen.getByTestId('row-Failover')).getByText('menu:Delete'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(apiKnowledgeBase.deleteKnowledgeBase).toHaveBeenCalledWith('', 'kb-o'));
  });

  it('widens an account KB to the organisation after saving its content', async () => {
    isTenantAdmin.mockReturnValue(true);
    render(<KnowledgeBaseTab accountId='acct-1' />);

    fireEvent.click(within(await screen.findByTestId('row-Runbooks')).getByRole('button', { name: 'Edit knowledge base' }));
    fireEvent.click(await screen.findByText('Submit KB form for the organisation'));

    await waitFor(() => expect(apiKnowledgeBase.updateKnowledgeBaseVisibility).toHaveBeenCalledWith('kb-m', 'organisation', [], ''));
    expect(apiKnowledgeBase.updateKnowledgeBase).toHaveBeenCalledWith('acct-1', 'kb-m', expect.any(Object));
  });

  it('lets a tenant admin add organisation KBs and switch them for everyone from the tenant-wide view', async () => {
    isTenantAdmin.mockReturnValue(true);
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok([orgKb]));
    render(<KnowledgeBaseTab accountId='' />);

    const row = await screen.findByTestId('row-Failover');
    fireEvent.click(within(row).getByRole('checkbox'));
    await waitFor(() => expect(apiKnowledgeBase.setKnowledgeBaseEnabled).toHaveBeenCalledWith('', 'kb-o', false));
    expect(apiKnowledgeBase.setKnowledgeBaseAccountOverride).not.toHaveBeenCalled();
    expect(within(row).queryByText('menu:Load History')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Add Knowledge Base' }));
    expect(await screen.findByText('scopes:organisation,selected_accounts')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Submit KB form for the organisation'));
    await waitFor(() =>
      expect(apiKnowledgeBase.createKnowledgeBase).toHaveBeenCalledWith('', expect.objectContaining({ visibility: 'organisation' }))
    );
    expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).not.toHaveBeenCalled();
  });

  it('maps an organisation KB moved back to one account to all agents there', async () => {
    isTenantAdmin.mockReturnValue(true);
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue(ok([orgKb]));
    apiKnowledgeBase.getKnowledgeBase.mockResolvedValue(ok({ ...orgKb }));
    apiKnowledgeBase.mapKnowledgeBaseToAgent.mockResolvedValue(ok());
    render(<KnowledgeBaseTab accountId='acct-1' />);

    fireEvent.click(within(await screen.findByTestId('row-Failover')).getByRole('button', { name: 'Edit knowledge base' }));
    fireEvent.click(await screen.findByText('Submit KB form for this account'));

    await waitFor(() => expect(apiKnowledgeBase.updateKnowledgeBaseVisibility).toHaveBeenCalledWith('kb-o', 'account', [], 'acct-1'));
    await waitFor(() => expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).toHaveBeenCalledWith('acct-1', 'kb-o', '*'));
  });
});
