import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import InvestigateSidebar from '@components/k8s/investigate/InvestigateSidebar';
import apiOwnership from '@api1/ownership';
import { isTenantAdmin } from '@lib/auth';

/*
 * "Assign owner" beside "Unassigned" in the investigation sidebar: who is offered it,
 * for which events, and what it writes to.
 */

jest.mock('@api1/ownership', () => ({ __esModule: true, default: { resolveOwners: jest.fn() } }));

jest.mock('@lib/auth', () => ({
  __esModule: true,
  isTenantAdmin: jest.fn(() => true),
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

// The dialog has its own suite; a marker that echoes what the sidebar hands it.
jest.mock('@components/ownership/AssignOwnerModal', () => ({
  __esModule: true,
  default: (props: { open: boolean; resourceType: string; resourceKey: string; cloudAccountId?: string; onChange?: () => void }) =>
    props.open ? (
      <button
        type='button'
        data-testid='assign-modal'
        data-resource-type={props.resourceType}
        data-resource-key={props.resourceKey}
        data-cloud-account={props.cloudAccountId}
        onClick={props.onChange}
      />
    ) : null,
}));

const resolveOwners = apiOwnership.resolveOwners as jest.Mock;
const mockIsTenantAdmin = isTenantAdmin as jest.Mock;

const k8sRow = (overrides = {}) => ({
  id: 'evt-1',
  cloud_account_id: 'acct-1',
  cloud_resource_id: 'res-1',
  subject_namespace: 'payments',
  subject_name: 'api-7d9f',
  subject_owner: 'api',
  subject_type: 'pod',
  ...overrides,
});

// Answers each rung the sidebar asks for; `first` is merged into the resource's own rung.
const answer = (first: object = {}, rest: object = {}) =>
  resolveOwners.mockImplementation(async (refs: { resource_type: string; resource_key: string }[]) =>
    refs.map((ref, i) => ({ ...ref, found: false, ...(i === 0 ? first : rest) }))
  );

const sidebar = (row = k8sRow(), isK8s = true) => (
  <InvestigateSidebar row={row} isK8s={isK8s} sourceKnown othersData={[]} alertLabels={{}} alertRules={[]} queryParam={{}} />
);
const renderSidebar = (row = k8sRow(), isK8s = true) => render(sidebar(row, isK8s));

const settled = () => waitFor(() => expect(screen.queryByText('Resolving…')).not.toBeInTheDocument());

describe('assigning an owner from the investigation sidebar', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockIsTenantAdmin.mockReturnValue(true);
  });

  it('offers a tenant admin the workload the event is about, and re-reads the owner after a save', async () => {
    answer();
    renderSidebar();
    await settled();

    expect(screen.getByText('Unassigned')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('investigate-assign-owner'));
    const modal = screen.getByTestId('assign-modal');
    expect(modal).toHaveAttribute('data-resource-type', 'workload');
    expect(modal).toHaveAttribute('data-resource-key', 'res-1');
    expect(modal).toHaveAttribute('data-cloud-account', 'acct-1');

    const before = resolveOwners.mock.calls.length;
    fireEvent.click(modal);
    await waitFor(() => expect(resolveOwners.mock.calls.length).toBeGreaterThan(before));
  });

  it('offers nothing to anyone who is not a tenant admin', async () => {
    mockIsTenantAdmin.mockReturnValue(false);
    answer();
    renderSidebar();
    await settled();

    expect(screen.getByText('Unassigned')).toBeInTheDocument();
    expect(screen.queryByTestId('investigate-assign-owner')).not.toBeInTheDocument();
  });

  it('offers nothing when the resource is no longer active', async () => {
    answer({ no_active_resource: true });
    renderSidebar();
    await settled();

    expect(screen.getByText('Unassigned')).toBeInTheDocument();
    expect(screen.queryByTestId('investigate-assign-owner')).not.toBeInTheDocument();
  });

  it('offers nothing when the event only identifies a namespace', async () => {
    answer();
    renderSidebar(k8sRow({ cloud_resource_id: null }));
    await settled();

    expect(screen.getByText('Unassigned')).toBeInTheDocument();
    expect(screen.queryByTestId('investigate-assign-owner')).not.toBeInTheDocument();
  });

  it('offers nothing when something already owns the resource', async () => {
    answer({}, { found: true, via: 'self', source: 'manual', owner_type: 'group', owner_id: 'g1', owner_name: 'Payments' });
    renderSidebar();
    await settled();

    expect(screen.queryByText('Unassigned')).not.toBeInTheDocument();
    expect(screen.queryByTestId('investigate-assign-owner')).not.toBeInTheDocument();
  });

  it('keeps an open dialog on the resource it was opened for when the sidebar moves to another event', async () => {
    // The sidebar is handed a new event rather than remounted; a dialog that followed
    // the chain would end up writing the owner picked for one resource onto another.
    answer();
    const view = renderSidebar();
    await settled();
    fireEvent.click(screen.getByTestId('investigate-assign-owner'));

    view.rerender(sidebar(k8sRow({ id: 'evt-2', cloud_resource_id: 'res-2', subject_owner: 'ledger' })));
    await settled();

    expect(screen.getByTestId('assign-modal')).toHaveAttribute('data-resource-key', 'res-1');
  });

  it('targets the cloud resource on a cloud event', async () => {
    answer();
    renderSidebar(k8sRow({ subject_namespace: 'EC2', subject_type: 'ec2_instance', subject_owner: '' }), false);
    await settled();

    fireEvent.click(screen.getByTestId('investigate-assign-owner'));
    expect(screen.getByTestId('assign-modal')).toHaveAttribute('data-resource-type', 'cloud_resource');
  });
});
