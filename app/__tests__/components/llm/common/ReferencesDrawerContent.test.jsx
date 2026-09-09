import { render, screen, fireEvent } from '@testing-library/react';
import ReferencesDrawerContent from '@components/llm/common/ReferencesDrawerContent';

// Contract under test (#34779): a knowledge_base reference row — e.g. the
// runbook the event analysis used — must render a readable subject (falling
// back to metadata.name when subject is absent) and, when the reference
// carries a source-page url, an "Open source page" link in the expanded row.

const kbRef = (metadata) => ({
  id: 'ref-1',
  type: 'knowledge_base',
  metadata,
});

describe('ReferencesDrawerContent knowledge_base rows', () => {
  it('falls back to metadata.name for the subject when subject is absent', () => {
    render(<ReferencesDrawerContent references={[kbRef({ name: 'dev-confluence', via: 'kb_prestep' })]} />);
    expect(screen.getByText('dev-confluence')).toBeInTheDocument();
    expect(screen.queryByText('(unnamed)')).not.toBeInTheDocument();
  });

  it('prefers metadata.subject over metadata.name', () => {
    render(<ReferencesDrawerContent references={[kbRef({ subject: 'NBLLM Agent Latency P95 High — Runbook', name: 'dev-confluence' })]} />);
    expect(screen.getByText('NBLLM Agent Latency P95 High — Runbook')).toBeInTheDocument();
  });

  it('renders an external link in the expanded row when metadata.url is present', () => {
    render(
      <ReferencesDrawerContent
        references={[
          kbRef({
            name: 'dev-confluence',
            url: 'https://example.atlassian.net/wiki/pages/113836034',
            source: 'confluence',
          }),
        ]}
      />
    );
    // Expand the row.
    fireEvent.click(screen.getByText('dev-confluence'));
    const link = screen.getByRole('link', { name: /open source page/i });
    expect(link).toHaveAttribute('href', 'https://example.atlassian.net/wiki/pages/113836034');
    expect(link).toHaveAttribute('target', '_blank');
    expect(link).toHaveAttribute('rel', expect.stringContaining('noopener'));
  });

  it('renders no link when metadata.url is absent or not http(s)', () => {
    render(<ReferencesDrawerContent references={[kbRef({ name: 'dev-confluence', url: 'javascript:alert(1)' })]} />);
    fireEvent.click(screen.getByText('dev-confluence'));
    expect(screen.queryByRole('link', { name: /open source page/i })).not.toBeInTheDocument();
  });
});

describe('ReferencesDrawerContent knowledge_base kinds', () => {
  // Three writers persist reference_type 'knowledge_base': pre-step documents
  // attributed to a KB, documents from collections with no KB row, and skills
  // loaded mid-run. Before metadata.kind they rendered identically, so a
  // conversation that injected no KB content still showed "contexts" and read
  // as grounded.
  const rows = [
    { id: '1', type: 'knowledge_base', metadata: { kind: 'kb_document', name: 'Runbooks', subject: 'Restart procedure' } },
    { id: '2', type: 'knowledge_base', metadata: { kind: 'nb_document', name: 'nudgebee_docs', subject: 'Playbook Catalog' } },
    { id: '3', type: 'knowledge_base', metadata: { kind: 'skill', name: 'Golden_Signal_Dashboard' } },
    {
      id: '4',
      type: 'knowledge_base',
      metadata: { kind: 'account_document', name: 'Account documents', url: 'https://nudgebee.atlassian.net/wiki/spaces/SD/pages/164064' },
    },
    { id: '5', type: 'knowledge_base', metadata: { name: 'Legacy row, no kind' } },
  ];

  it('labels each kind distinctly instead of collapsing them', () => {
    render(<ReferencesDrawerContent references={rows} />);
    expect(screen.getAllByText('Knowledge Base').length).toBeGreaterThan(0);
    expect(screen.getAllByText('NB Doc').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Skill').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Confluence').length).toBeGreaterThan(0);
  });

  it('treats a row written before metadata.kind as a KB document', () => {
    render(<ReferencesDrawerContent references={[rows[4]]} />);
    expect(screen.getAllByText('Knowledge Base').length).toBeGreaterThan(0);
    expect(screen.queryByText('NB Doc')).toBeNull();
    expect(screen.queryByText('Skill')).toBeNull();
  });
});
