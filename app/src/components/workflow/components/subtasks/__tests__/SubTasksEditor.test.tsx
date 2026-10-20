import React, { useState } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import SubTasksEditor, { type SubTask } from '../SubTasksEditor';

jest.mock('@api1/account', () => ({
  __esModule: true,
  default: { getNotificationUserList: jest.fn() },
}));

// Dropdown sources for account / namespace fields aren't under test here.
jest.mock('../../../hooks/data-fetchers/useTaskFormData', () => ({
  useTaskFormData: () => ({
    cloudAccounts: [],
    integrations: [],
    namespaces: [],
    notifications: [],
    ticketConfigurations: [],
    resourceTypes: [],
    workloadKinds: [],
  }),
}));

import apiAccount from '@api1/account';

const mockUserList = (apiAccount as any).getNotificationUserList as jest.Mock;

const taskDefinitions = [
  {
    name: 'notifications.dm',
    display_name: 'Send DM',
    description: 'Send a direct message',
    input_schema: {
      provider: { type: 'string', description: 'Notification Provider', required: true, options: ['slack'], order: 1 },
      user_id: { type: 'string', description: 'User ID to send message to', required: true, order: 2 },
      message: { type: 'string', description: 'Message Body', required: true, order: 3 },
    },
  },
  {
    name: 'k8s.get_resource',
    display_name: 'Get Resource',
    description: 'Fetch a Kubernetes resource',
    input_schema: {
      account_id: { type: 'account', required: true, order: 1 },
      output_format: { type: 'string', default: 'json', order: 2 },
    },
  },
];

const Harness: React.FC<{ initial?: SubTask[]; onChangeSpy: (tasks: SubTask[]) => void }> = ({ initial = [], onChangeSpy }) => {
  const [tasks, setTasks] = useState<SubTask[]>(initial);
  return (
    <SubTasksEditor
      value={tasks}
      onChange={(next) => {
        onChangeSpy(next);
        setTasks(next);
      }}
      errors={{}}
      taskDefinitions={taskDefinitions}
      previousTasks={[]}
      workflowInputs={[]}
      workflowConfigs={[]}
      blockedTypes={new Set(['core.foreach'])}
      testIdPrefix='foreach'
      accountId='acct-1'
      copy={{ helperText: 'helper', emptyStateText: 'empty' }}
    />
  );
};

const pickAction = (label: string) => {
  const picker = screen.getByTestId('foreach-subtask-0-type-picker');
  fireEvent.mouseDown(within(picker).getByRole('combobox'));
  fireEvent.click(screen.getByText(label));
};

describe('SubTasksEditor action picker', () => {
  beforeEach(() => {
    mockUserList.mockReset();
    mockUserList.mockResolvedValue({ data: { data: [{ id: 'U1', display_name: 'Alice' }] } });
  });

  it('groups actions under their category header like the Actions tab', () => {
    render(<Harness onChangeSpy={jest.fn()} />);
    fireEvent.click(screen.getByTestId('foreach-empty-add-subtask-btn'));
    const picker = screen.getByTestId('foreach-subtask-0-type-picker');
    fireEvent.mouseDown(within(picker).getByRole('combobox'));
    expect(screen.getAllByTestId(/^foreach-subtask-action-group-/).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText('Send DM')).toBeInTheDocument();
  });

  it('auto-fills defaults and the workflow account when an action is picked', () => {
    const spy = jest.fn();
    render(<Harness onChangeSpy={spy} />);
    fireEvent.click(screen.getByTestId('foreach-empty-add-subtask-btn'));
    pickAction('Get Resource');
    const last = spy.mock.calls[spy.mock.calls.length - 1][0];
    expect(last[0]).toMatchObject({ type: 'k8s.get_resource', params: { account_id: 'acct-1', output_format: 'json' } });
  });

  it('selects the only DM provider and loads its users for the User ID field', async () => {
    const spy = jest.fn();
    render(<Harness onChangeSpy={spy} />);
    fireEvent.click(screen.getByTestId('foreach-empty-add-subtask-btn'));
    pickAction('Send DM');
    const last = spy.mock.calls[spy.mock.calls.length - 1][0];
    expect(last[0].params).toEqual({ provider: 'slack' });
    await waitFor(() => expect(mockUserList).toHaveBeenCalledWith('slack'));
    await act(async () => {});
  });

  it('shows a retryable error instead of "No users available" when the user list fails', async () => {
    mockUserList.mockReset();
    mockUserList.mockRejectedValueOnce(new Error('rate limited')).mockResolvedValue({ data: { data: [{ id: 'U1', display_name: 'Alice' }] } });
    render(<Harness onChangeSpy={jest.fn()} />);
    fireEvent.click(screen.getByTestId('foreach-empty-add-subtask-btn'));
    pickAction('Send DM');

    expect(await screen.findByText('Could not load users. Try again in a moment.')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(mockUserList).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByText('Could not load users. Try again in a moment.')).not.toBeInTheDocument());
  });

  it('deletes without a confirm prompt when params equal the auto-fill in a different key order', () => {
    const spy = jest.fn();
    const initial = [{ id: 'task_1', type: 'k8s.get_resource', params: { output_format: 'json', account_id: 'acct-1' } }];
    render(<Harness initial={initial} onChangeSpy={spy} />);
    fireEvent.click(screen.getByTestId('foreach-subtask-0-delete-btn'));
    expect(screen.queryByText('Delete sub-task?')).not.toBeInTheDocument();
    expect(spy).toHaveBeenLastCalledWith([]);
  });

  it('still confirms delete when the user changed a param', () => {
    const initial = [{ id: 'task_1', type: 'k8s.get_resource', params: { account_id: 'acct-1', output_format: 'yaml' } }];
    render(<Harness initial={initial} onChangeSpy={jest.fn()} />);
    fireEvent.click(screen.getByTestId('foreach-subtask-0-delete-btn'));
    expect(screen.getByText('Delete sub-task?')).toBeInTheDocument();
  });

  it('switches action without a confirm prompt when only auto-filled params exist', async () => {
    const spy = jest.fn();
    render(<Harness onChangeSpy={spy} />);
    fireEvent.click(screen.getByTestId('foreach-empty-add-subtask-btn'));
    pickAction('Get Resource');
    pickAction('Send DM');
    expect(screen.queryByText('Change action?')).not.toBeInTheDocument();
    const last = spy.mock.calls[spy.mock.calls.length - 1][0];
    expect(last[0].type).toBe('notifications.dm');
    await act(async () => {});
  });
});
