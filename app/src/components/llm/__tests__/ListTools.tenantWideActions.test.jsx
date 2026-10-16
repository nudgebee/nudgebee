import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import ListTools from '@components/llm/ListTools';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { hasWriteAccess } from '@lib/auth';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { listTools: jest.fn(), deleteTool: jest.fn() },
}));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn() }));
jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: () => ({ baseTitle: 'Nudgebee' }),
  useBrandingConfig: () => ({ title: 'Nudgebee' }),
}));
jest.mock('@components/llm/ScopeChip', () => ({ __esModule: true, default: () => null, HybridScopeChips: () => null }));
jest.mock('@components/llm/CreateTool', () => ({
  __esModule: true,
  default: ({ accountId }) => <div data-testid='create-tool-form' data-account-id={accountId} />,
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
    expect(apiAskNudgebee.listTools).toHaveBeenCalledWith({ accountId: '', nbToolType: 'tool' });
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
