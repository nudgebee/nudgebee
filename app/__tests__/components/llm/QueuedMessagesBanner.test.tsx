import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import QueuedMessagesBanner from '@components/llm/common/QueuedMessagesBanner';

jest.mock('@utils/colors');

describe('QueuedMessagesBanner', () => {
  const defaultMessages = [
    {
      id: 'qm-1',
      text: 'close open PR 803 and 804',
      createdAt: 1000,
    },
    {
      id: 'qm-2',
      text: 'check deployment logs',
      images: [{ data: 'test', mime_type: 'image/png' }],
      createdAt: 2000,
    },
  ];

  it('shows delete progress until the request settles and blocks duplicate actions', async () => {
    let finish!: () => void;
    const onDelete = jest.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        })
    );
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={onDelete}
      />
    );
    fireEvent.click(screen.getByTestId('queued-message-delete-qm-1'));
    expect(screen.getByRole('status')).toHaveTextContent('Deleting…');
    expect(screen.getByTestId('queued-message-item-qm-1')).toBeInTheDocument();
    for (const id of ['qm-1', 'qm-2']) {
      for (const action of ['edit', 'delete', 'send-now']) expect(screen.getByTestId(`queued-message-${action}-${id}`)).toBeDisabled();
    }
    fireEvent.click(screen.getByTestId('queued-message-delete-qm-1'));
    expect(onDelete).toHaveBeenCalledTimes(1);
    await act(async () => {
      finish();
    });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.getByTestId('queued-message-delete-qm-1')).toBeEnabled();
  });

  it('renders nothing when queuedMessages is empty', () => {
    const { container } = render(
      <QueuedMessagesBanner
        queuedMessages={[]}
        isExpanded={true}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders header with count and subtitle', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={true}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    expect(screen.getByText('Queued')).toBeInTheDocument();
    expect(screen.getByTestId('queued-messages-count')).toHaveTextContent('2');
    expect(screen.getByText('Runs after the current response')).toBeInTheDocument();
  });

  it('calls onToggleExpanded when header is clicked', () => {
    const onToggleExpanded = jest.fn();
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={true}
        onToggleExpanded={onToggleExpanded}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    fireEvent.click(screen.getByTestId('queued-messages-header'));
    expect(onToggleExpanded).toHaveBeenCalledTimes(1);
  });

  it('renders items when isExpanded is true', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={true}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    expect(screen.getByText('close open PR 803 and 804')).toBeInTheDocument();
    expect(screen.getByText('check deployment logs')).toBeInTheDocument();
  });

  it('does not render items when isExpanded is false', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={false}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    expect(screen.queryByText('close open PR 803 and 804')).not.toBeInTheDocument();
  });

  it('triggers onSendNow and onDelete with message id', async () => {
    const onSendNow = jest.fn();
    const onDelete = jest.fn();

    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={true}
        onToggleExpanded={jest.fn()}
        onSendNow={onSendNow}
        onUpdate={jest.fn()}
        onDelete={onDelete}
      />
    );

    await act(async () => {
      fireEvent.click(screen.getByTestId('queued-message-send-now-qm-1'));
    });
    expect(onSendNow).toHaveBeenCalledWith('qm-1');

    fireEvent.click(screen.getByTestId('queued-message-delete-qm-2'));
    expect(onDelete).toHaveBeenCalledWith('qm-2');
  });

  it('edits a message in place: Enter saves the new text, other rows stay locked meanwhile', async () => {
    let finish!: () => void;
    const onUpdate = jest.fn(() => new Promise<void>((resolve) => (finish = resolve)));
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={onUpdate}
        onDelete={jest.fn()}
      />
    );
    fireEvent.click(screen.getByTestId('queued-message-edit-qm-1'));
    const editor = screen.getByTestId('queued-message-editor-qm-1');
    expect(editor).toHaveValue('close open PR 803 and 804');
    for (const action of ['edit', 'delete', 'send-now']) expect(screen.getByTestId(`queued-message-${action}-qm-2`)).toBeDisabled();

    fireEvent.change(editor, { target: { value: 'close open PR 803' } });
    fireEvent.keyDown(editor, { key: 'Enter' });
    expect(onUpdate).toHaveBeenCalledWith('qm-1', 'close open PR 803');
    await act(async () => finish());
    expect(screen.queryByTestId('queued-message-editor-qm-1')).not.toBeInTheDocument();
    expect(screen.getByTestId('queued-message-edit-qm-2')).toBeEnabled();
  });

  it('closes without saving when the text is unchanged', () => {
    const onUpdate = jest.fn();
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={onUpdate}
        onDelete={jest.fn()}
      />
    );
    fireEvent.click(screen.getByTestId('queued-message-edit-qm-1'));
    fireEvent.keyDown(screen.getByTestId('queued-message-editor-qm-1'), { key: 'Enter' });
    expect(onUpdate).not.toHaveBeenCalled();
    expect(screen.queryByTestId('queued-message-editor-qm-1')).not.toBeInTheDocument();
  });

  it('keeps the editor open with the error when saving fails, and Escape cancels', async () => {
    const onUpdate = jest.fn().mockRejectedValue(new Error('This message already started running.'));
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={onUpdate}
        onDelete={jest.fn()}
      />
    );
    fireEvent.click(screen.getByTestId('queued-message-edit-qm-1'));
    fireEvent.change(screen.getByTestId('queued-message-editor-qm-1'), { target: { value: 'close open PR 804' } });
    await act(async () => {
      fireEvent.click(screen.getByTestId('queued-message-edit-save-qm-1'));
    });
    expect(screen.getByText('This message already started running.')).toBeInTheDocument();
    const editor = screen.getByTestId('queued-message-editor-qm-1');
    fireEvent.keyDown(editor, { key: 'Escape' });
    expect(screen.queryByTestId('queued-message-editor-qm-1')).not.toBeInTheDocument();
    expect(screen.getByText('close open PR 803 and 804')).toBeInTheDocument();
  });

  it('renders author badge when user is a team member', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={[
          {
            id: 'qm-3',
            text: 'team member follow-up',
            user: 'Alice',
            createdAt: 3000,
          },
        ]}
        isExpanded={true}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    expect(screen.getByText('Alice')).toBeInTheDocument();
    expect(screen.getByText('team member follow-up')).toBeInTheDocument();
  });

  it('renders capacity when maxQueuedMessages is provided', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={true}
        maxQueuedMessages={5}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    expect(screen.getByTestId('queued-messages-count')).toHaveTextContent('2/5');
    expect(screen.getByText('Runs after the current response')).toBeInTheDocument();
  });

  it('shows saving and unconfirmed rows without queue actions', () => {
    const onRefresh = jest.fn();
    render(
      <QueuedMessagesBanner
        queuedMessages={[
          { id: 'qm-saving', text: 'still saving', createdAt: 1, saving: true },
          { id: 'qm-failed', text: 'not confirmed', createdAt: 2, saveError: 'Couldn’t confirm saving—refresh to check.' },
        ]}
        isExpanded
        onRefresh={onRefresh}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );
    expect(screen.getByRole('status')).toHaveTextContent('Saving…');
    expect(screen.getByRole('alert')).toHaveTextContent('Couldn’t confirm saving');
    expect(screen.getByText('1 needs attention')).toBeInTheDocument();
    expect(screen.queryByTestId('queued-message-send-now-qm-saving')).not.toBeInTheDocument();
    expect(screen.queryByTestId('queued-message-edit-qm-failed')).not.toBeInTheDocument();
    fireEvent.click(screen.getByText('Refresh history'));
    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('blocks other queue actions while Send now is in flight', async () => {
    let finish!: () => void;
    const onSendNow = jest.fn(() => new Promise<void>((resolve) => (finish = resolve)));
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded
        onToggleExpanded={jest.fn()}
        onSendNow={onSendNow}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );
    fireEvent.click(screen.getByTestId('queued-message-send-now-qm-1'));
    for (const action of ['edit', 'delete', 'send-now']) expect(screen.getByTestId(`queued-message-${action}-qm-2`)).toBeDisabled();
    fireEvent.click(screen.getByTestId('queued-message-send-now-qm-1'));
    expect(onSendNow).toHaveBeenCalledTimes(1);
    await act(async () => finish());
    expect(screen.getByTestId('queued-message-send-now-qm-2')).toBeEnabled();
  });

  it('says the queue is paused when the conversation ended without completing', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded
        isPaused
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );
    expect(screen.getByText(/Paused: nothing is running/)).toBeInTheDocument();
  });

  it('renders full capacity warning when queue is at or above limit', () => {
    render(
      <QueuedMessagesBanner
        queuedMessages={defaultMessages}
        isExpanded={true}
        maxQueuedMessages={2}
        onToggleExpanded={jest.fn()}
        onSendNow={jest.fn()}
        onUpdate={jest.fn()}
        onDelete={jest.fn()}
      />
    );

    expect(screen.getByTestId('queued-messages-count')).toHaveTextContent('2/2');
    expect(screen.getByText(/Queue full/i)).toBeInTheDocument();
  });
});
