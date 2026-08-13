// listWatchesByConversation latches the watch poll off on a 404 (per-env route unmounted) and stops calling.

const mockQueryGraphQL = jest.fn();
jest.mock('@lib/HttpService', () => ({
  queryGraphQL: (...args: any[]) => mockQueryGraphQL(...args),
  gqlStringify: (v: any) => JSON.stringify(v),
}));
jest.mock('@lib/auth', () => ({ getUserSession: jest.fn() }));

// Fresh module each call so the module-scope latch resets between scenarios.
function loadApi() {
  let api: any;
  jest.isolateModules(() => {
    api = require('@api1/ask-nudgebee').default;
  });
  return api;
}

describe('listWatchesByConversation — per-env route-404 latch', () => {
  beforeEach(() => mockQueryGraphQL.mockReset());

  it('returns watches when the route is available (feature on)', async () => {
    mockQueryGraphQL.mockResolvedValue({
      data: { data: { ai_list_watches_by_conversation: { data: { watches: [{ id: 'w1', status: 'ACTIVE' }] } } } },
    });
    const api = loadApi();
    await expect(api.listWatchesByConversation({ conversationId: 'c1' })).resolves.toEqual([{ id: 'w1', status: 'ACTIVE' }]);
    expect(mockQueryGraphQL).toHaveBeenCalledTimes(1);
  });

  it('latches off on a 404 and makes no further network calls (feature off for this env)', async () => {
    mockQueryGraphQL.mockResolvedValue({
      data: { errors: [{ message: 'not found', extensions: { upstream: { status: 404 } } }] },
    });
    const api = loadApi();
    await expect(api.listWatchesByConversation({ conversationId: 'c1' })).resolves.toEqual([]);
    await expect(api.listWatchesByConversation({ conversationId: 'c2' })).resolves.toEqual([]);
    expect(mockQueryGraphQL).toHaveBeenCalledTimes(1);
  });

  it('does NOT latch on a transient non-404 error (keeps polling)', async () => {
    mockQueryGraphQL.mockResolvedValue({
      data: { errors: [{ message: 'boom', extensions: { upstream: { status: 500 } } }] },
    });
    const api = loadApi();
    await api.listWatchesByConversation({ conversationId: 'c1' });
    await api.listWatchesByConversation({ conversationId: 'c1' });
    expect(mockQueryGraphQL).toHaveBeenCalledTimes(2);
  });

  it('does NOT latch when the call throws (transient network failure)', async () => {
    mockQueryGraphQL.mockRejectedValue(new Error('network down'));
    const api = loadApi();
    await expect(api.listWatchesByConversation({ conversationId: 'c1' })).resolves.toEqual([]);
    await api.listWatchesByConversation({ conversationId: 'c1' });
    expect(mockQueryGraphQL).toHaveBeenCalledTimes(2);
  });
});
