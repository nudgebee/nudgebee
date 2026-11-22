import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import AutomationToolsList from '@components/llm/AutomationToolsList';
import { queryGraphQL } from '@lib/HttpService';
import { hasWriteAccess } from '@lib/auth';

jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn() }));
jest.mock('@lib/auth', () => ({ hasWriteAccess: jest.fn() }));
jest.mock('@api1/tool-approval', () => ({ listToolApprovalAccounts: jest.fn(() => Promise.resolve([{ id: 'acct-1', account_name: 'Prod' }])) }));
jest.mock('@ui/Switch', () => ({
  Switch: ({ label, checked, onChange, disabled }) => (
    <label>
      {label}
      <input type='checkbox' aria-label={label} checked={checked} disabled={disabled} onChange={(e) => onChange(e, e.target.checked)} />
    </label>
  ),
}));
jest.mock('@shared/tables/CustomTable', () => ({
  __esModule: true,
  default: ({ tableData }) => (
    <div>
      {tableData.map((row, i) => (
        <div key={i} data-testid='row'>
          {row.map((cell, j) => (
            <span key={j}>{cell.component ?? cell.text}</span>
          ))}
        </div>
      ))}
    </div>
  ),
}));

const listed = (workflows) => ({ data: { data: { workflows_list_ai_settings: { workflows } } } });

const offered = { id: 'w1', name: 'Restart api', account_id: 'acct-1', ai_invocable: true, ai_auto_approve: false, offered: true };
const staleLive = {
  id: 'w2',
  name: 'Collect logs',
  account_id: 'acct-1',
  ai_invocable: true,
  ai_auto_approve: true,
  offered: false,
  not_offered_reason: 'no_llm_description',
};

beforeEach(() => {
  jest.clearAllMocks();
  hasWriteAccess.mockReturnValue(true);
});

// The Automations sub-tab shows what Nubi is offered and lets editors change it (#40399).
describe('AutomationToolsList', () => {
  it('shows the server verdict on each automation and whether it asks', async () => {
    queryGraphQL.mockResolvedValueOnce(listed([offered, staleLive]));
    render(<AutomationToolsList />);

    expect(await screen.findByText('Restart api')).toBeInTheDocument();
    expect(screen.getByText('Offered')).toBeInTheDocument();
    expect(screen.getByText('Not offered')).toBeInTheDocument();
    expect(screen.getByText('Always asks')).toBeInTheDocument();
    expect(screen.getByText('Never asks')).toBeInTheDocument();
    expect(screen.getAllByText('Prod')).toHaveLength(2);
  });

  it('saves both settings, and turning Nubi off turns off running without asking', async () => {
    queryGraphQL.mockResolvedValueOnce(listed([staleLive])).mockResolvedValueOnce({
      data: {
        data: {
          workflows_update_ai_settings: { id: 'w2', ai_invocable: false, ai_auto_approve: false, offered: false, not_offered_reason: 'not_opted_in' },
        },
      },
    });
    render(<AutomationToolsList />);

    fireEvent.click(await screen.findByTestId('automation-ai-manage-w2'));
    fireEvent.click(screen.getByLabelText('Nubi can run it'));
    expect(screen.getByLabelText('Runs without asking')).not.toBeChecked();
    expect(screen.getByLabelText('Runs without asking')).toBeDisabled();
    fireEvent.click(screen.getByTestId('automation-ai-save'));

    await waitFor(() =>
      expect(queryGraphQL).toHaveBeenLastCalledWith(expect.stringContaining('workflows_update_ai_settings'), 'UpdateAutomationAISettings', {
        request: { account_id: 'acct-1', id: 'w2', ai_invocable: false, ai_auto_approve: false },
      })
    );
    // Nubi may no longer run it, so whether it asks is shown as not applying.
    expect(await screen.findByText('—')).toBeInTheDocument();
    expect(screen.queryByText('Never asks')).not.toBeInTheDocument();
  });

  it('offers Manage only on automations the viewer can edit', async () => {
    hasWriteAccess.mockReturnValue(false);
    queryGraphQL.mockResolvedValueOnce(listed([offered]));
    render(<AutomationToolsList />);

    expect(await screen.findByText('Restart api')).toBeInTheDocument();
    expect(screen.queryByText('Manage')).not.toBeInTheDocument();
  });
});
