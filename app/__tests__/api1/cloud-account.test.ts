import { extractGraphQLErrorMessage } from '@api1/cloud-account';

// extractGraphQLErrorMessage replaces the previous "Unknown error" UX when
// cloud_apply_command failed. The fixtures below mirror the actual envelopes
// observed in production for AWS UnauthorizedOperation, Azure AuthorizationFailed,
// and GCP 403 missing-permission — those three failure modes were the bug
// that prompted this helper.
describe('extractGraphQLErrorMessage', () => {
  it('returns Unknown error when response is null/undefined', () => {
    expect(extractGraphQLErrorMessage(undefined)).toBe('Unknown error');
    expect(extractGraphQLErrorMessage(null)).toBe('Unknown error');
    expect(extractGraphQLErrorMessage({})).toBe('Unknown error');
  });

  it('returns Unknown error when errors array is empty', () => {
    expect(extractGraphQLErrorMessage({ data: { errors: [] } })).toBe('Unknown error');
  });

  it('extracts AWS UnauthorizedOperation from Hasura action error envelope', () => {
    const response = {
      data: {
        errors: [
          {
            message: 'internal error',
            extensions: {
              internal: {
                response: {
                  body: [
                    {
                      message:
                        'Failed to apply command: failed to stop instances: operation error EC2: StopInstances, https response error StatusCode: 403, api error UnauthorizedOperation: You are not authorized to perform this operation.',
                    },
                  ],
                },
              },
            },
          },
        ],
      },
    };
    expect(extractGraphQLErrorMessage(response)).toContain('UnauthorizedOperation');
    expect(extractGraphQLErrorMessage(response)).toContain('StopInstances');
  });

  it('extracts GCP 403 missing-permission detail', () => {
    const response = {
      data: {
        errors: [
          {
            extensions: {
              internal: {
                response: {
                  body: [
                    {
                      message:
                        "Failed to apply command: failed to start instance: googleapi: Error 403: Required 'compute.instances.start' permission for 'projects/nudgebee-dev/zones/us-central1-c/instances/for-testing'",
                    },
                  ],
                },
              },
            },
          },
        ],
      },
    };
    expect(extractGraphQLErrorMessage(response)).toContain("'compute.instances.start' permission");
  });

  it('extracts Azure AuthorizationFailed detail', () => {
    const response = {
      data: {
        errors: [
          {
            extensions: {
              internal: {
                response: {
                  body: [
                    {
                      message:
                        "Failed to apply command: failed to deallocate VM: AuthorizationFailed: The client does not have authorization to perform action 'Microsoft.Compute/virtualMachines/deallocate/action'",
                    },
                  ],
                },
              },
            },
          },
        ],
      },
    };
    expect(extractGraphQLErrorMessage(response)).toContain('AuthorizationFailed');
    expect(extractGraphQLErrorMessage(response)).toContain('Microsoft.Compute/virtualMachines/deallocate');
  });

  it('falls back to nested object body shape (cloud-collector envelope without array)', () => {
    const response = {
      data: {
        errors: [
          {
            extensions: {
              internal: {
                response: {
                  body: {
                    errors: [{ message: 'cloud-collector said no' }],
                  },
                },
              },
            },
          },
        ],
      },
    };
    expect(extractGraphQLErrorMessage(response)).toBe('cloud-collector said no');
  });

  it('falls back to body.message when body is a single object', () => {
    const response = {
      data: {
        errors: [
          {
            extensions: {
              internal: {
                response: { body: { message: 'something specific' } },
              },
            },
          },
        ],
      },
    };
    expect(extractGraphQLErrorMessage(response)).toBe('something specific');
  });

  it('falls back to top-level error message when no inner body present', () => {
    const response = {
      data: {
        errors: [{ message: 'invalid jwt' }],
      },
    };
    expect(extractGraphQLErrorMessage(response)).toBe('invalid jwt');
  });
});
