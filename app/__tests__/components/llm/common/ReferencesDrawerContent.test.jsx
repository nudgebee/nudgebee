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
