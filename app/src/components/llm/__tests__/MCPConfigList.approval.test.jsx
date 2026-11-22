import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import MCPConfigList from '@components/llm/MCPConfigList';
import apiIntegrations from '@api1/integrations';
import { isTenantAdmin } from '@lib/auth';

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: { listIntegrations: jest.fn(), updateMcpWriteTools: jest.fn() },
}));
jest.mock('@api1/integrations/helpers', () => ({ parseIntegrationItem: (item) => item }));
jest.mock('@lib/auth', () => ({ isTenantAdmin: jest.fn(), canManage: jest.fn(() => false) }));
jest.mock('@components/common/forms/McpWriteToolsField', () => {
  const actual = jest.requireActual('@components/common/forms/McpWriteToolsField');
  return {
    __esModule: true,
    parseToolNames: actual.parseToolNames,
    default: ({ value, onChange }) => <input data-testid='write-tools-input' value={value} onChange={(e) => onChange(e.target.value)} />,
  };
});
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ headers, tableData }) => (
    <div>
      {headers.map((h) => (
        <span key={h} data-testid='header'>
          {h}
        </span>
      ))}
      {tableData.map((row, i) => (
        <div key={i} data-testid={`row-${i}`}>
          {row.map((cell, j) => (
            <span key={j}>{cell.component ?? cell.text}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const server = {
  id: 'integration-1',
  name: 'jira',
  status: 'enabled',
  integration_config_values: [{ name: 'write_tools', value: 'create_issue,delete_issue' }],
  integrations_cloud_accounts: [{ cloud_account_id: 'acct-1', cloud_account_name: 'Prod' }],
};

beforeEach(() => {
  jest.clearAllMocks();
  apiIntegrations.listIntegrations.mockResolvedValue({
    data: { data: { integrations_list: { rows: [server] }, integrations_aggregate: { rows: [{ count: 1 }] } } },
  });
  apiIntegrations.updateMcpWriteTools.mockResolvedValue({ data: { data: { integrations_update_mcp_write_tools: { status: 'success' } } } });
});

// The MCP Servers view shows and sets which tools need approval (#40399).
describe('MCPConfigList tools that need approval', () => {
  it('shows how many tools need approval, after Status so it keeps its column', async () => {
    isTenantAdmin.mockReturnValue(false);
    render(<MCPConfigList />);

    expect(await screen.findByText('2 tools')).toBeInTheDocument();
    expect(screen.getAllByTestId('header').map((h) => h.textContent)).toEqual([
      'Name',
      'Connection',
      'Account',
      'Created By',
      'Status',
      'Health',
      'Needs approval',
    ]);
    expect(screen.queryByText('Manage')).not.toBeInTheDocument();
  });

  it('lets a tenant admin change them and saves only that setting', async () => {
    isTenantAdmin.mockReturnValue(true);
    render(<MCPConfigList />);

    fireEvent.click(await screen.findByText('Manage'));
    fireEvent.change(screen.getByTestId('write-tools-input'), { target: { value: 'create_issue' } });
    fireEvent.click(screen.getByText('Save'));

    await waitFor(() => expect(apiIntegrations.updateMcpWriteTools).toHaveBeenCalledWith('integration-1', 'create_issue'));
  });
});
