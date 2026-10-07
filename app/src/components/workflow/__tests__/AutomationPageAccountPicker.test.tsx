import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import WorkflowPage from '@pages/automation/[workflowId]';
import apiWorkflow from '@api1/workflow';

// #40216: /automation/<id> without ?accountId= used to spin forever on "Loading Automation...".
const replace = jest.fn();
let query: Record<string, string> = {};
jest.mock('next/router', () => ({
  useRouter: () => ({ isReady: true, pathname: '/automation/[workflowId]', query, replace }),
}));
jest.mock('next/dynamic', () => () => () => <div>builder</div>);
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => true, hasPermission: () => true }));
jest.mock('@api1/workflow', () => ({ __esModule: true, default: { getWorkflowAccount: jest.fn() } }));
jest.mock('@components/common/AccountPickerModal', () => ({
  __esModule: true,
  default: ({ onSelect, onCancel }: { onSelect: (id: string) => void; onCancel: () => void }) => (
    <div>
      <button onClick={() => onSelect('acc-7')}>pick</button>
      <button onClick={onCancel}>cancel</button>
    </div>
  ),
}));

const resolveAccount = apiWorkflow.getWorkflowAccount as jest.Mock;

describe('automation page opened without an account in the URL', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    window.location.hash = '';
  });

  it('opens the automation in its own account, keeping the rest of the link', async () => {
    query = { workflowId: 'wf-1', executionId: 'ex-9' };
    window.location.hash = '#executions';
    resolveAccount.mockResolvedValue({ data: { workflow_get_account: { id: 'wf-1', account_id: 'acc-3' } } });

    render(<WorkflowPage />);

    await waitFor(() =>
      expect(replace).toHaveBeenCalledWith({
        pathname: '/automation/[workflowId]',
        query: { workflowId: 'wf-1', executionId: 'ex-9', accountId: 'acc-3' },
        hash: 'executions',
      })
    );
    expect(resolveAccount).toHaveBeenCalledWith('wf-1');
    expect(screen.queryByText('builder')).not.toBeInTheDocument();
  });

  it('says the automation was not found when the lookup answers 404', async () => {
    query = { workflowId: 'wf-1' };
    resolveAccount.mockResolvedValue({
      data: { workflow_get_account: null },
      errors: [{ message: 'workflow not found', extensions: { upstream: { status: 404 } } }],
    });

    render(<WorkflowPage />);

    expect(await screen.findByText('Automation not found')).toBeInTheDocument();
    expect(screen.queryByText('pick')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Go to Automations'));
    expect(replace).toHaveBeenCalledWith('/automation');
  });

  it('falls back to asking for the account when the lookup fails', async () => {
    query = { workflowId: 'wf-1', executionId: 'ex-9' };
    window.location.hash = '#executions';
    resolveAccount.mockResolvedValue({ data: null, errors: [{ message: 'upstream returned 502', extensions: { upstream: { status: 502 } } }] });

    render(<WorkflowPage />);
    fireEvent.click(await screen.findByText('pick'));

    expect(replace).toHaveBeenCalledWith({
      pathname: '/automation/[workflowId]',
      query: { workflowId: 'wf-1', executionId: 'ex-9', accountId: 'acc-7' },
      hash: 'executions',
    });
  });

  it('goes back to the automations list when the picker is closed', async () => {
    query = { workflowId: 'wf-1' };
    resolveAccount.mockResolvedValue({ data: null, errors: [{ message: 'network' }] });

    render(<WorkflowPage />);
    fireEvent.click(await screen.findByText('cancel'));

    expect(replace).toHaveBeenCalledWith('/automation');
  });

  it('opens the builder straight away when the URL names the account', () => {
    query = { workflowId: 'wf-1', accountId: 'acc-7' };

    render(<WorkflowPage />);

    expect(screen.getByText('builder')).toBeInTheDocument();
    expect(resolveAccount).not.toHaveBeenCalled();
  });
});
