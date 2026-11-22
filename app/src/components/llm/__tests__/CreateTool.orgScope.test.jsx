import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import CreateTool from '@components/llm/CreateTool';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { toast as snackbar } from '@ui/Toast';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { createTool: jest.fn(), updateTool: jest.fn() },
}));
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { listAccounts: jest.fn(() => Promise.resolve([{ id: 'acct-2', account_name: 'Staging', cloud_provider: 'AWS' }])) },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const TENANT_ADMIN_SCOPES = [
  { value: 'account', label: 'This account' },
  { value: 'organisation', label: 'Organisation' },
  { value: 'selected_accounts', label: 'Selected accounts' },
];

const fill = () => {
  fireEvent.change(screen.getByPlaceholderText('Enter tool name'), { target: { value: 'shared_tool' } });
  fireEvent.change(screen.getByPlaceholderText('Describe what this tool does'), { target: { value: 'A shared tool' } });
  fireEvent.change(screen.getByPlaceholderText('e.g., alpine:latest or myrepo/myimage:tag'), { target: { value: 'alpine:latest' } });
};

const renderForm = (props = {}) => render(<CreateTool accountId='acct-1' handleClose={jest.fn()} allTools={[]} {...props} />);

beforeEach(() => {
  jest.clearAllMocks();
  apiAskNudgebee.createTool.mockResolvedValue({ data: { data: { ai_create_tool: { data: { id: 'tool-1' } } } } });
});

describe('CreateTool "Who can use it" (#41073)', () => {
  it('has no scope field without scope options (account admins)', () => {
    renderForm();
    expect(screen.queryByTestId('org-scope-field')).not.toBeInTheDocument();
  });

  it('creates an organisation tool with no account and visibility in the tool', async () => {
    renderForm({ scopeOptions: TENANT_ADMIN_SCOPES });
    fill();
    fireEvent.click(screen.getByRole('radio', { name: 'Organisation' }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() => expect(apiAskNudgebee.createTool).toHaveBeenCalled());
    const payload = apiAskNudgebee.createTool.mock.calls[0][0];
    expect(payload.account_id).toBe('');
    expect(payload.tool).toEqual(expect.objectContaining({ name: 'shared_tool', visibility: 'organisation', account_ids: [] }));
  });

  it('keeps an account tool in its account', async () => {
    renderForm({ scopeOptions: TENANT_ADMIN_SCOPES });
    fill();
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() => expect(apiAskNudgebee.createTool).toHaveBeenCalled());
    const payload = apiAskNudgebee.createTool.mock.calls[0][0];
    expect(payload.account_id).toBe('acct-1');
    expect(payload.tool.visibility).toBe('account');
  });

  it('asks for at least one account for "Selected accounts"', async () => {
    renderForm({ scopeOptions: TENANT_ADMIN_SCOPES });
    fill();
    fireEvent.click(screen.getByRole('radio', { name: 'Selected accounts' }));
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    expect(snackbar.error).toHaveBeenCalledWith('Pick at least one account to share this tool with');
    expect(apiAskNudgebee.createTool).not.toHaveBeenCalled();
  });

  it("surfaces the backend's reason when the name is taken in another account", async () => {
    apiAskNudgebee.createTool.mockResolvedValue({
      data: { errors: [{ message: 'tool "shared_tool" already exists in account Prod; rename it first' }] },
    });
    renderForm({ scopeOptions: TENANT_ADMIN_SCOPES, initialVisibility: 'organisation' });
    fill();
    fireEvent.click(screen.getByRole('button', { name: 'Submit' }));

    await waitFor(() =>
      expect(snackbar.error).toHaveBeenCalledWith('Failed to create tool - tool "shared_tool" already exists in account Prod; rename it first')
    );
  });
});
