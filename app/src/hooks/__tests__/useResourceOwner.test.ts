import { renderHook, waitFor, act } from '@testing-library/react';
import useResourceOwner, { derivedText, sourceText, type ChainLevel } from '../useResourceOwner';
import apiOwnership from '@api1/ownership';

jest.mock('@api1/ownership', () => ({ __esModule: true, default: { resolveOwners: jest.fn() } }));

const resolveOwners = apiOwnership.resolveOwners as jest.Mock;

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

const chain: ChainLevel[] = [
  { level: 'Workload', resourceType: 'workload', resourceKey: 'res-1', own: null },
  { level: 'Namespace', resourceType: 'namespace', resourceKey: 'acct-1/nudgebee', own: null },
  { level: 'Cloud account', resourceType: 'cloud_account', resourceKey: 'acct-1', own: null },
];

// Responses are matched back by (resource_type, resource_key), so mocks must carry
// them exactly as the real API does.
const respond = (levels: ChainLevel[], ...results: object[]) =>
  results.map((r, i) => ({ resource_type: levels[i].resourceType, resource_key: levels[i].resourceKey, ...r }));

beforeEach(() => resolveOwners.mockReset());

describe('useResourceOwner', () => {
  it('reports the most specific rung that owns the resource', async () => {
    resolveOwners.mockResolvedValue(respond(chain, unowned, owner(), owner({ owner_name: 'Platform Guild' })));
    const { result } = renderHook(() => useResourceOwner(chain));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.effectiveIndex).toBe(1);
    expect(result.current.effective?.owner_name).toBe('Mangglesh Dagar');
  });

  it('restates `via` relative to the resource, not the rung that owns it', async () => {
    // The namespace rung owns itself (via self); from the workload's point of view
    // that is inheritance, so the badge must say `namespace`.
    resolveOwners.mockResolvedValue(respond(chain, unowned, owner(), unowned));
    const { result } = renderHook(() => useResourceOwner(chain));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.effective?.via).toBe('namespace');
  });

  it('calls a rung "self" when the resource itself is owned', async () => {
    resolveOwners.mockResolvedValue(respond(chain, owner(), unowned, unowned));
    const { result } = renderHook(() => useResourceOwner(chain));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.effective?.via).toBe('self');
  });

  it('treats an inherited result as not owning the rung it was reported on', async () => {
    // Every rung answers "found", but only via inheritance — nothing owns anything.
    resolveOwners.mockResolvedValue(respond(chain, owner({ via: 'namespace' }), owner({ via: 'cluster' }), unowned));
    const { result } = renderHook(() => useResourceOwner(chain));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.effectiveIndex).toBe(-1);
    expect(result.current.effective).toBeNull();
  });

  it('matches responses by key so a short result set cannot shift owners onto the wrong rung', async () => {
    // Namespace omitted, cloud_account returned first. Index matching would put the
    // account's owner on the workload rung and report the resource as directly owned.
    resolveOwners.mockResolvedValue([
      { resource_type: 'cloud_account', resource_key: 'acct-1', ...owner({ owner_name: 'Account Owner' }) },
      { resource_type: 'workload', resource_key: 'res-1', ...unowned },
    ]);
    const { result } = renderHook(() => useResourceOwner(chain));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.effectiveIndex).toBe(2);
    expect(result.current.effective?.owner_name).toBe('Account Owner');
    expect(result.current.effective?.via).toBe('cluster');
  });

  it('resolves nothing and does not call the API for an empty chain', async () => {
    const { result } = renderHook(() => useResourceOwner([]));

    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(resolveOwners).not.toHaveBeenCalled();
    expect(result.current.effective).toBeNull();
  });

  it('reports unresolvable when the resolve rejects', async () => {
    resolveOwners.mockRejectedValue(new Error('boom'));
    const { result } = renderHook(() => useResourceOwner(chain));

    await waitFor(() => expect(result.current.unresolvable).toBe(true));
    expect(result.current.loading).toBe(false);
  });

  it('does not refetch when given an equivalent chain in a fresh array', async () => {
    resolveOwners.mockResolvedValue(respond(chain, owner(), unowned, unowned));
    const { result, rerender } = renderHook(({ c }) => useResourceOwner(c), { initialProps: { c: chain } });

    await waitFor(() => expect(result.current.loading).toBe(false));
    rerender({ c: chain.map((l) => ({ ...l })) });

    expect(resolveOwners).toHaveBeenCalledTimes(1);
  });

  it('aborts the in-flight resolve when the chain changes', async () => {
    resolveOwners.mockResolvedValue(respond(chain, owner(), unowned, unowned));
    const { rerender } = renderHook(({ c }) => useResourceOwner(c), { initialProps: { c: chain } });

    const firstSignal = resolveOwners.mock.calls[0][1] as AbortSignal;
    expect(firstSignal.aborted).toBe(false);

    const other = chain.map((l) => ({ ...l, resourceKey: `${l.resourceKey}-b` }));
    rerender({ c: other });

    expect(firstSignal.aborted).toBe(true);
    expect((resolveOwners.mock.calls[1][1] as AbortSignal).aborted).toBe(false);
  });

  it('ignores a late response for a chain the caller has already moved off', async () => {
    const secondChain: ChainLevel[] = chain.map((l, i) => (i === 0 ? { ...l, resourceKey: 'res-2' } : { ...l }));
    let settleFirst: (v: unknown) => void = () => {};
    resolveOwners
      .mockImplementationOnce(() => new Promise((res) => (settleFirst = res)))
      .mockResolvedValueOnce(respond(secondChain, owner({ owner_name: 'Second Owner' }), unowned, unowned));

    const { result, rerender } = renderHook(({ c }) => useResourceOwner(c), { initialProps: { c: chain } });
    rerender({ c: secondChain });

    await waitFor(() => expect(result.current.effective?.owner_name).toBe('Second Owner'));

    // The stale first response lands last and must not overwrite the current one.
    await act(async () => {
      settleFirst(respond(chain, owner({ owner_name: 'Stale Owner' }), unowned, unowned));
    });
    expect(result.current.effective?.owner_name).toBe('Second Owner');
  });
});

