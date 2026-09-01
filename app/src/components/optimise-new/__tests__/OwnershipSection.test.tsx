import '@testing-library/jest-dom';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import OwnershipSection, { buildLevels } from '@components/optimise-new/OwnershipSection';
import apiOwnership from '@api1/ownership';
import { isTenantAdmin } from '@lib/auth';

jest.mock('@api1/ownership', () => ({
  __esModule: true,
  default: { resolveOwners: jest.fn() },
}));

// Writing is tenant-admin only; default to admin so the assign action renders,
// and flip it off in the one test that checks the read-only path.
jest.mock('@lib/auth', () => ({ __esModule: true, isTenantAdmin: jest.fn(() => true) }));

// The modal has its own suite; here we stub it to a marker that echoes the props
// OwnershipSection wires, so we can assert which resource the assign targets.
jest.mock('@components/ownership/AssignOwnerModal', () => ({
  __esModule: true,
  default: (props: { open: boolean; resourceType: string; resourceKey: string; cloudAccountId?: string }) =>
    props.open ? (
      <div
        data-testid='assign-modal'
        data-resource-type={props.resourceType}
        data-resource-key={props.resourceKey}
        data-cloud-account={props.cloudAccountId}
      />
    ) : null,
}));

const resolveOwners = apiOwnership.resolveOwners as jest.Mock;
const mockIsTenantAdmin = isTenantAdmin as jest.Mock;

// A k8s recommendation: resource_id is the workload's cloud_resourses id, and
// resource_k8s_namespace is what makes it k8s rather than cloud.
const k8sRec = (overrides = {}) => ({
  id: 'rec-1',
  account_id: 'acct-1',
  resource_id: 'res-1',
  resource_name: 'sonarqube-postgresql',
  resource_type: 'StatefulSet',
  resource_k8s_namespace: 'sonarqube',
  ...overrides,
});

const owner = (overrides = {}) => ({
  found: true,
  owner_type: 'user',
  owner_id: 'u1',
  owner_name: 'Mangglesh Dagar',
  source: 'manual',
  via: 'self',
  ...overrides,
});

const unowned = { found: false };

// The chain the component asks for, in order. Responses are matched back by
// (resource_type, resource_key), so mocks must carry them like the real API does.
const K8S_CHAIN = [
  { resource_type: 'workload', resource_key: 'res-1' },
  { resource_type: 'namespace', resource_key: 'acct-1/sonarqube' },
  { resource_type: 'cloud_account', resource_key: 'acct-1' },
];
const CLOUD_CHAIN = [
  { resource_type: 'cloud_resource', resource_key: 'res-1' },
  { resource_type: 'cloud_account', resource_key: 'acct-1' },
];

// Same chain, retargeted at a different resource id.
const chainFor = (resourceId: string) => [{ resource_type: 'workload', resource_key: resourceId }, ...K8S_CHAIN.slice(1)];

// Stamp each result with the rung it answers for.
const respond = (chain: { resource_type: string; resource_key: string }[], ...results: object[]) => results.map((r, i) => ({ ...chain[i], ...r }));

beforeEach(() => {
  resolveOwners.mockReset();
  mockIsTenantAdmin.mockReturnValue(true);
});

describe('buildLevels', () => {
  it('walks workload → namespace → cloud account for a k8s resource', () => {
    expect(buildLevels('res-1', 'acct-1', 'sonarqube')).toEqual([
      { level: 'Workload', resourceType: 'workload', resourceKey: 'res-1', own: null },
      { level: 'Namespace', resourceType: 'namespace', resourceKey: 'acct-1/sonarqube', own: null },
      { level: 'Cloud account', resourceType: 'cloud_account', resourceKey: 'acct-1', own: null },
    ]);
  });

  it('drops the namespace rung for a cloud resource', () => {
    expect(buildLevels('res-9', 'acct-1', '')).toEqual([
      { level: 'Resource', resourceType: 'cloud_resource', resourceKey: 'res-9', own: null },
      { level: 'Cloud account', resourceType: 'cloud_account', resourceKey: 'acct-1', own: null },
    ]);
  });
});

