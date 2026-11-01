// getUserAuthByTokenSha256 is the metastore read behind direct API-token auth.
// It has to distinguish "no such token" from "the lookup could not be
// performed" — queryGraphQL resolves rather than rejects on a transport
// failure, handing back `e.response || e.request`, so an unguarded read turns
// a metastore outage into a silent 401 on every API call.

jest.mock('@lib/HttpService', () => ({
  queryGraphQL: jest.fn(),
  gqlStringify: jest.fn(),
  unwrapGraphQL: jest.fn(),
}));

import { getUserAuthByTokenSha256 } from '@lib/UserService';
import { queryGraphQL } from '@lib/HttpService';

const mockQuery = queryGraphQL as jest.Mock;

function rowsResponse(rows: any[]) {
  return { data: { data: { users_get_auth_by_username: { rows } } }, status: 200 };
}

beforeEach(() => jest.clearAllMocks());

describe('getUserAuthByTokenSha256', () => {
  it('maps a hit into the nested shape the session builder expects', async () => {
    mockQuery.mockResolvedValue(
      rowsResponse([
        {
          auth_id: 'auth-1',
          auth_tenant_id: 'tenant-1',
          auth_status: 'active',
          expires_at: null,
          id: 'user-1',
          display_name: 'Ada',
          username: 'ada@example.com',
          user_status: 'active',
          user_roles: '[{"role":"tenant_admin","entity_type":"tenant","entity_id":"tenant-1"}]',
        },
      ])
    );

    const result = await getUserAuthByTokenSha256('abc123');

    expect(result.data.user_auths[0]).toMatchObject({
      id: 'auth-1',
      status: 'active',
      tenant_id: 'tenant-1',
      user: { id: 'user-1', username: 'ada@example.com', status: 'active' },
    });
    // json columns arrive as a string on some upstream builds.
    expect(result.data.user_auths[0].user.user_roles).toEqual([{ role: 'tenant_admin', entity_type: 'tenant', entity_id: 'tenant-1' }]);
    expect(mockQuery).toHaveBeenCalledWith(expect.any(String), 'GetUserAuthByTokenHash', {
      provider: 'token',
      hash: 'abc123',
    });
  });

  it('returns an empty row set for an unknown token', async () => {
    mockQuery.mockResolvedValue(rowsResponse([]));

    const result = await getUserAuthByTokenSha256('abc123');

    expect(result.errors).toBeUndefined();
    expect(result.data.user_auths).toEqual([]);
  });

  it('reports an error rather than an empty row set when the response has none', async () => {
    // queryGraphQL's catch returns `e.response || e.request` — undefined when a
    // server-side call fails with neither.
    mockQuery.mockResolvedValue(undefined);

    const result = await getUserAuthByTokenSha256('abc123');

    expect(result.errors).toHaveLength(1);
    expect(result.data).toBeUndefined();
  });

  it('reports an error when the response is a bare request object', async () => {
    // The other half of `e.response || e.request`: truthy, but carries no data.
    mockQuery.mockResolvedValue({ method: 'POST', path: '/api/graphql' });

    const result = await getUserAuthByTokenSha256('abc123');

    expect(result.errors).toHaveLength(1);
  });

  it('passes a gateway error through so the caller fails closed and logs it', async () => {
    mockQuery.mockResolvedValue({ data: { errors: [{ message: 'server_gateway_unavailable' }] }, status: 503 });

    const result = await getUserAuthByTokenSha256('abc123');

    expect(result.errors).toEqual([{ message: 'server_gateway_unavailable' }]);
  });
});
