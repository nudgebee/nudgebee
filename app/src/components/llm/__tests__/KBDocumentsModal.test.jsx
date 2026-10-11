import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import KBDocumentsModal from '@components/llm/KBDocumentsModal';
import apiKnowledgeBase from '@api1/knowledge-base';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: { getKBDocuments: jest.fn(), getKBDocument: jest.fn(), retriggerKB: jest.fn(), setKBDocumentCategory: jest.fn() },
}));

const renderModal = (props = {}) =>
  render(<KBDocumentsModal open onClose={jest.fn()} accountId='acct-1' kbId='kb-1' kbName='Confluence' {...props} />);

// jsdom implements neither of these; the component needs them to show a stored PDF.
beforeAll(() => {
  global.URL.createObjectURL = jest.fn(() => 'blob:mock-pdf');
  global.URL.revokeObjectURL = jest.fn();
});

beforeEach(() => {
  jest.clearAllMocks();
  apiKnowledgeBase.getKBDocuments.mockResolvedValue({
    data: {
      items: [
        { id: 'p1', title: 'Runbook: restart auth-svc', url: 'https://wiki.example.com/p1' },
        { id: 'p2', title: 'Plain note', url: null },
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
    expect(screen.getByText('Plain note').closest('a')).toBeNull();
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

  it('renders a scraped page as HTML, not as a wall of tags', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: {
        id: 'p2',
        title: 'Plain note',
        content: '<ul><li><p>Auto-apply Migrations</p></li></ul>',
        content_type: 'text/html',
      },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    const rendered = await screen.findByTestId('kb-document-html');
    expect(rendered).toBeInTheDocument();
    // The text survives; the markup around it does not reach the reader.
    expect(rendered).toHaveTextContent('Auto-apply Migrations');
    expect(rendered.querySelector('li')).not.toBeNull();
    expect(rendered.textContent).not.toContain('<ul>');
  });

  it('strips anything executable out of a scraped page', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: {
        id: 'p2',
        title: 'Plain note',
        content: '<p>safe</p><script>window.pwned = 1</script><img src=x onerror="window.pwned=1">',
        content_type: 'text/html',
      },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    const rendered = await screen.findByTestId('kb-document-html');
    expect(rendered).toHaveTextContent('safe');
    expect(rendered.querySelector('script')).toBeNull();
    expect(rendered.querySelector('img')?.getAttribute('onerror')).toBeNull();
  });

  it('says so when a scraped page keeps its picture on a site we cannot sign in to', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: {
        id: 'p2',
        title: 'Plain note',
        content: '<p>Follow the matrix.</p><img src="https://wiki.example.com/download/attachments/1/m.png" alt="escalation matrix">',
        content_type: 'text/html',
      },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    const rendered = await screen.findByTestId('kb-document-html');
    const img = rendered.querySelector('img');
    expect(img).not.toBeNull();

    // jsdom never fetches, so the failure the reader would see is raised directly.
    act(() => {
      fireEvent.error(img);
    });

    const note = await screen.findByTestId('kb-document-image-unavailable');
    // The alt text is kept: it is often the only description of the picture the page has.
    expect(note).toHaveTextContent('Image unavailable — escalation matrix.');
    expect(rendered.querySelector('img')).toBeNull();
    // The rest of the page is untouched.
    expect(rendered).toHaveTextContent('Follow the matrix.');
    // This document has no url, so its title is plain text — do not send the reader
    // to click something that is not a link.
    expect(note.textContent).not.toContain('Open the page');
  });

  it('points the reader at the title when the page can be opened at its source', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: {
        id: 'p1',
        title: 'Runbook: restart auth-svc',
        content: '<img src="https://wiki.example.com/download/attachments/1/m.png" alt="escalation matrix">',
        content_type: 'text/html',
      },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Runbook: restart auth-svc' }));

    const rendered = await screen.findByTestId('kb-document-html');
    act(() => {
      fireEvent.error(rendered.querySelector('img'));
    });

    expect(await screen.findByTestId('kb-document-image-unavailable')).toHaveTextContent(
      'Image unavailable — escalation matrix. Open the page from its title above to see it.'
    );
  });

  it('leaves a picture alone until it actually fails', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: {
        id: 'p2',
        title: 'Plain note',
        content: '<img src="https://wiki.example.com/ok.png" alt="diagram">',
        content_type: 'text/html',
      },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    const rendered = await screen.findByTestId('kb-document-html');
    expect(rendered.querySelector('img')).not.toBeNull();
    expect(screen.queryByTestId('kb-document-image-unavailable')).toBeNull();
  });

  it('renders a typed note as markdown', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: { id: 'p2', title: 'Plain note', content: '## Steps\n- drain the node', content_type: 'text/markdown' },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    expect(await screen.findByTestId('kb-document-markdown')).toBeInTheDocument();
  });

  it('says so when nothing is indexed, and offers to rebuild it on the same line', async () => {
    // A knowledge base can keep its document_count after the index is dropped, so the
    // count alone would have the reader believe documents exist when none do.
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: { items: [{ id: 'kb-1', title: 'sop.pdf', url: null }], nextOffset: null, indexed: false },
      errors: [],
    });
    apiKnowledgeBase.retriggerKB.mockResolvedValue({ data: { status: 'queued' }, errors: [] });
    const user = userEvent.setup();
    renderModal();

    expect(await screen.findByTestId('kb-not-indexed')).toBeInTheDocument();
    // Named the same as the three-dots menu item, because it is the same operation.
    await user.click(screen.getByRole('button', { name: 'Retrigger' }));
    expect(apiKnowledgeBase.retriggerKB).toHaveBeenCalledWith('acct-1', 'kb-1');
  });

  it('shows the PDF viewer and nothing else', async () => {
    // A `//` line written inside JSX children is not a comment, it is text, and it
    // shipped to every reader of a PDF knowledge base. Assert the view carries the
    // document and the download link, and no stray source text.
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: { items: [{ id: 'kb-1', title: 'sop.pdf', url: null }], nextOffset: null, indexed: true },
      errors: [],
    });
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: { id: 'kb-1', title: 'sop.pdf', content: 'JVBERi0xLjQK', content_type: 'application/pdf' },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of sop.pdf' }));

    const view = await screen.findByTestId('kb-document-pdf');
    expect(view).not.toHaveTextContent('//');
    expect(view).toHaveTextContent('Download sop.pdf');
  });

  it("sends a stored page's own links to a new tab", async () => {
    // The page came from someone's wiki and its links point back there. In the same
    // tab they take the reader out of the app; rel keeps the opened tab from
    // reaching back through window.opener.
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: {
        id: 'p2',
        title: 'Plain note',
        content: '<p>See <a href="https://wiki.example.com/page">the page</a></p>',
        content_type: 'text/html',
      },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    const link = (await screen.findByTestId('kb-document-html')).querySelector('a');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('can be retried after the call throws', async () => {
    // The re-entry guard is a ref, so a throw that skipped its release would not stall
    // one click — it would disable the button for the life of the component.
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: { items: [{ id: 'kb-1', title: 'sop.pdf', url: null }], nextOffset: null, indexed: false },
      errors: [],
    });
    apiKnowledgeBase.retriggerKB.mockRejectedValueOnce(new Error('network down')).mockResolvedValue({ data: {}, errors: [] });
    jest.spyOn(console, 'error').mockImplementation(() => {});
    const user = userEvent.setup();
    renderModal();

    expect(await screen.findByTestId('kb-not-indexed')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Retrigger' }));
    await screen.findByText('Failed to start reindexing');

    await user.click(screen.getByRole('button', { name: 'Retrigger' }));
    expect(apiKnowledgeBase.retriggerKB).toHaveBeenCalledTimes(2);
    console.error.mockRestore();
  });

  it('rebuilds once however fast the button is clicked', async () => {
    // The banner's action has no disabled state, so nothing but the handler stops a
    // second click from dropping and rebuilding the collection a second time.
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: { items: [{ id: 'kb-1', title: 'sop.pdf', url: null }], nextOffset: null, indexed: false },
      errors: [],
    });
    let release;
    apiKnowledgeBase.retriggerKB.mockReturnValue(
      new Promise((resolve) => {
        release = resolve;
      })
    );
    const user = userEvent.setup();
    renderModal();

    expect(await screen.findByTestId('kb-not-indexed')).toBeInTheDocument();
    const button = screen.getByRole('button', { name: 'Retrigger' });
    await user.click(button);
    await user.click(screen.getByRole('button', { name: 'Retriggering…' }));

    expect(apiKnowledgeBase.retriggerKB).toHaveBeenCalledTimes(1);
    await act(async () => release({ data: { status: 'queued' }, errors: [] }));
  });

  it('shows no warning when the index is healthy', async () => {
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: { items: [{ id: 'p1', title: 'A page', url: null }], nextOffset: null, indexed: true },
      errors: [],
    });
    renderModal();
    expect(await screen.findByText('A page')).toBeInTheDocument();
    expect(screen.queryByTestId('kb-not-indexed')).not.toBeInTheDocument();
  });

  it('renders an uploaded PDF from its stored file', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocument.mockResolvedValue({
      data: { id: 'p2', title: 'sop.pdf', content: 'JVBERi0xLjQK', content_type: 'application/pdf' },
      errors: [],
    });
    renderModal();
    await user.click(await screen.findByRole('button', { name: 'View content of Plain note' }));

    expect(await screen.findByTestId('kb-document-pdf')).toBeInTheDocument();
  });

  it('appends the next page on Load more', async () => {
    const user = userEvent.setup();
    renderModal();
    await screen.findByText('Plain note');
    apiKnowledgeBase.getKBDocuments.mockResolvedValueOnce({
      data: { items: [{ id: 'p3', title: 'Third page', url: null }], nextOffset: null },
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

  it('shows the backend error when listing fails', async () => {
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({ data: null, errors: [{ message: 'kb: search index service not configured' }] });
    renderModal();
    expect(await screen.findByText('kb: search index service not configured')).toBeInTheDocument();
  });
});

describe('KBDocumentsModal Fact/SOP marks', () => {
  const RUNBOOK_KEY = 'integration-a_knowledge_base|101';
  const GUIDE_KEY = 'integration-a_knowledge_base|102';

  beforeEach(() => {
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: {
        items: [
          { id: 'p1', title: 'Runbook: restart auth-svc', url: 'https://wiki.example.com/p1', document_key: RUNBOOK_KEY, note_category: '' },
          { id: 'p2', title: 'Escalation guide', url: 'https://wiki.example.com/p2', document_key: GUIDE_KEY, note_category: 'fact' },
          // Neither a source id nor a URL: nothing stable to key a mark on.
          { id: 'p3', title: 'Loose page', url: null, document_key: '', note_category: '' },
        ],
        nextOffset: null,
      },
      errors: [],
    });
  });

  it('shows each document its stored mark, and none when it has no mark', async () => {
    renderModal({ canEdit: true });
    const marked = await screen.findByRole('group', { name: 'Mark Escalation guide as a fact or an SOP' });
    expect(within(marked).getByRole('radio', { name: 'Fact' })).toHaveAttribute('aria-checked', 'true');

    const unmarked = screen.getByRole('group', { name: 'Mark Runbook: restart auth-svc as a fact or an SOP' });
    expect(within(unmarked).getByRole('radio', { name: 'Default' })).toHaveAttribute('aria-checked', 'true');
  });

  it('offers no control on a document that cannot be marked', async () => {
    renderModal({ canEdit: true });
    await screen.findByText('Loose page');
    expect(screen.queryByRole('group', { name: 'Mark Loose page as a fact or an SOP' })).not.toBeInTheDocument();
  });

  it('marks a document as an SOP, keyed on document_key rather than the point id', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: { status: 'ok' }, errors: [] });
    renderModal({ canEdit: true });

    const group = await screen.findByRole('group', { name: 'Mark Runbook: restart auth-svc as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'SOP' }));

    expect(apiKnowledgeBase.setKBDocumentCategory).toHaveBeenCalledWith('acct-1', 'kb-1', RUNBOOK_KEY, 'sop');
    await waitFor(() => expect(within(group).getByRole('radio', { name: 'SOP' })).toHaveAttribute('aria-checked', 'true'));
  });

  // An integration's organisation knowledge base (#40605): accounts that carry
  // their own mark are counted, and setting the mark here replaces theirs.
  it('says how many accounts mark a document differently, until the mark is set here', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.getKBDocuments.mockResolvedValue({
      data: {
        items: [{ id: 'p1', title: 'Runbook: restart auth-svc', url: null, document_key: RUNBOOK_KEY, note_category: 'fact', account_marks: 2 }],
        nextOffset: null,
      },
      errors: [],
    });
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: { status: 'ok' }, errors: [] });
    renderModal({ canEdit: true });

    expect(await screen.findByText('2 accounts differ')).toBeInTheDocument();
    const group = screen.getByRole('group', { name: 'Mark Runbook: restart auth-svc as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'SOP' }));

    await waitFor(() => expect(screen.queryByText('2 accounts differ')).not.toBeInTheDocument());
  });

  it('clears a mark with Default', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: { status: 'ok' }, errors: [] });
    renderModal({ canEdit: true });

    const group = await screen.findByRole('group', { name: 'Mark Escalation guide as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'Default' }));

    expect(apiKnowledgeBase.setKBDocumentCategory).toHaveBeenCalledWith('acct-1', 'kb-1', GUIDE_KEY, '');
  });

  it('puts the control back and reports the error when the write fails', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: null, errors: [{ message: 'kb: permission denied' }] });
    renderModal({ canEdit: true });

    const group = await screen.findByRole('group', { name: 'Mark Escalation guide as a fact or an SOP' });
    await user.click(within(group).getByRole('radio', { name: 'SOP' }));

    expect(await screen.findByText('kb: permission denied')).toBeInTheDocument();
    expect(within(group).getByRole('radio', { name: 'Fact' })).toHaveAttribute('aria-checked', 'true');
  });

  it('shows a read-only viewer the mark without a way to change it', async () => {
    renderModal();
    const row = (await screen.findByText('Escalation guide')).closest('[data-testid="kb-document-row"]');
    expect(within(row).getByText('Fact')).toBeInTheDocument();
    expect(screen.queryByRole('group', { name: /as a fact or an SOP/ })).not.toBeInTheDocument();
    // The label alone says nothing about what it does, so the legend is shown too.
    expect(screen.getByTestId('kb-document-category-legend')).toHaveTextContent('Select an account to change');
  });
});