describe('OwnershipSection', () => {
  it('shows an owner inherited from the namespace, marking that level effective', async () => {
    resolveOwners.mockResolvedValue(
      respond(
        K8S_CHAIN,
        { ...owner(), via: 'namespace' }, // workload: inherited, so it owns nothing itself
        owner(), // namespace: via self ⇒ this is the level that owns
        { ...owner(), owner_name: 'Platform Guild', owner_type: 'group', owner_id: 'g1' }
      )
    );
    render(<OwnershipSection rec={k8sRec()} />);

    expect(await screen.findByText('Inherited from the namespace owner.')).toBeInTheDocument();
    expect(screen.getByText('Ownership chain')).toBeInTheDocument();
    expect(screen.getByText('effective')).toBeInTheDocument();
    // The workload rung has no owner of its own.
    expect(screen.getByText('—')).toBeInTheDocument();
  });

  it('labels the link "Manage rules" when a rule decided the owner', async () => {
    resolveOwners.mockResolvedValue(respond(K8S_CHAIN, owner({ source: 'rule' }), unowned, unowned));
    render(<OwnershipSection rec={k8sRec()} />);

    expect(await screen.findByText('Matched by an ownership rule.')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Manage rules/ })).toHaveAttribute('href', '/user-management#ownership');
    // Hybrid: a rule match still offers an in-place assign to override it.
    expect(screen.getByRole('button', { name: 'Assign owner' })).toBeInTheDocument();
  });

  it('sets a manually-owned k8s workload in place, targeting its workload ref', async () => {
    resolveOwners.mockResolvedValue(respond(K8S_CHAIN, owner(), unowned, unowned));
    render(<OwnershipSection rec={k8sRec()} />);

    expect(await screen.findByText('Assigned directly to this resource.')).toBeInTheDocument();
    // No navigate-away link anymore — a direct owner offers "Change owner" instead.
    expect(screen.queryByRole('link', { name: /Manage ownership/ })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Change owner' }));
    const modal = screen.getByTestId('assign-modal');
    expect(modal).toHaveAttribute('data-resource-type', 'workload');
    expect(modal).toHaveAttribute('data-resource-key', 'res-1');
  });

  it('warns when nobody owns the resource', async () => {
    resolveOwners.mockResolvedValue(respond(K8S_CHAIN, unowned, unowned, unowned));
    render(<OwnershipSection rec={k8sRec()} />);

    expect(await screen.findByText('No owner set yet')).toBeInTheDocument();
    expect(screen.queryByText('Ownership chain')).not.toBeInTheDocument();
    // An admin can still assign from scratch on an unowned resource.
    expect(screen.getByRole('button', { name: 'Assign owner' })).toBeInTheDocument();
  });

  it('hides the assign action from non-admins, leaving the card read-only', async () => {
    mockIsTenantAdmin.mockReturnValue(false);
    resolveOwners.mockResolvedValue(respond(K8S_CHAIN, owner(), unowned, unowned));
    render(<OwnershipSection rec={k8sRec()} />);

    expect(await screen.findByText('Assigned directly to this resource.')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /owner/i })).not.toBeInTheDocument();
  });

  it('resolves a cloud recommendation against cloud_resource, not workload', async () => {
    resolveOwners.mockResolvedValue(respond(CLOUD_CHAIN, owner({ owner_name: 'FinOps Team', owner_type: 'group', owner_id: 'g2' }), unowned));
    render(<OwnershipSection rec={k8sRec({ resource_k8s_namespace: '', resource_type: 'AWS::EC2::Instance' })} />);

    await screen.findByText('Assigned directly to this resource.');
    expect(resolveOwners).toHaveBeenCalledWith(
      [
        { resource_type: 'cloud_resource', resource_key: 'res-1' },
        { resource_type: 'cloud_account', resource_key: 'acct-1' },
      ],
      expect.any(AbortSignal)
    );
    // The modal targets cloud_resource (not workload) for a cloud recommendation.
    fireEvent.click(screen.getByRole('button', { name: 'Change owner' }));
    const modal = screen.getByTestId('assign-modal');
    expect(modal).toHaveAttribute('data-resource-type', 'cloud_resource');
    expect(modal).toHaveAttribute('data-resource-key', 'res-1');
    expect(modal).toHaveAttribute('data-cloud-account', 'acct-1');
  });

  it('leaves the Workload rung empty for a Pod recommendation rather than guessing', async () => {
    // resource_id points at the Pod, so no k8s_workloads row matches and the
    // workload ref comes back unowned — the namespace still answers.
    resolveOwners.mockResolvedValue(respond(K8S_CHAIN, unowned, owner(), unowned));
    render(<OwnershipSection rec={k8sRec({ resource_type: 'Pod' })} />);

    expect(await screen.findByText('Inherited from the namespace owner.')).toBeInTheDocument();
    expect(screen.getByText('Workload')).toBeInTheDocument();
  });

  it('renders nothing when the recommendation carries no resource id', () => {
    const { container } = render(<OwnershipSection rec={k8sRec({ resource_id: '' })} />);
    expect(container).toBeEmptyDOMElement();
    expect(resolveOwners).not.toHaveBeenCalled();
  });

  it('renders nothing when the resolve call fails, leaving the rest of the drawer intact', async () => {
    resolveOwners.mockRejectedValue(new Error('boom'));
    const { container } = render(<OwnershipSection rec={k8sRec()} />);
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it('maps each rung by (type, key) so a short response cannot shift owners onto the wrong level', async () => {
    // Namespace rung omitted entirely, cloud_account returned out of order. Index
    // matching would put the account's owner on the namespace rung.
    resolveOwners.mockResolvedValue([
      { ...CLOUD_CHAIN[1], ...owner({ owner_name: 'Account Owner' }) },
      { ...K8S_CHAIN[0], ...unowned },
    ]);
    render(<OwnershipSection rec={k8sRec()} />);

    expect(await screen.findByText('Inherited from the cloud account (cluster) owner.')).toBeInTheDocument();
    expect(screen.getAllByText('Account Owner')).toHaveLength(2); // Owner row + Cloud account rung
  });

  it('aborts the in-flight resolve when the drawer moves to another recommendation', async () => {
    resolveOwners.mockResolvedValue(respond(K8S_CHAIN, owner(), unowned, unowned));
    const { rerender } = render(<OwnershipSection rec={k8sRec({ resource_id: 'res-1' })} />);

    const firstSignal = resolveOwners.mock.calls[0][1] as AbortSignal;
    expect(firstSignal.aborted).toBe(false);

    rerender(<OwnershipSection rec={k8sRec({ resource_id: 'res-2' })} />);
    expect(firstSignal.aborted).toBe(true);
    expect((resolveOwners.mock.calls[1][1] as AbortSignal).aborted).toBe(false);
  });

  it('removes itself on unmount rather than leaving a card stuck on Resolving', async () => {
    resolveOwners.mockImplementation(() => new Promise(() => {})); // never settles
    const { container, rerender } = render(<OwnershipSection rec={k8sRec({ resource_id: 'res-1' })} />);
    expect(await screen.findByText('Resolving…')).toBeInTheDocument();

    // Same component instance, new resource: the previous request is retired.
    rerender(<OwnershipSection rec={k8sRec({ resource_id: '' })} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('ignores a late response for a recommendation the user already navigated away from', async () => {
    let resolveFirst: (v: unknown) => void = () => {};
    resolveOwners
      .mockImplementationOnce(() => new Promise((res) => (resolveFirst = res)))
      .mockResolvedValueOnce(respond(chainFor('res-2'), owner({ owner_name: 'Second Owner' }), unowned, unowned));

    const { rerender } = render(<OwnershipSection rec={k8sRec({ resource_id: 'res-1' })} />);
    rerender(<OwnershipSection rec={k8sRec({ resource_id: 'res-2' })} />);

    // Named twice — once in the Owner row, once in the chain's Workload rung.
    expect(await screen.findAllByText('Second Owner')).toHaveLength(2);
    // The stale first response lands last and must not overwrite the current one.
    resolveFirst(respond(K8S_CHAIN, owner({ owner_name: 'Stale Owner' }), unowned, unowned));
    await waitFor(() => expect(screen.queryByText('Stale Owner')).not.toBeInTheDocument());
    expect(screen.getAllByText('Second Owner')).toHaveLength(2);
  });
});
