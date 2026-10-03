import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import ListAgents from '@components/llm/ListAgents';
import apiAskNudgebee from '@api1/ask-nudgebee';
import apiKnowledgeBase from '@api1/knowledge-base';
import { hasWriteAccess } from '@lib/auth';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { deleteAgent: jest.fn(), listAgentExtensions: jest.fn() },
}));
jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  KB_AGENT_WILDCARD: '*',
  default: {
    getAgentsWithKbCounts: jest.fn(),
    getKnowledgeBases: jest.fn(),
    getAgentKnowledgeBases: jest.fn(),
    mapKnowledgeBaseToAgent: jest.fn(),
    unmapKnowledgeBaseFromAgent: jest.fn(),
  },
}));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn() }));
jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ baseTitle: 'Nudgebee' }),
  useBrandingConfig: () => ({ title: 'Nudgebee' }),
}));
jest.mock('@components/llm/ScopeChip', () => ({ HybridScopeChips: () => null }));
jest.mock('@components/llm/CreateAgentNew', () => ({
  __esModule: true,
  default: ({ accountId }) => <div data-testid='create-agent-form' data-account-id={accountId} />,
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
