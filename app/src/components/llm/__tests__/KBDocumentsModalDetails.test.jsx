import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import KBDocumentsModal from '@components/llm/KBDocumentsModal';
import apiKnowledgeBase from '@api1/knowledge-base';

jest.mock('@api1/knowledge-base', () => ({
  __esModule: true,
  default: { getKBDocuments: jest.fn(), getKBDocument: jest.fn(), retriggerKB: jest.fn(), setKBDocumentCategory: jest.fn() },
}));

const KEY = (n) => `integration-a_knowledge_base|${n}`;

const KB = {
  kinds: ['knowledge', 'sop'],
  shapes: ['decision_tree', 'deterministic_procedure'],
  capabilities: ['kubectl_execute', 'promql_query'],
  capability_details: [
    { name: 'kubectl_execute', surface: 'agent_tool', scope: 'pass_through', description: 'Runs any kubectl command' },
    { name: 'promql_query', surface: 'agent_tool', scope: 'narrow', description: 'Writes PromQL, never runs it' },
  ],
};

const RUNBOOK = {
  id: 'p1',
  title: 'Pod CrashLoopBackOff runbook',
  url: 'https://wiki.example.com/p1',
  document_key: KEY(1),
  note_category: '',
  kind: 'sop',
  shape: 'deterministic_procedure',
  capabilities: ['kubectl_execute', 'promql_query', 'mystery_tool'],
};
const SECOND = {
  id: 'p2',
  title: 'Latency runbook',
  url: 'https://wiki.example.com/p2',
  document_key: KEY(2),
  note_category: '',
  kind: 'knowledge',
  shape: 'narrative_reference',
  capabilities: [],
};
const NOTE = { id: 'kb-1', title: 'Payment runbook', url: null, document_key: '', note_category: '' };

const listing = (items) => apiKnowledgeBase.getKBDocuments.mockResolvedValue({ data: { items, nextOffset: null }, errors: [] });

const renderModal = (props = {}) =>
  render(<KBDocumentsModal open onClose={jest.fn()} accountId='acct-1' kbId='kb-1' kbName='Confluence' knowledgeBase={KB} {...props} />);

const toggle = (title, open = false) => screen.findByRole('button', { name: `${open ? 'Hide' : 'Show'} details for ${title}` });

beforeEach(() => {
  jest.clearAllMocks();
  listing([RUNBOOK, SECOND, NOTE]);
});

describe('KBDocumentsModal accordion rows', () => {
  it('puts the arrow at the right end of the row, after the view-content button, pointing down while closed', async () => {
    renderModal();
    const row = (await screen.findAllByTestId('kb-document-row'))[0];
    const buttons = within(row).getAllByRole('button');
    const arrow = buttons[buttons.length - 1];
    expect(arrow).toHaveAccessibleName(`Show details for ${RUNBOOK.title}`);
    expect(arrow).toHaveAttribute('aria-expanded', 'false');
    expect(buttons[buttons.length - 2]).toHaveAccessibleName(`View content of ${RUNBOOK.title}`);
    expect(within(arrow).getByTestId('KeyboardArrowDownIcon')).toBeInTheDocument();
  });

  it('has no arrow on a hand-made note, which the classifier does not label per page', async () => {
    renderModal();
    await screen.findByText(NOTE.title);
    expect(screen.queryByRole('button', { name: `Show details for ${NOTE.title}` })).not.toBeInTheDocument();
  });

  it('opens the detail view under the row, flips to an up arrow, and closes on a second click', async () => {
    const user = userEvent.setup();
    renderModal();
    expect(screen.queryByTestId('kb-document-detail-body')).not.toBeInTheDocument();
    await user.click(await toggle(RUNBOOK.title));
    const body = await screen.findByTestId('kb-document-detail-body');
    expect(within(body).getByTestId('kb-detail-kind')).toHaveTextContent('SOP');
    const open = await toggle(RUNBOOK.title, true);
    expect(open).toHaveAttribute('aria-expanded', 'true');
    expect(within(open).getByTestId('KeyboardArrowUpIcon')).toBeInTheDocument();
    await user.click(open);
    expect(screen.queryByTestId('kb-document-detail-body')).not.toBeInTheDocument();
  });

  it('lets several rows stay open at once, each on its own tab', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(await toggle(RUNBOOK.title));
    await user.click(await toggle(SECOND.title));
    const bodies = await screen.findAllByTestId('kb-document-detail-body');
    expect(bodies).toHaveLength(2);
    await user.click(within(bodies[0]).getByRole('tab', { name: /Capabilities/ }));
    expect(bodies[0]).toHaveTextContent('kubectl_execute');
    expect(within(bodies[1]).getByTestId('kb-detail-kind')).toBeInTheDocument();
  });
});