// derivedText moved here with the function it tests — every ownership surface words
// its explanation through this, so it must stay covered.
describe('derivedText', () => {
  const levels = (source: string) => [
    { level: 'Workload', resourceType: 'workload', resourceKey: 'k', own: { found: true, source } },
    { level: 'Namespace', resourceType: 'namespace', resourceKey: 'k', own: null },
  ];

  it('distinguishes a direct assignment from a rule match', () => {
    expect(derivedText(levels('manual'), 0)).toBe('Assigned directly to this resource.');
    expect(derivedText(levels('rule'), 0)).toBe('Matched by an ownership rule.');
  });

  it('names the level an owner was inherited from', () => {
    expect(derivedText(levels('manual'), 1)).toBe('Inherited from the namespace owner.');
    expect(derivedText([...levels('manual'), { level: 'Cloud account', resourceType: 'cloud_account', resourceKey: 'a', own: null }], 2)).toBe(
      'Inherited from the cloud account (cluster) owner.'
    );
  });

  it('reports no owner when nothing in the chain owns the resource', () => {
    expect(derivedText(levels('manual'), -1)).toBe('No owner assigned yet.');
  });
});

// sourceText is what narrow surfaces show instead of the chain, so it has to carry the
// rung as well as the mechanism — that is the whole reason it exists separately.
describe('sourceText', () => {
  const chainOwnedAt = (index: number, source = 'manual'): ChainLevel[] =>
    [
      { level: 'Workload', resourceType: 'workload', resourceKey: 'k', own: null },
      { level: 'Namespace', resourceType: 'namespace', resourceKey: 'a/n', own: null },
      { level: 'Cloud account', resourceType: 'cloud_account', resourceKey: 'a', own: null },
    ].map((l, i) => (i === index ? { ...l, own: { found: true, source } } : l));

  it('names the rung an inherited owner came from', () => {
    expect(sourceText(chainOwnedAt(1), 1)).toBe('Inherited from the namespace owner');
    expect(sourceText(chainOwnedAt(2), 2)).toBe('Inherited from the cloud account owner');
  });

  it('names the rule and the rung it applied at', () => {
    expect(sourceText(chainOwnedAt(1, 'rule'), 1)).toBe('Ownership rule, at namespace level');
  });

  it('distinguishes a direct assignment from a rule on the resource itself', () => {
    expect(sourceText(chainOwnedAt(0), 0)).toBe('Assigned directly to this resource');
    expect(sourceText(chainOwnedAt(0, 'rule'), 0)).toBe('Ownership rule, on this resource');
  });

  it('says plainly that nothing covers the resource when unowned', () => {
    expect(sourceText(chainOwnedAt(0), -1)).toBe('No rule or assignment covers this resource');
  });
});
