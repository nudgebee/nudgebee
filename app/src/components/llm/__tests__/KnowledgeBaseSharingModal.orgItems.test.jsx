import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KnowledgeBaseSharingModal from '@components/llm/KnowledgeBaseSharingModal';
import apiKnowledgeBase from '@api1/knowledge-base';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: { updateKnowledgeBaseVisibility: jest.fn(), listKnowledgeBaseAccountOverrides: jest.fn() },
}));
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { listAccounts: jest.fn(() => Promise.resolve([])) },
}));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

beforeEach(() => {
  jest.clearAllMocks();
  apiKnowledgeBase.updateKnowledgeBaseVisibility.mockResolvedValue({ data: {}, errors: [] });
});

describe('KnowledgeBaseSharingModal', () => {
  it('still drives a knowledge base by default, with the organisation scopes only', async () => {
    const onSaved = jest.fn();
    render(
      <KnowledgeBaseSharingModal
        open
        onClose={jest.fn()}
        knowledgeBase={{ id: 'kb-1', name: 'Docs', visibility: 'selected_accounts' }}
        onSaved={onSaved}
      />
    );

    expect(screen.queryByRole('radio', { name: 'This account' })).not.toBeInTheDocument();
    expect(screen.getByText('Accounts that use "Docs" in their answers.')).toBeInTheDocument();
    await waitFor(() => expect(apiKnowledgeBase.listKnowledgeBaseAccountOverrides).toHaveBeenCalledWith('kb-1'));
    fireEvent.click(screen.getByRole('radio', { name: 'Organisation' }));
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(apiKnowledgeBase.updateKnowledgeBaseVisibility).toHaveBeenCalledWith('kb-1', 'organisation', [], ''));
    expect(onSaved).toHaveBeenCalled();
  });

  it('moves an organisation agent back to one account with the given calls', async () => {
    const saveVisibility = jest.fn().mockResolvedValue({ data: {}, errors: [] });
    const listOverrides = jest.fn().mockResolvedValue({ data: [], errors: [] });
    render(
      <KnowledgeBaseSharingModal
        open
        onClose={jest.fn()}
        item={{ id: 'agent-org', name: 'Org agent', visibility: 'organisation' }}
        noun='agent'
        saveVisibility={saveVisibility}
        listOverrides={listOverrides}
        accountOption={{ label: 'This account', targetAccountId: 'acct-1' }}
        testId='agent-sharing-modal'
      />
    );

    expect(screen.getByRole('group', { name: 'Who can use this agent' })).toBeInTheDocument();
    expect(screen.getByText('Accounts that can use "Org agent".')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', { name: 'This account' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() => expect(saveVisibility).toHaveBeenCalledWith('agent-org', 'account', [], 'acct-1'));
    expect(apiKnowledgeBase.updateKnowledgeBaseVisibility).not.toHaveBeenCalled();
  });

  it('says the selected accounts are loading until they arrive', async () => {
    let resolve;
    const listOverrides = jest.fn(() => new Promise((r) => (resolve = r)));
    render(
      <KnowledgeBaseSharingModal
        open
        onClose={jest.fn()}
        item={{ id: 'tool-sel', name: 'Sel tool', visibility: 'selected_accounts' }}
        noun='tool'
        saveVisibility={jest.fn()}
        listOverrides={listOverrides}
        testId='tool-sharing-modal'
      />
    );

    expect(await screen.findByText('Loading accounts…')).toBeInTheDocument();
    resolve({ data: [], errors: [] });
    await waitFor(() => expect(screen.queryByText('Loading accounts…')).not.toBeInTheDocument());
  });
});
