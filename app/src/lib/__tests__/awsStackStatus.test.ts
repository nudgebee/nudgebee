import { stackRemovedReason } from '@lib/awsStackStatus';

// An AWS account whose CloudFormation stack failed and rolled back kept showing
// Active although the rollback deleted Nudgebee's role (#42643). The collector now
// disables it; the Status column has to say why.
describe('stackRemovedReason', () => {
  const removed = { stack_name: 'connectToNudgebee-1/5f1c2a90-guid', stack_status: 'Delete_IN_PROGRESS' };

  it('explains a disabled account whose stack was removed', () => {
    expect(stackRemovedReason('disabled', removed)).toContain('"connectToNudgebee-1" was deleted or rolled back');
    expect(stackRemovedReason('disabled', { stack_status: 'Delete_IN_PROGRESS' })).toContain('Add the account again');
  });

  it('says nothing once the account is enabled again', () => {
    expect(stackRemovedReason('active', removed)).toBeNull();
  });

  it('says nothing for an account disabled for another reason', () => {
    expect(stackRemovedReason('disabled', { stack_status: 'Create_IN_PROGRESS' })).toBeNull();
    expect(stackRemovedReason('disabled', undefined)).toBeNull();
  });
});
