// #40707: the top-right toolbar (Search nodes…, Relationships, Download, Settings)
// is absolutely positioned over the graph card, while the Back / Forward / path row
// sits in normal flow under it. Without room reserved on the right, a long path
// slid under the toolbar and its last chip and the "Details" toggle could not be
// clicked. jsdom has no layout, so this pins the mechanism: the row reserves the
// toolbar's measured width as right padding.

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KnowledgeGraph from '../KnowledgeGraph';

jest.mock('reactflow', () => {
  const actual = jest.requireActual('reactflow');
  const Stub = () => null;
  return { __esModule: true, ...actual, default: Stub, Controls: Stub, Background: Stub, MiniMap: Stub };
});
jest.mock('@api1/kubernetes1', () => ({
  __esModule: true,
  default: {
    knowledgeGraphFilterOptions: jest.fn(() => Promise.resolve({ data: { data: { kg_get_filter_options: { data: {} } } } })),
    // Never settles: the graph itself is irrelevant to the toolbar/path layout.
    knowledgeGraph: jest.fn(() => new Promise(() => {})),
    knowledgeGraphGetNode: jest.fn(),
    knowledgeGraphGetEdge: jest.fn(),
  },
}));
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: jest.fn(() => Promise.resolve([])) } }));
jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 't1' }), isTenantAdmin: () => true }));
jest.mock('@components/k8s/common/LogQueryBuilderAutocomplete', () => ({
  __esModule: true,
  default: () => null,
  PropertyFilterHelp: () => null,
}));
jest.mock('@components/k8s/details/NodeDetails', () => () => null);
jest.mock('@components/k8s/details/EdgeDetails', () => () => null);
jest.mock('@components/k8s/details/ServiceMapLegends', () => () => null);
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn() } }));

const TOOLBAR_WIDTH = 480;
const OriginalResizeObserver = global.ResizeObserver;

beforeEach(() => {
  // Report a laid-out width and fire the observer once on observe, as a browser does.
  jest.spyOn(HTMLElement.prototype, 'offsetWidth', 'get').mockReturnValue(TOOLBAR_WIDTH);
  global.ResizeObserver = class {
    constructor(callback) {
      this.callback = callback;
    }
    observe(el) {
      this.callback([{ target: el }]);
    }
    unobserve() {}
    disconnect() {}
  };
});

afterEach(() => {
  jest.restoreAllMocks();
  global.ResizeObserver = OriginalResizeObserver;
});

it('reserves the toolbar width on the right of the Back / path row', async () => {
  render(<KnowledgeGraph />);
  expect(screen.queryByTestId('kg-back-btn')).not.toBeInTheDocument();

  // Clear All pushes a history entry, which brings up the Back / path row.
  fireEvent.click(screen.getByTestId('kg-clear-filters-btn'));
  const backBtn = await screen.findByTestId('kg-back-btn');

  const row = screen.getByTestId('kg-path-row');
  expect(row).toContainElement(backBtn);
  await waitFor(() => expect(row).toHaveStyle(`padding-right: calc(${TOOLBAR_WIDTH}px + var(--ds-space-3))`));
});