describe('KBDocumentsModal accordion state is per target', () => {
  it('closes every open detail panel when the modal moves to another knowledge base', async () => {
    const user = userEvent.setup();
    const { rerender } = renderModal();
    await user.click(await toggle(RUNBOOK.title));
    expect(await screen.findByTestId('kb-document-detail-body')).toBeInTheDocument();

    rerender(<KBDocumentsModal open onClose={jest.fn()} accountId='acct-1' kbId='kb-2' kbName='ServiceNow' knowledgeBase={KB} />);

    await toggle(RUNBOOK.title);
    expect(screen.queryByTestId('kb-document-detail-body')).not.toBeInTheDocument();
  });

  it('opens with every panel closed after the modal was closed and opened again', async () => {
    const user = userEvent.setup();
    const { rerender } = renderModal();
    await user.click(await toggle(RUNBOOK.title));
    expect(await screen.findByTestId('kb-document-detail-body')).toBeInTheDocument();

    rerender(<KBDocumentsModal open={false} onClose={jest.fn()} accountId='acct-1' kbId='kb-1' kbName='Confluence' knowledgeBase={KB} />);
    rerender(<KBDocumentsModal open onClose={jest.fn()} accountId='acct-1' kbId='kb-1' kbName='Confluence' knowledgeBase={KB} />);

    await toggle(RUNBOOK.title);
    expect(screen.queryByTestId('kb-document-detail-body')).not.toBeInTheDocument();
  });
});

describe('KBDocumentsModal detail view', () => {
  it('shows the capabilities with what the catalog says about each, and marks one it does not know', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(await toggle(RUNBOOK.title));
    const body = await screen.findByTestId('kb-document-detail-body');
    await user.click(within(body).getByRole('tab', { name: /Capabilities/ }));
    expect(body).toHaveTextContent('Pass-through');
    expect(body).toHaveTextContent('Runs any kubectl command');
    expect(body).toHaveTextContent('Narrow');
    expect(body).toHaveTextContent('mystery_tool');
    expect(body).toHaveTextContent('Not in catalog');
  });

  it('shows the knowledge base roll-up and how the loaded pages split', async () => {
    const user = userEvent.setup();
    renderModal();
    await user.click(await toggle(RUNBOOK.title));
    const body = await screen.findByTestId('kb-document-detail-body');
    await user.click(within(body).getByRole('tab', { name: /Knowledge base/ }));
    expect(within(body).getByTestId('kb-detail-kb-shapes')).toHaveTextContent('Decision Tree');
    expect(within(body).getByTestId('kb-detail-kb-loaded')).toHaveTextContent('1 SOP · 1 knowledge · 0 not classified');
  });

  it('follows a mark set from the row and explains that the mark wins', async () => {
    const user = userEvent.setup();
    apiKnowledgeBase.setKBDocumentCategory.mockResolvedValue({ data: { status: 'ok' }, errors: [] });
    renderModal({ canEdit: true });
    await user.click(await toggle(RUNBOOK.title));
    const body = await screen.findByTestId('kb-document-detail-body');
    const group = screen.getByRole('group', { name: `Mark ${RUNBOOK.title} as a fact or an SOP` });
    await user.click(within(group).getByRole('radio', { name: 'Fact' }));
    await waitFor(() => expect(within(body).getByTestId('kb-detail-mark')).toHaveTextContent('Fact'));
    expect(body).toHaveTextContent('The classifier says SOP; your mark (Fact) wins.');
  });

  it('degrades to a hint when the knowledge base has no roll-up', async () => {
    const user = userEvent.setup();
    renderModal({ knowledgeBase: null });
    await user.click(await toggle(RUNBOOK.title));
    const body = await screen.findByTestId('kb-document-detail-body');
    await user.click(within(body).getByRole('tab', { name: /Knowledge base/ }));
    expect(body).toHaveTextContent('No roll-up is stored for this knowledge base yet.');
  });
});

describe('KBDocumentsModal chip colours tell the elements apart', () => {
  it('makes SOP the standout (green), knowledge neutral, and the shape a quiet outline', async () => {
    renderModal();
    const rows = await screen.findAllByTestId('kb-document-row');
    const kindOf = (row, text) => within(row).getByText(text).closest('[data-tone]');
    expect(kindOf(rows[0], 'Classifier: SOP')).toHaveAttribute('data-tone', 'success');
    expect(kindOf(rows[1], 'Classifier: Knowledge')).toHaveAttribute('data-tone', 'neutral');
    expect(kindOf(rows[0], 'Deterministic Procedure')).toHaveAttribute('data-tone', 'subtle');
    expect(kindOf(rows[1], 'Narrative Reference')).toHaveAttribute('data-tone', 'subtle');
  });

  it('gives capabilities their own chip, only on a page that has some', async () => {
    renderModal();
    const rows = await screen.findAllByTestId('kb-document-row');
    expect(within(rows[0]).getByText('3 capabilities')).toBeInTheDocument();
    expect(within(rows[1]).queryByText(/capabilit/)).not.toBeInTheDocument();
  });
});
