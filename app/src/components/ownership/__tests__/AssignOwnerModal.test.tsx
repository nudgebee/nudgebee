import '@testing-library/jest-dom';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import AssignOwnerModal from '@components/ownership/AssignOwnerModal';
import apiOwnership from '@api1/ownership';

/*
 * The dialog every surface assigns owners through. The backend stores an owner for
 * any key, and the resolver ignores one stored on a resource it does not hold as
 * active — so a save on such a resource reported "Owner assigned" and left the
 * resource unowned. The dialog is where that is refused, for every host at once.
 */

jest.mock('@api1/ownership', () => ({
  __esModule: true,
  default: { getOwner: jest.fn(), assignOwner: jest.fn(), removeOwner: jest.fn() },
}));

jest.mock('@lib/auth', () => ({ __esModule: true, isTenantAdmin: jest.fn(() => true) }));

// The picker loads the tenant's users and groups; here it is one button that picks a user.
jest.mock('@components/ownership/OwnerPicker', () => ({
  __esModule: true,
  default: (props: { onChange: (v: { ownerType: string; ownerId: string }) => void }) => (
    <button type='button' data-testid='pick-owner' onClick={() => props.onChange({ ownerType: 'user', ownerId: 'u1' })} />
  ),
}));

const getOwner = apiOwnership.getOwner as jest.Mock;
const assignOwner = apiOwnership.assignOwner as jest.Mock;

const renderModal = (onChange = jest.fn()) =>
  render(
    <AssignOwnerModal
      open
      onClose={jest.fn()}
      resourceType='workload'
      resourceKey='res-1'
      cloudAccountId='acct-1'
      resourceLabel='api'
      onChange={onChange}
    />
  );

const save = () => screen.getByRole('button', { name: 'Save' });

describe('AssignOwnerModal', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    assignOwner.mockResolvedValue({ status: 'ok' });
  });

  it('assigns the picked owner to an active resource nobody owns', async () => {
    getOwner.mockResolvedValue({ resource_type: 'workload', resource_key: 'res-1', found: false });
    const onChange = jest.fn();
    renderModal(onChange);

    await waitFor(() => expect(screen.getByTestId('pick-owner')).toBeInTheDocument());
    expect(screen.queryByTestId('assign-owner-not-active')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId('pick-owner'));
    await waitFor(() => expect(save()).toBeEnabled());
    fireEvent.click(save());

    await waitFor(() => expect(onChange).toHaveBeenCalled());
    expect(assignOwner).toHaveBeenCalledWith({
      resourceType: 'workload',
      resourceKey: 'res-1',
      ownerType: 'user',
      ownerId: 'u1',
      cloudAccountId: 'acct-1',
    });
  });

  it('refuses a resource that is no longer active, and says why', async () => {
    getOwner.mockResolvedValue({ resource_type: 'workload', resource_key: 'res-1', found: false, no_active_resource: true });
    renderModal();

    await waitFor(() => expect(screen.getByTestId('assign-owner-not-active')).toBeInTheDocument());
    expect(screen.getByTestId('assign-owner-not-active')).toHaveTextContent('no longer active');
    expect(screen.queryByTestId('pick-owner')).not.toBeInTheDocument();
    expect(save()).toBeDisabled();
    expect(assignOwner).not.toHaveBeenCalled();
  });

  it('holds Save until it knows whether the resource is active', async () => {
    let answer: (value: unknown) => void = () => {};
    getOwner.mockReturnValue(new Promise((resolve) => (answer = resolve)));
    renderModal();

    fireEvent.click(screen.getByTestId('pick-owner'));
    expect(save()).toBeDisabled();

    answer({ resource_type: 'workload', resource_key: 'res-1', found: false });
    await waitFor(() => expect(save()).toBeEnabled());
  });
});
