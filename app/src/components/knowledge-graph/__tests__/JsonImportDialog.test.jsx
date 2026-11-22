// The import result groups what landed by kind and by outcome, so a user sees
// which nodes were created, which already existed, and why anything was refused.

import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import JsonImportDialog, { ResultView } from '../JsonImportDialog';
import apiKnowledgeGraph from '@api1/knowledge-graph';

jest.mock('@api1/knowledge-graph', () => ({ __esModule: true, default: { createManualDeclarations: jest.fn() } }));
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn(), success: jest.fn(), warning: jest.fn() } }));
jest.mock('@ui/CodeEditor', () => ({
  __esModule: true,
  CodeEditor: ({ value, onChange }) => <textarea data-testid='declarations-editor' value={value} onChange={(e) => onChange(e.target.value)} />,
}));

const RESULT = {
  nodes: {
    created: [
      { index: 0, ref: 'billing-db', id: 1, node_id: 'n-1', declaration: { name: 'billing-db' } },
      { index: 1, ref: 'orders-db', id: 2, node_id: 'n-2', declaration: { name: 'orders-db' } },
    ],
    existing: [
      { index: 2, ref: 'ledger-worker', node_id: 'n-3', error: 'this resource is already in the graph (node n-3, from k8s)', declaration: {} },
    ],
    rejected: [{ index: 3, ref: 'no-region', error: 'region is required', declaration: { name: 'no-region' } }],
  },
  dependencies: {
    created: [
      {
        index: 0,
        id: 10,
        status: 'resolved',
        declaration: { source: { node_type: 'Workload', name: 'accounting', namespace: 'demo' }, dest: 'billing-db' },
      },
    ],
    existing: [
      {
        index: 2,
        id: 11,
        status: 'resolved',
        error: 'manual dependency already declared',
        declaration: { source: 'ledger-worker', dest: 'billing-db' },
      },
    ],
    rejected: [
      { index: 1, error: 'dest: node "no-region" was not declared: region is required', declaration: { source: 'ledger-worker', dest: 'no-region' } },
    ],
  },
};

it('summarises and groups every item by kind and outcome', () => {
  const onDone = jest.fn();
  render(<ResultView result={RESULT} onDone={onDone} />);

  expect(screen.getByTestId('manual-declarations-result-summary')).toHaveTextContent(
    '2 nodes created, 1 existing, 1 rejected · 1 dependency created, 1 existing, 1 rejected'
  );
  const rows = screen.getAllByTestId('manual-declarations-result-row');
  expect(rows.map((r) => r.getAttribute('data-outcome'))).toEqual(['created', 'created', 'existing', 'rejected', 'created', 'existing', 'rejected']);

  expect(rows[0]).toHaveTextContent('#0 billing-db → node n-1');
  expect(rows[2]).toHaveTextContent('already in the graph');
  // A refused node carries no node id.
  expect(rows[3]).toHaveTextContent('#3 no-region');
  expect(rows[3]).not.toHaveTextContent('→ node');
  expect(within(rows[3]).getByText('region is required')).toBeInTheDocument();

  expect(rows[4]).toHaveTextContent('#0 Workload: demo/accounting → billing-db');
  expect(within(rows[4]).getByText('resolved')).toBeInTheDocument();
  // Re-importing a dependency reports it, it is not an error.
  expect(rows[5]).toHaveTextContent('#2 ledger-worker → billing-db');
  expect(rows[5]).toHaveTextContent('already declared');
  expect(rows[6]).toHaveTextContent('#1 ledger-worker → no-region');
  expect(rows[6]).toHaveTextContent('was not declared');

  fireEvent.click(screen.getByRole('button', { name: 'Done' }));
  expect(onDone).toHaveBeenCalled();
});

it('leaves out a kind the document did not declare', () => {
  render(
    <ResultView
      result={{ nodes: { created: [], existing: [], rejected: [] }, dependencies: { created: RESULT.dependencies.created, rejected: [] } }}
      onDone={() => {}}
    />
  );
  expect(screen.queryByText('Nodes')).not.toBeInTheDocument();
  expect(screen.getByText('Dependencies')).toBeInTheDocument();
  expect(screen.getByTestId('manual-declarations-result-summary')).toHaveTextContent('0 nodes created, 0 existing, 0 rejected');
});

describe('closing the dialog', () => {
  const importOnce = async (props) => {
    apiKnowledgeGraph.createManualDeclarations.mockResolvedValue({ data: { data: { kg_create_manual_declarations: { data: RESULT } } } });
    render(<JsonImportDialog open schema={[]} {...props} />);
    fireEvent.change(screen.getByTestId('declarations-editor'), { target: { value: '{"nodes": [{"node_type": "Database", "name": "a"}]}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Import' }));
    await waitFor(() => expect(screen.getByTestId('manual-declarations-result-summary')).toBeInTheDocument());
  };

  it('after an import, the close icon refreshes the lists like Done does', async () => {
    const onClose = jest.fn();
    const onImported = jest.fn();
    await importOnce({ onClose, onImported });
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onImported).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();
  });

  it('while an import is in flight, the close icon does nothing', async () => {
    const onClose = jest.fn();
    const onImported = jest.fn();
    let finish;
    apiKnowledgeGraph.createManualDeclarations.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    render(<JsonImportDialog open schema={[]} onClose={onClose} onImported={onImported} />);
    fireEvent.change(screen.getByTestId('declarations-editor'), { target: { value: '{"nodes": [{"node_type": "Database", "name": "a"}]}' } });
    fireEvent.click(screen.getByRole('button', { name: 'Import' }));
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).not.toHaveBeenCalled();
    expect(onImported).not.toHaveBeenCalled();
    finish({ data: { data: { kg_create_manual_declarations: { data: RESULT } } } });
    await waitFor(() => expect(screen.getByTestId('manual-declarations-result-summary')).toBeInTheDocument());
  });

  it('before any import, the close icon only closes', () => {
    const onClose = jest.fn();
    const onImported = jest.fn();
    render(<JsonImportDialog open schema={[]} onClose={onClose} onImported={onImported} />);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onImported).not.toHaveBeenCalled();
  });
});
