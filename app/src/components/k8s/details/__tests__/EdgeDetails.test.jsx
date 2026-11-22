// #40706: Edge Details must say where an edge came from in one readable row,
// without confusing it with the starting node or the edge's own `source` property.

import { render, screen, within } from '@testing-library/react';
import EdgeDetails from '../EdgeDetails';

// Shape of kg_get_edge plus the node names KnowledgeGraph adds before opening the dialog.
const edgeFor = (overrides = {}) => ({
  id: 'edge-1',
  source_node_id: 'n1',
  dest_node_id: 'n2',
  relationship_type: 'RUNS_AS',
  source: '',
  properties: { source: 'iam_role', contributing_sources: ['aws'] },
  source_name: 'checkout-lambda',
  destination_name: 'checkout-role',
  updated_at: '2026-10-09T10:00:00Z',
  ...overrides,
});

const rowValue = (label) => {
  const row = screen.getByText(label, { selector: 'p' }).closest('tr');
  return within(row).getAllByText(/.*/, { selector: 'p' })[1].textContent;
};

describe('EdgeDetails', () => {
  it('shows the origin as a readable "Detected By" row and names the endpoints From / To', () => {
    render(<EdgeDetails edge={edgeFor()} />);

    expect(rowValue('Detected By')).toBe('aws');
    expect(rowValue('From')).toBe('checkout-lambda');
    expect(rowValue('To')).toBe('checkout-role');
    expect(rowValue('Relationship Type')).toBe('RUNS_AS');
    expect(rowValue('Derived From')).toBe('iam_role');

    expect(screen.queryByText('["aws"]')).not.toBeInTheDocument();
    expect(screen.queryByText('Contributing Sources')).not.toBeInTheDocument();
    expect(screen.queryByText('Source Name')).not.toBeInTheDocument();
    expect(screen.queryByText('Source', { selector: 'p' })).not.toBeInTheDocument();
    expect(screen.getByTestId('edge-last-seen')).toHaveTextContent(/Last seen:/);
  });

  it('shows "manual" for a manually declared edge', () => {
    render(<EdgeDetails edge={edgeFor({ relationship_type: 'DEPENDS_ON', properties: { contributing_sources: ['manual'] } })} />);

    expect(rowValue('Detected By')).toBe('manual');
    expect(screen.queryByText('Derived From')).not.toBeInTheDocument();
  });

  // #41504: the dialog opens on the graph's edge object, which carries a
  // client-side `relationshipLabel` for the hover tooltip. It is not a label of
  // the edge, and it used to be the only row a failed lookup left on screen.
  it('never lists the client-side relationshipLabel as a label', () => {
    render(<EdgeDetails edge={edgeFor({ relationshipLabel: 'Runs As' })} />);

    expect(screen.queryByText('Relationship Label')).not.toBeInTheDocument();
    expect(screen.queryByText('Runs As')).not.toBeInTheDocument();
    expect(rowValue('Relationship Type')).toBe('RUNS_AS');
  });

  it('says so when the full details could not be fetched, instead of rendering blank', () => {
    const { unmount } = render(<EdgeDetails edge={edgeFor()} />);
    expect(screen.queryByTestId('edge-details-unavailable')).not.toBeInTheDocument();
    unmount();

    render(<EdgeDetails edge={edgeFor()} detailsUnavailable={true} />);
    expect(screen.getByTestId('edge-details-unavailable')).toHaveTextContent('Full details are unavailable for this relationship.');
    // The endpoints are still worth showing — only the lookup failed.
    expect(rowValue('From')).toBe('checkout-lambda');
  });

  it('joins several collectors and falls back to the top-level source', () => {
    const { unmount } = render(<EdgeDetails edge={edgeFor({ properties: { contributing_sources: ['ebpf', 'traces'] } })} />);
    expect(rowValue('Detected By')).toBe('ebpf, traces');
    unmount();

    render(<EdgeDetails edge={edgeFor({ source: 'aws', properties: {} })} />);
    expect(rowValue('Detected By')).toBe('aws');
  });
});
