// #41138: Back/Forward must restore the Label/Attribute chips that produced the
// graph they return to — not leave the newer chips in the panel while the canvas
// shows the older result, and not let a later Apply resend the stale chip.

import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import KnowledgeGraph from '../KnowledgeGraph';
import apiKubernetes1 from '@api1/kubernetes1';

jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: { knowledgeGraphFilterOptions: jest.fn(), knowledgeGraph: jest.fn(), knowledgeGraphGetNode: jest.fn(), knowledgeGraphGetEdge: jest.fn() },
}));
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: jest.fn(() => Promise.resolve([])) } }));
jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 't1' }), isTenantAdmin: () => false }));
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

// Stand-in for the chip builder: shows the chips it is given, and "key=value" + Enter adds
// a chip and reports the KG query built from all chips — like the real component does.
jest.mock('@components/k8s/common/LogQueryBuilderAutocomplete', () => {
  const MockQueryBuilder = ({ heading, queryItems, onQueryItemsChange, onQueryChange }) => {
    const section = heading || 'Label';
    return (
      <div data-testid={`qb-${section}`}>
        {(queryItems || []).map((c) => (
          <span key={c.id} data-testid='chip'>{`${c.label}=${c.value}`}</span>
        ))}
        <input
          aria-label={`${section} input`}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return;
            const [label, value] = e.target.value.split('=');
            const next = [...(queryItems || []), { id: `${label}-${value}`, label, operator: '=', value }];
            onQueryItemsChange(next);
            onQueryChange({ query: JSON.stringify(next.map((c) => ({ key: c.label, value: c.value }))) });
          }}
        />
      </div>
    );
  };
  return { __esModule: true, default: MockQueryBuilder, PropertyFilterHelp: () => null };
});

const chipsIn = (section) =>
  within(screen.getByTestId(`qb-${section}`))
    .queryAllByTestId('chip')
    .map((c) => c.textContent);
const lastGraphRequest = () => apiKubernetes1.knowledgeGraph.mock.calls.at(-1)[0];

const addChip = (section, text) => fireEvent.keyDown(screen.getByLabelText(`${section} input`), { key: 'Enter', target: { value: text } });
const apply = () => fireEvent.click(screen.getByTestId('kg-apply-filters-btn'));

beforeEach(() => {
  jest.clearAllMocks();
  apiKubernetes1.knowledgeGraphFilterOptions.mockResolvedValue({ data: { data: { kg_get_filter_options: { data: {} } } } });
  apiKubernetes1.knowledgeGraph.mockResolvedValue({ data: { data: { kg_get_complete_graph: { data: { nodes: [], edges: [] } } } } });
});

const renderGraph = async () => {
  render(<KnowledgeGraph />);
  await waitFor(() => expect(apiKubernetes1.knowledgeGraph).toHaveBeenCalled());
};

it.each([
  ['Attribute', 'attributes'],
  ['Label', 'labels'],
])('Back and Forward restore the %s chips of the graph they return to', async (section, requestKey) => {
  await renderGraph();

  addChip(section, 'instance_type=t3.micro');
  apply();
  addChip(section, 'region=us-east-1');
  apply();
  expect(chipsIn(section)).toEqual(['instance_type=t3.micro', 'region=us-east-1']);

  fireEvent.click(screen.getByTestId('kg-back-btn'));
  expect(lastGraphRequest()[requestKey]).toEqual({ instance_type: 't3.micro' });
  expect(chipsIn(section)).toEqual(['instance_type=t3.micro']);

  fireEvent.click(screen.getByTestId('kg-forward-btn'));
  expect(lastGraphRequest()[requestKey]).toEqual({ instance_type: 't3.micro', region: 'us-east-1' });
  expect(chipsIn(section)).toEqual(['instance_type=t3.micro', 'region=us-east-1']);

  // Apply after Back must not resend the chip Back removed.
  fireEvent.click(screen.getByTestId('kg-back-btn'));
  addChip(section, 'env=prod');
  apply();
  expect(lastGraphRequest()[requestKey]).toEqual({ instance_type: 't3.micro', env: 'prod' });
  expect(chipsIn(section)).toEqual(['instance_type=t3.micro', 'env=prod']);
});

it('Back restores Label and Attribute chips together', async () => {
  await renderGraph();

  addChip('Label', 'team=core');
  addChip('Attribute', 'instance_type=t3.micro');
  apply();
  addChip('Label', 'tier=web');
  addChip('Attribute', 'region=us-east-1');
  apply();

  fireEvent.click(screen.getByTestId('kg-back-btn'));
  expect(chipsIn('Label')).toEqual(['team=core']);
  expect(chipsIn('Attribute')).toEqual(['instance_type=t3.micro']);
  expect(lastGraphRequest()).toMatchObject({ labels: { team: 'core' }, attributes: { instance_type: 't3.micro' } });
});
