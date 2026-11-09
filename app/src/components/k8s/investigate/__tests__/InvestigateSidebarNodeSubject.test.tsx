import '@testing-library/jest-dom';
import { render, screen, waitFor } from '@testing-library/react';
import InvestigateSidebar from '@components/k8s/investigate/InvestigateSidebar';
import apiOwnership from '@api1/ownership';

/*
 * The "where" rows at the top of the investigation sidebar when the alert is about a node.
 */

jest.mock('@api1/ownership', () => ({ __esModule: true, default: { resolveOwners: jest.fn() } }));

jest.mock('@lib/auth', () => ({
  __esModule: true,
  isTenantAdmin: jest.fn(() => false),
  hasWriteAccess: jest.fn(() => true),
  getUserSession: jest.fn(() => ({})),
}));

jest.mock('next/router', () => ({
  useRouter: () => ({ events: { on: jest.fn(), off: jest.fn() }, push: jest.fn(), replace: jest.fn(), asPath: '/investigate', query: {} }),
}));

// Nothing here is about the event's state, its actions or its history.
jest.mock('@components/k8s/investigate/InvestigateDropdown', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/k8s/investigate/EventStateRows', () => ({
  __esModule: true,
  EventOccurrenceRows: () => null,
  EventStateRows: () => null,
  useEventOccurrence: () => ({}),
}));

const resolveOwners = apiOwnership.resolveOwners as jest.Mock;

const NODE = 'gke-pool-1-node-7f3a';

const renderSidebar = (row: object) =>
  render(
    <InvestigateSidebar
      row={{ id: 'evt-1', cloud_account_id: 'acct-1', ...row }}
      isK8s
      sourceKnown
      othersData={[]}
      alertLabels={{}}
      alertRules={[]}
      queryParam={{}}
    />
  );

const settled = () => waitFor(() => expect(screen.queryByText('Resolving…')).not.toBeInTheDocument());

describe('the node rows of the investigation sidebar', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    resolveOwners.mockImplementation(async (refs: object[]) => refs.map((ref) => ({ ...ref, found: false })));
  });

  it('names a node subject once', async () => {
    renderSidebar({ subject_type: 'node', subject_name: NODE, subject_node: NODE });
    await settled();

    expect(screen.getAllByText('Node')).toHaveLength(1);
    expect(screen.getAllByText(NODE)).toHaveLength(1);
    expect(screen.queryByText('Namespace')).not.toBeInTheDocument();
  });

  it('still shows the node a pod runs on', async () => {
    renderSidebar({ subject_type: 'pod', subject_name: 'api-7d9f', subject_namespace: 'payments', subject_node: NODE });
    await settled();

    expect(screen.getByText('Pod')).toBeInTheDocument();
    expect(screen.getByText('Node')).toBeInTheDocument();
    expect(screen.getByText(NODE)).toBeInTheDocument();
  });
});
