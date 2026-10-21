import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import CreateAgentNew from '@components/llm/CreateAgentNew';
import apiAskNudgebee from '@api1/ask-nudgebee';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { listTools: jest.fn(), createAgent: jest.fn(), updateAgent: jest.fn() },
}));
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { listAccounts: jest.fn(() => Promise.resolve([])) },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const TENANT_ADMIN_SCOPES = [
  { value: 'account', label: 'This account' },
  { value: 'organisation', label: 'Organisation' },
  { value: 'selected_accounts', label: 'Selected accounts' },
];

const baseProps = {
  handleClose: jest.fn(),
  allAgents: [],
  editMode: false,
  customizeMode: false,
  agentData: null,
  onSubmitStart: jest.fn(),
  onSubmitEnd: jest.fn(),
};

const fillAndSubmit = (rerender, props) => {
  fireEvent.change(screen.getByPlaceholderText('Enter agent name'), { target: { value: 'shared_agent' } });
  fireEvent.change(screen.getByPlaceholderText('Describe what this agent does'), { target: { value: 'Shared agent' } });
  fireEvent.change(screen.getByPlaceholderText(/Key responsibilities/), { target: { value: 'Help' } });
  rerender(<CreateAgentNew {...baseProps} {...props} triggerSubmit />);
};

beforeAll(() => {
  global.IntersectionObserver = class {
    observe() {}
    disconnect() {}
  };
});

beforeEach(() => {
  jest.clearAllMocks();
  apiAskNudgebee.listTools.mockResolvedValue({ data: { data: { ai_list_tools: { data: [] } } } });
  apiAskNudgebee.createAgent.mockResolvedValue({ data: { data: { ai_create_agent: { data: { id: 'a-1' } } } } });
  apiAskNudgebee.updateAgent.mockResolvedValue({ data: { data: { ai_update_agent: { data: { id: 'a-1' } } } } });
});

describe('CreateAgentNew "Who can use it" (#41073)', () => {
  it('creates an organisation agent from the tenant-wide view with no account and visibility in the agent', async () => {
    const props = { accountId: '', scopeOptions: TENANT_ADMIN_SCOPES.slice(1), initialVisibility: 'organisation' };
    const { rerender } = render(<CreateAgentNew {...baseProps} {...props} triggerSubmit={false} />);

    await waitFor(() => expect(apiAskNudgebee.listTools).toHaveBeenCalledWith({ accountId: '' }));
    fillAndSubmit(rerender, props);

    await waitFor(() => expect(apiAskNudgebee.createAgent).toHaveBeenCalled());
    const payload = apiAskNudgebee.createAgent.mock.calls[0][0];
    expect(payload.account_id).toBe('');
    expect(payload.agent).toEqual(expect.objectContaining({ name: 'shared_agent', visibility: 'organisation', account_ids: [] }));
  });

  it("keeps the account's tool list (so MCP tools stay pickable) when an agent from an account is made organisation-wide", async () => {
    const props = { accountId: 'acct-1', scopeOptions: TENANT_ADMIN_SCOPES, initialVisibility: 'account' };
    const { rerender } = render(<CreateAgentNew {...baseProps} {...props} triggerSubmit={false} />);

    await waitFor(() => expect(apiAskNudgebee.listTools).toHaveBeenCalledWith({ accountId: 'acct-1' }));
    fireEvent.click(screen.getByRole('radio', { name: 'Organisation' }));
    await waitFor(() => expect(apiAskNudgebee.listTools).toHaveBeenLastCalledWith({ accountId: 'acct-1' }));

    fillAndSubmit(rerender, props);
    await waitFor(() => expect(apiAskNudgebee.createAgent).toHaveBeenCalled());
    expect(apiAskNudgebee.createAgent.mock.calls[0][0].account_id).toBe('');
  });

  it('keeps an account agent in its account', async () => {
    const props = { accountId: 'acct-1', scopeOptions: TENANT_ADMIN_SCOPES, initialVisibility: 'account' };
    const { rerender } = render(<CreateAgentNew {...baseProps} {...props} triggerSubmit={false} />);

    fillAndSubmit(rerender, props);
    await waitFor(() => expect(apiAskNudgebee.createAgent).toHaveBeenCalled());
    const payload = apiAskNudgebee.createAgent.mock.calls[0][0];
    expect(payload.account_id).toBe('acct-1');
    expect(payload.agent.visibility).toBe('account');
  });

  it('edits an organisation agent through its owner with the tenant-wide tool list', async () => {
    const agentData = {
      id: 'agent-org',
      name: 'org_agent',
      description: 'Organisation agent',
      status: 'enabled',
      tools: [],
      account_id: '',
      visibility: 'organisation',
      system_prompt: JSON.stringify({ role: '', instructions: ['Help'], constraints: [''], tool_usage: { toolUsage: '' }, examples: '' }),
    };
    const props = { accountId: '', editMode: true, agentData, scopeOptions: TENANT_ADMIN_SCOPES };
    const { rerender } = render(<CreateAgentNew {...baseProps} {...props} triggerSubmit={false} />);

    await waitFor(() => expect(apiAskNudgebee.listTools).toHaveBeenCalledWith({ accountId: '' }));
    expect(screen.queryByTestId('org-scope-field')).not.toBeInTheDocument();
    rerender(<CreateAgentNew {...baseProps} {...props} triggerSubmit />);

    await waitFor(() => expect(apiAskNudgebee.updateAgent).toHaveBeenCalled());
    expect(apiAskNudgebee.updateAgent.mock.calls[0][0].account_id).toBe('');
  });
});
