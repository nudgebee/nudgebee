// What the Status column says about an AWS account that the collector disabled
// because its CloudFormation stack was deleted or rolled back. The collector
// records the stack's Delete in connection_status.cf_stack of the AWS agent.

export interface CfStack {
  stack_name?: string;
  stack_status?: string;
}

/**
 * Tooltip explaining why a disabled account is disabled, or null when it was not
 * disabled by its stack being removed (for example, a user clicked Disable).
 */
export function stackRemovedReason(status: string | undefined, cfStack: CfStack | undefined | null): string | null {
  if (status !== 'disabled' || cfStack?.stack_status !== 'Delete_IN_PROGRESS') {
    return null;
  }
  // stack_name is stored as "<name>/<stack guid>"; only the name means anything to a reader.
  const name = cfStack.stack_name?.split('/')[0];
  const stack = name ? `"${name}"` : 'that connected this account';
  return `Disabled because the CloudFormation stack ${stack} was deleted or rolled back in AWS, which removed Nudgebee's access role. Add the account again to reconnect it.`;
}
