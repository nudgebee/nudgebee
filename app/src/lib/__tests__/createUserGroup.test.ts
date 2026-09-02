// createUserGroup's contract, pinned.
//
// Two things went wrong here in production and one in review, all about the
// SHAPE of what this returns:
//   * it used to return the GraphQL error envelope on failure, so a failed
//     create was indistinguishable from a successful one — GroupModal read an
//     undefined id without an exception, toasted "Group added successfully",
//     then called usergroups_update_members with group_id undefined, surfacing
//     a 401 as `missing_required_variable:$group_id`;
//   * the api1 wrapper re-wrapped the result one level deeper, so the caller
//     had to read `result.data.data.id` — correct, but it reads like a bug and
//     was reported as one.
// Both are now fixed; these tests are what stops either from coming back.

jest.mock('@lib/cache', () => ({
  __esModule: true,
  default: { get: jest.fn(), set: jest.fn(), del: jest.fn(), delWithSuffix: jest.fn(), getWithSuffix: jest.fn(), setWithSuffix: jest.fn() },
}));

const queryGraphQL = jest.fn();
jest.mock('@lib/HttpService', () => {
  const actual = jest.requireActual('@lib/HttpService');
  return { ...actual, queryGraphQL: (...args: unknown[]) => queryGraphQL(...args) };
});

import { createUserGroup } from '@lib/UserService';

describe('createUserGroup', () => {
  beforeEach(() => queryGraphQL.mockReset());

  it('returns the created group one level deep, so callers read result.data.id', async () => {
    queryGraphQL.mockResolvedValue({ data: { data: { usergroup_create: { id: 'g-1' } } } });
    const result = await createUserGroup('platform', 'desc');
    expect(result.data).toEqual({ id: 'g-1' });
    expect(result.data?.id).toBe('g-1');
  });

  // The regression that produced the false "Group added successfully": a handler
  // error must not come back as a resolved value the caller has to remember to
  // inspect.
  it('throws the handler message instead of resolving with an error envelope', async () => {
    queryGraphQL.mockResolvedValue({ data: { errors: [{ message: 'Not Allowed' }] } });
    await expect(createUserGroup('platform', 'desc')).rejects.toThrow('Not Allowed');
  });

  it('falls back to a named error when the envelope carries no message', async () => {
    queryGraphQL.mockResolvedValue({ data: { errors: [{}] } });
    await expect(createUserGroup('platform', 'desc')).rejects.toThrow('Failed to create group');
  });
});
