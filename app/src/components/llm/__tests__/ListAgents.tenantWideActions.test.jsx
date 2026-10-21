import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import ListAgents, { alwaysLoadDisabledReason } from '@components/llm/ListAgents';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiKnowledgeBase from '@api1/knowledge-base';
import { hasWriteAccess, isTenantAdmin } from '@lib/auth';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    deleteAgent: jest.fn(),
    listAgentExtensions: jest.fn(),
    setAgentAccountOverride: jest.fn(),
    updateAgentVisibility: jest.fn(),
    listAgentAccountOverrides: jest.fn(),
  },
}));
jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  KB_AGENT_WILDCARD: '*',
  ALWAYS_LOAD_MAX_CHARS: 4000,
  default: {
    getAgentsWithKbCounts: jest.fn(),
    getKnowledgeBases: jest.fn(),
    getAgentKnowledgeBases: jest.fn(),
    mapKnowledgeBaseToAgent: jest.fn(),
    unmapKnowledgeBaseFromAgent: jest.fn(),
  },
}));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn(), isTenantAdmin: jest.fn() }));
jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ baseTitle: 'Nudgebee' }),
  useBrandingConfig: () => ({ title: 'Nudgebee' }),
}));
jest.mock('@components/llm/ScopeChip', () => ({ HybridScopeChips: () => null }));
jest.mock('@components/llm/CreateAgentNew', () => ({
  __esModule: true,
  default: ({ accountId, scopeOptions, initialVisibility }) => (
    <div
      data-testid='create-agent-form'
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
jest.mock('@components/llm/CreateAgentExtension', () => () => null);
jest.mock('@ui/ThreeDotsMenu', () => ({
  __esModule: true,
  default: ({ menuItems, data, onMenuClick }) => (
    <div data-testid={`actions-${data.name}`}>
      {menuItems.map((mi) => (
        <button key={mi.id} onClick={() => onMenuClick(mi, data)}>
          {mi.label}
        </button>
      ))}
    </div>
  ),
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
        <div key={i}>
          {row.map((cell, j) => (
            <span key={j}>{cell.component}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const systemAgent = { name: 'k8s-agent', type: 'system', status: 'enabled', description: 'System agent', tools: [] };
const customAgent = {
  name: 'my-agent',
  type: 'custom',
  status: 'enabled',
  description: 'Custom agent',
  tools: [],
  account_id: 'acct-9',
  account_name: 'Prod',
};

const renderList = (accountId) =>
  render(<ListAgents accountId={accountId} allAgents={[systemAgent, customAgent]} refreshAgentListing={jest.fn()} loadingAgents={false} />);

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockReturnValue(true);
  isTenantAdmin.mockReturnValue(false);
  apiAskNudgebee.listAgentExtensions.mockResolvedValue({ data: [] });
  apiAskNudgebee.deleteAgent.mockResolvedValue({ data: { data: { ai_delete_agent: { data: { status: 'ok' } } } } });
  apiKnowledgeBase.getAgentsWithKbCounts.mockResolvedValue({ data: [] });
  apiKnowledgeBase.getKnowledgeBases.mockResolvedValue({ data: [] });
  apiKnowledgeBase.getAgentKnowledgeBases.mockResolvedValue({ data: [] });
});

describe('ListAgents tenant-wide Action column', () => {
  it('shows the Action column with a menu on user-created agents only', async () => {
    renderList('');

    expect(screen.getAllByTestId('header').map((h) => h.textContent)).toEqual([
      'Name',
      'Description',
      'Status',
      'Tools',
      'Usage',
      'Account',
      'Action',
    ]);
    expect(await screen.findByTestId('actions-my-agent')).toBeInTheDocument();
    expect(screen.queryByTestId('actions-k8s-agent')).not.toBeInTheDocument();
  });

  it("hides the menu when the caller cannot write to the agent's account", () => {
    hasWriteAccess.mockImplementation((id) => id === 'acct-other');
    renderList('');

    expect(screen.queryByTestId('actions-my-agent')).not.toBeInTheDocument();
    expect(hasWriteAccess).toHaveBeenCalledWith('acct-9');
  });

  it("deletes a user-created agent against the agent's own account", async () => {
    renderList('');

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Delete Agent'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(apiAskNudgebee.deleteAgent).toHaveBeenCalledWith('acct-9', 'my-agent'));
  });

  it("loads and saves prioritized knowledge against the agent's own account", async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue({
      data: [
        { id: 'kb-1', name: 'Runbooks' },
        { id: 'kb-2', name: 'Postmortems' },
      ],
    });
    apiKnowledgeBase.getAgentKnowledgeBases.mockImplementation((_acct, name) => Promise.resolve({ data: name === '*' ? [] : [{ id: 'kb-2' }] }));
    apiKnowledgeBase.mapKnowledgeBaseToAgent.mockResolvedValue({});
    apiKnowledgeBase.unmapKnowledgeBaseFromAgent.mockResolvedValue({});
    renderList('');

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Prioritize knowledge'));

    await waitFor(() => expect(apiKnowledgeBase.getKnowledgeBases).toHaveBeenCalledWith('acct-9'));
    expect(apiKnowledgeBase.getAgentKnowledgeBases).toHaveBeenCalledWith('acct-9', 'my-agent');
    // #39919: the dialog says what a mapping does: every agent can use every
    // knowledge base, and the ticked ones are prioritized for this agent.
    expect(await screen.findByText(/Every agent can use all of this account's knowledge bases/)).toBeInTheDocument();
    expect(screen.getByText('Prioritized')).toBeInTheDocument();
    fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Runbooks' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select Postmortems' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));

    await waitFor(() => expect(apiKnowledgeBase.unmapKnowledgeBaseFromAgent).toHaveBeenCalledWith('acct-9', 'kb-2', 'my-agent'));
    expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).toHaveBeenCalledWith('acct-9', 'kb-1', 'my-agent');
  });

  it("opens Edit Agent scoped to the agent's own account", async () => {
    renderList('');

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Edit Agent'));

    expect(await screen.findByTestId('create-agent-form')).toHaveAttribute('data-account-id', 'acct-9');
  });

  it('keeps the menu on every agent when an account is selected', async () => {
    renderList('acct-1');

    expect(await screen.findByTestId('actions-my-agent')).toBeInTheDocument();
    expect(screen.getByTestId('actions-k8s-agent')).toBeInTheDocument();
    fireEvent.click(within(screen.getByTestId('actions-my-agent')).getByText('Delete Agent'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

    await waitFor(() => expect(apiAskNudgebee.deleteAgent).toHaveBeenCalledWith('acct-1', 'my-agent'));
  });
});

// #41073: organisation custom agents.
const orgAgent = {
  id: 'agent-org',
  name: 'org-agent',
  type: 'custom',
  status: 'enabled',
  description: 'Organisation agent',
  tools: [],
  account_id: '',
  visibility: 'organisation',
};

const renderWith = (accountId, agents, refresh = jest.fn()) =>
  render(<ListAgents accountId={accountId} allAgents={agents} refreshAgentListing={refresh} loadingAgents={false} />);

describe('ListAgents organisation agents', () => {
  it('shows an account admin the badge and a switch, but no edit actions', async () => {
    apiAskNudgebee.setAgentAccountOverride.mockResolvedValue({ data: true, errors: [] });
    const refresh = jest.fn();
    renderWith('acct-1', [orgAgent, { ...orgAgent, id: 'agent-sel', name: 'sel-agent', visibility: 'selected_accounts' }], refresh);

    const badges = await screen.findAllByTestId('agent-org-badge');
    expect(badges.map((b) => b.textContent)).toEqual(['Organisation', 'Selected accounts']);
    const menu = screen.getByTestId('actions-org-agent');
    expect(within(menu).getByText('Prioritize knowledge')).toBeInTheDocument();
    expect(within(menu).queryByText('Edit Agent')).not.toBeInTheDocument();
    expect(within(menu).queryByText('Delete Agent')).not.toBeInTheDocument();
    expect(within(menu).queryByText('Who can use it')).not.toBeInTheDocument();

    const [orgSwitch, selectedSwitch] = screen.getAllByRole('checkbox', { name: /in this account/ });
    expect(orgSwitch).toBeChecked();
    // Only a tenant admin changes which accounts use a selected-accounts agent.
    expect(selectedSwitch).toBeDisabled();
    fireEvent.click(orgSwitch);

    await waitFor(() => expect(apiAskNudgebee.setAgentAccountOverride).toHaveBeenCalledWith('acct-1', 'agent-org', false));
    await waitFor(() => expect(refresh).toHaveBeenCalled());
  });

  it('shows a switched-off organisation agent with its switch off so it can be switched back on', async () => {
    apiAskNudgebee.setAgentAccountOverride.mockResolvedValue({ data: true, errors: [] });
    renderWith('acct-1', [{ ...orgAgent, switched_off: true }]);

    const toggle = await screen.findByRole('checkbox', { name: 'Use agent org-agent in this account' });
    expect(toggle).not.toBeChecked();
    fireEvent.click(toggle);

    await waitFor(() => expect(apiAskNudgebee.setAgentAccountOverride).toHaveBeenCalledWith('acct-1', 'agent-org', true));
  });

  it('lets a tenant admin open "Who can use it" and delete an organisation agent through its owner', async () => {
    isTenantAdmin.mockReturnValue(true);
    renderWith('acct-1', [orgAgent, customAgent]);

    const menu = await screen.findByTestId('actions-org-agent');
    fireEvent.click(within(menu).getByText('Who can use it'));
    const modal = await screen.findByTestId('sharing-modal');
    expect(modal).toHaveAttribute('data-item-id', 'agent-org');
    expect(modal).toHaveAttribute('data-noun', 'agent');
    // From an account's list it can be moved back to that account.
    expect(modal).toHaveAttribute('data-target-account', 'acct-1');

    fireEvent.click(within(screen.getByTestId('actions-org-agent')).getByText('Delete Agent'));
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(apiAskNudgebee.deleteAgent).toHaveBeenCalledWith('', 'org-agent'));
  });

  it('lets a tenant admin widen an account agent from the tenant-wide view', async () => {
    isTenantAdmin.mockReturnValue(true);
    renderWith('', [customAgent]);

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Who can use it'));
    expect(await screen.findByTestId('sharing-modal')).toHaveAttribute('data-target-account', 'acct-9');
  });

  it('opens Edit Agent for an organisation agent with no account', async () => {
    isTenantAdmin.mockReturnValue(true);
    renderWith('', [orgAgent]);

    fireEvent.click(within(await screen.findByTestId('actions-org-agent')).getByText('Edit Agent'));
    expect(await screen.findByTestId('create-agent-form')).toHaveAttribute('data-account-id', '');
  });

  it('hides Create and organisation actions from non-tenant-admins in the tenant-wide view', async () => {
    renderWith('', [orgAgent]);
    expect(screen.queryByRole('button', { name: /Create Custom Agent/ })).not.toBeInTheDocument();
    expect(screen.queryByTestId('actions-org-agent')).not.toBeInTheDocument();
  });

  it('gives a tenant admin Create in the tenant-wide view with the organisation scopes only', async () => {
    isTenantAdmin.mockReturnValue(true);
    renderWith('', [orgAgent]);

    fireEvent.click(screen.getByRole('button', { name: /Create Custom Agent/ }));
    const form = await screen.findByTestId('create-agent-form');
    expect(form).toHaveAttribute('data-scopes', 'organisation,selected_accounts');
    expect(form).toHaveAttribute('data-initial-visibility', 'organisation');
  });

  it("offers a tenant admin 'This account' when creating from an account", async () => {
    isTenantAdmin.mockReturnValue(true);
    renderWith('acct-1', [orgAgent]);

    fireEvent.click(screen.getByRole('button', { name: /Create Custom Agent/ }));
    const form = await screen.findByTestId('create-agent-form');
    expect(form).toHaveAttribute('data-scopes', 'account,organisation,selected_accounts');
    // A tenant admin's new agent defaults to the organisation (rule 1); 'This account' is still offered.
    expect(form).toHaveAttribute('data-initial-visibility', 'organisation');
  });
});

describe('ListAgents always-load switch (#40899)', () => {
  const manualKb = { id: 'kb-1', name: 'Runbooks', kb_type: 'manual', format: 'text', content: 'short text' };

  it.each([
    ['a connected source', { ...manualKb, kb_type: 'integration' }, { isSelected: true }, /user-written skills/],
    ['a PDF skill', { ...manualKb, format: 'pdf' }, { isSelected: true }, /PDF/],
    ['an oversized skill', { ...manualKb, content: 'x'.repeat(4001) }, { isSelected: true }, /longer than/],
    ['an unlinked skill', manualKb, { isSelected: false }, /Prioritize this skill/],
  ])('is disabled for %s', (_label, kb, flags, reason) => {
    expect(alwaysLoadDisabledReason(kb, flags)).toMatch(reason);
  });

  it('is enabled for a linked user-written text skill within the cap', () => {
    expect(alwaysLoadDisabledReason({ ...manualKb, content: 'x'.repeat(4000) }, { isSelected: true })).toBe('');
  });

  it('marks an existing link and sends the flag on save', async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue({
      data: [manualKb, { id: 'kb-2', name: 'Confluence', kb_type: 'integration' }],
    });
    apiKnowledgeBase.getAgentKnowledgeBases.mockImplementation((_acct, name) =>
      Promise.resolve({
        data:
          name === '*'
            ? []
            : [
                { id: 'kb-1', always_load: false },
                { id: 'kb-2', always_load: false },
              ],
      })
    );
    apiKnowledgeBase.mapKnowledgeBaseToAgent.mockResolvedValue({});
    renderList('');

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Prioritize knowledge'));

    const runbooks = await screen.findByRole('checkbox', { name: 'Always load Runbooks' });
    await waitFor(() => expect(runbooks).not.toBeDisabled());
    expect(screen.getByRole('checkbox', { name: 'Always load Confluence' })).toBeDisabled();

    fireEvent.click(runbooks);
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));

    await waitFor(() => expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).toHaveBeenCalledWith('acct-9', 'kb-1', 'my-agent', true));
    expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).toHaveBeenCalledTimes(1);
    expect(apiKnowledgeBase.unmapKnowledgeBaseFromAgent).not.toHaveBeenCalled();
  });

  it("marks a skill linked to All agents by adding this agent's own link", async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue({ data: [manualKb] });
    apiKnowledgeBase.getAgentKnowledgeBases.mockImplementation((_acct, name) => Promise.resolve({ data: name === '*' ? [{ id: 'kb-1' }] : [] }));
    apiKnowledgeBase.mapKnowledgeBaseToAgent.mockResolvedValue({});
    renderList('');

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Prioritize knowledge'));

    const runbooks = await screen.findByRole('checkbox', { name: 'Always load Runbooks' });
    await waitFor(() => expect(runbooks).not.toBeDisabled());
    fireEvent.click(runbooks);
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));

    await waitFor(() => expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).toHaveBeenCalledWith('acct-9', 'kb-1', 'my-agent', true));
    expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).not.toHaveBeenCalledWith('acct-9', 'kb-1', '*', true);
  });

  it('shows an existing mark as checked and unmarks it on save', async () => {
    apiKnowledgeBase.getKnowledgeBases.mockResolvedValue({ data: [manualKb] });
    apiKnowledgeBase.getAgentKnowledgeBases.mockImplementation((_acct, name) =>
      Promise.resolve({ data: name === '*' ? [] : [{ id: 'kb-1', always_load: true }] })
    );
    apiKnowledgeBase.mapKnowledgeBaseToAgent.mockResolvedValue({});
    renderList('');

    fireEvent.click(within(await screen.findByTestId('actions-my-agent')).getByText('Prioritize knowledge'));

    const runbooks = await screen.findByRole('checkbox', { name: 'Always load Runbooks' });
    await waitFor(() => expect(runbooks).toBeChecked());
    fireEvent.click(runbooks);
    fireEvent.click(screen.getByRole('button', { name: 'Save Changes' }));

    await waitFor(() => expect(apiKnowledgeBase.mapKnowledgeBaseToAgent).toHaveBeenCalledWith('acct-9', 'kb-1', 'my-agent', false));
  });
});
