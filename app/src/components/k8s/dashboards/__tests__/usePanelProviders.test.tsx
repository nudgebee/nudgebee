import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { usePanelProviders } from '../panelProviders';
import type { AccountOption } from '@api1/dashboards';

interface ProviderAnswer {
  data: { data: { observability_get_default_provider: { provider: string; available_providers: { provider: string }[] } } };
}

const answer = (provider: string, available: string[]): Promise<ProviderAnswer> =>
  Promise.resolve({
    data: { data: { observability_get_default_provider: { provider, available_providers: available.map((p) => ({ provider: p })) } } },
  });

const getDefaultProvider = jest.fn((req: { provider_type: string }) => answer(`${req.provider_type}-provider`, []));

const answerWith = (provider: string, available: string[]) => getDefaultProvider.mockImplementation(() => answer(provider, available));

jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: (req: any) => getDefaultProvider(req) } }));
// Cache hits would mask the refetch these tests are about.
jest.mock('@lib/cache', () => ({ __esModule: true, default: { get: () => null, set: () => {} } }));

const accounts: AccountOption[] = [{ label: 'a1', value: 'a1', cloud_provider: 'K8S' }];

afterEach(cleanup);
beforeEach(() => getDefaultProvider.mockClear());

describe('usePanelProviders', () => {
  it('drops the previous answer when the datasource changes', async () => {
    // Holding it would render metrics badges under a logs panel, captioned with
    // the new type — wrong information, which is what this row exists to prevent.
    const { result, rerender } = renderHook(({ type }) => usePanelProviders(accounts, type), {
      initialProps: { type: 'metrics' as 'metrics' | 'logs' },
    });
    await waitFor(() => expect(result.current.entries).toHaveLength(1));
    expect(result.current.entries[0].provider).toBe('metrics-provider');

    await act(async () => {
      rerender({ type: 'logs' });
    });
    await waitFor(() => expect(result.current.entries[0].provider).toBe('logs-provider'));
    expect(getDefaultProvider).toHaveBeenLastCalledWith(expect.objectContaining({ provider_type: 'logs' }));
  });

  it('keeps the previous answer across an account change, so the row does not flash', async () => {
    const { result, rerender } = renderHook(({ list }) => usePanelProviders(list, 'metrics'), {
      initialProps: { list: accounts },
    });
    await waitFor(() => expect(result.current.entries).toHaveLength(1));

    const two = [...accounts, { label: 'a2', value: 'a2', cloud_provider: 'K8S' }];
    rerender({ list: two });
    // Still the old entries while the new lookup is in flight — same question.
    expect(result.current.loading).toBe(true);
    expect(result.current.entries).toHaveLength(1);
    await waitFor(() => expect(result.current.entries).toHaveLength(2));
  });

  it('resolves nothing for a datasource with no provider', async () => {
    const { result } = renderHook(() => usePanelProviders(accounts, undefined));
    expect(result.current).toEqual({ loading: false, entries: [], total: 0 });
    expect(getDefaultProvider).not.toHaveBeenCalled();
  });
});

describe('AWS metrics accounts', () => {
  const aws: AccountOption[] = [{ label: 'dev-aws', value: 'dev-aws', cloud_provider: 'AWS' }];

  it('still offers the account’s other configured providers alongside CloudWatch', async () => {
    // dev-aws has Datadog configured. CloudWatch is what an undeclared panel
    // queries, but Datadog is a legitimate choice and must stay selectable —
    // resolving CloudWatch ALONE would hide every provider the account has.
    answerWith('datadog', ['datadog']);
    const { result } = renderHook(() => usePanelProviders(aws, 'metrics'));
    await waitFor(() => expect(result.current.entries).toHaveLength(1));
    expect(result.current.entries[0].provider).toBe('aws_cloudwatch');
    expect(result.current.entries[0].available).toEqual(['aws_cloudwatch', 'datadog']);
  });

  it('asks the server rather than short-circuiting on the cloud provider', async () => {
    answerWith('datadog', ['datadog']);
    renderHook(() => usePanelProviders(aws, 'metrics'));
    await waitFor(() => expect(getDefaultProvider).toHaveBeenCalledWith(expect.objectContaining({ account_id: 'dev-aws' })));
  });

  it('reports CloudWatch even when the account has no metrics integration at all', async () => {
    // It is not an integration type, so the server can never name it — but
    // usePanelData forces it, which makes it this account's real default.
    answerWith('', []);
    const { result } = renderHook(() => usePanelProviders(aws, 'metrics'));
    await waitFor(() => expect(result.current.entries).toHaveLength(1));
    expect(result.current.entries[0].available).toEqual(['aws_cloudwatch']);
  });

  it('leaves logs and traces alone — CloudWatch is folded in for metrics only', async () => {
    answerWith('ES', ['ES']);
    const { result } = renderHook(() => usePanelProviders(aws, 'logs'));
    await waitFor(() => expect(result.current.entries).toHaveLength(1));
    expect(result.current.entries[0].provider).toBe('ES');
    expect(result.current.entries[0].available).toEqual(['ES']);
  });
});
