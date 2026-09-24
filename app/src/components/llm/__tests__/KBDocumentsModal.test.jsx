import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import KBDocumentsModal from '@components/llm/KBDocumentsModal';
import apiKnowledgeBase from '@api1/knowledge-base';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: { getKBDocuments: jest.fn(), getKBDocument: jest.fn(), setKBDocumentCategory: jest.fn() },
}));

const renderModal = (props = {}) =>
  render(<KBDocumentsModal open onClose={jest.fn()} accountId='acct-1' kbId='kb-1' kbName='Confluence' {...props} />);

beforeEach(() => {
  jest.clearAllMocks();
  apiKnowledgeBase.getKBDocuments.mockResolvedValue({
    data: {
      items: [
        {
          id: 'p1',
          title: 'Runbook: restart auth-svc',
          url: 'https://wiki.example.com/p1',
          document_key: 'integration-a_knowledge_base|101',
          note_category: '',
        },
        {
          id: 'p2',
          title: 'Plain note',
          url: 'https://wiki.example.com/p2',
          document_key: 'integration-a_knowledge_base|102',
          note_category: 'fact',
        },
        // Neither a source id nor a URL: nothing stable to key a mark on.
        { id: 'p4', title: 'Loose page', url: null, document_key: '', note_category: '' },
      ],
      nextOffset: 'p3',
    },
    errors: [],
  });
});

describe('KBDocumentsModal', () => {
  it('lists document titles, linking only those with a url', async () => {
    renderModal();
    expect(await screen.findByRole('link', { name: /Runbook: restart auth-svc/ })).toHaveAttribute('href', 'https://wiki.example.com/p1');
    expect(screen.getByText('Loose page').closest('a')).toBeNull();
    expect(apiKnowledgeBase.getKBDocuments).toHaveBeenCalledWith('acct-1', 'kb-1');
  });

  it('loads content only when the eye button is clicked, and caches it', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({ data: { id: 'p2', title: 'Plain note', content: 'restart with kubectl' }, errors: [] });
    renderModal();

    const view = await screen.findByRole('button', { name: 'View content of Plain note' });
    expect(apiKnowledgeBase.getKBDocument).not.toHaveBeenCalled();

    await user.click(view);
    expect(await screen.findByText(/restart with kubectl/)).toBeInTheDocument();
    expect(apiKnowledgeBase.getKBDocument).toHaveBeenCalledWith('acct-1', 'kb-1', 'p2');

    await user.click(screen.getByRole('button', { name: 'Hide content of Plain note' }));
    await waitFor(() => expect(screen.queryByText(/restart with kubectl/)).not.toBeInTheDocument());
    await user.click(screen.getByRole('button', { name: 'View content of Plain note' }));
    expect(await screen.findByText(/restart with kubectl/)).toBeInTheDocument();
    expect(apiKnowledgeBase.getKBDocument).toHaveBeenCalledTimes(1);
  });

  it('appends the next page on Load more', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText('Plain note');
    apiKnowledgeBase.getKBDocuments.mockResolvedValueOnce({
      data: { items: [{ id: 'p3', title: 'Third page', url: null, document_key: 'Third page', note_category: '' }], nextOffset: null },
      errors: [],
    });

    await user.click(screen.getByTestId('kb-documents-load-more-btn'));
    expect(await screen.findByText('Third page')).toBeInTheDocument();
    expect(apiKnowledgeBase.getKBDocuments).toHaveBeenLastCalledWith('acct-1', 'kb-1', 'p3');
    expect(screen.queryByTestId('kb-documents-load-more-btn')).not.toBeInTheDocument();
  });

  it('does not leave Load more spinning after switching KB mid-request', async () => {
    const user = userEvent.setup();
    const { rerender } = renderModal();
    await screen.findByText('Plain note');
    apiKnowledgeBase.getKBDocuments.mockReturnValueOnce(new Promise(() => {}));
    await user.click(screen.getByTestId('kb-documents-load-more-btn'));
    expect(screen.getByTestId('kb-documents-load-more-btn')).toHaveAttribute('aria-busy', 'true');

    rerender(<KBDocumentsModal open onClose={jest.fn()} accountId='acct-1' kbId='kb-2' kbName='Other' />);
    await screen.findByText('Plain note');
    expect(screen.getByTestId('kb-documents-load-more-btn')).not.toHaveAttribute('aria-busy');
  });

  it('shows each document its stored mark, and none when it has no mark', async () => {
    renderModal({ canEdit: true });
    const marked = await screen.findByRole('group', { name: 'Mark Plain note as a fact or an SOP' });
    expect(within(marked).getByRole('radio', { name: 'Fact' })).toHaveAttribute('aria-checked', 'true');

    const unmarked = screen.getByRole('group', { name: 'Mark Runbook: restart auth-svc as a fact or an SOP' });
    expect(within(unmarked).getByRole('radio', { name: 'Default' })).toHaveAttribute('aria-checked', 'true');
  });

  it('marks a document as an SOP, keyed on document_key rather than the point id', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: { status: 'ok' }, errors: [] });
    renderModal({ canEdit: true });

    const group = await screen.findByRole('group', { name: 'Mark Runbook: restart auth-svc as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'SOP' }));

    expect(apiKnowledgeBase.setKBDocumentCategory).toHaveBeenCalledWith('acct-1', 'kb-1', 'integration-a_knowledge_base|101', 'sop');
    await waitFor(() => expect(within(group).getByRole('radio', { name: 'SOP' })).toHaveAttribute('aria-checked', 'true'));
  });

  it('clears a mark with Default', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: { status: 'ok' }, errors: [] });
    renderModal({ canEdit: true });

    const group = await screen.findByRole('group', { name: 'Mark Plain note as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'Default' }));

    expect(apiKnowledgeBase.setKBDocumentCategory).toHaveBeenCalledWith('acct-1', 'kb-1', 'integration-a_knowledge_base|102', '');
  });

  it('puts the control back and reports the error when the write fails', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: null, errors: [{ message: 'kb: permission denied' }] });
    renderModal({ canEdit: true });

    const group = await screen.findByRole('group', { name: 'Mark Plain note as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'SOP' }));

    expect(await screen.findByText('kb: permission denied')).toBeInTheDocument();
    expect(within(group).getByRole('radio', { name: 'Fact' })).toHaveAttribute('aria-checked', 'true');
  });

  it('shows a read-only viewer the mark without a way to change it', async () => {
    renderModal();
    const row = (await screen.findByText('Plain note')).closest('[data-testid="kb-document-row"]');
    expect(within(row).getByText('Fact')).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: /as a fact or an SOP/ })).not.toBeInTheDocument();
    // The label alone says nothing about what it does, so the legend is shown too.
    expect(screen.getByTestId('kb-document-category-legend')).toHaveTextContent('Select an account to change');
  });

  it('offers no control on a document that cannot be marked', async () => {
    renderModal({ canEdit: true });
    await screen.findByText('Loose page');
    expect(screen.queryByRole('group', { name: 'Mark Loose page as a fact or an SOP' })).not.toBeInTheDocument();
  });

  it('shows the backend error when listing fails', async () => {
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({ data: null, errors: [{ message: 'kb: search index service not configured' }] });
    renderModal();
    expect(await screen.findByText('kb: search index service not configured')).toBeInTheDocument();
  });
});
