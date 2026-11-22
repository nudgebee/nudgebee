import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import KubernetesTracesListing from '@components/k8s/details/KubernetesTracesListing';
import apiTrace from '@api1/kubernetes/trace';
import apiAccount from '@api1/account';

const mockRouter = { push: jest.fn(), query: { KubernetesDetails: 'account-1' } };
jest.mock('next/router', () => ({ useRouter: () => mockRouter }));
jest.mock('next/dynamic', () => () => () => null);
jest.mock('@api1/kubernetes/trace', () => ({
  __esModule: true,
  toFilterList: jest.requireActual('@api1/kubernetes/trace').toFilterList,
  default: { traceV2: jest.fn(), traceDistinctFilters: jest.fn(async () => ({})), traceDistinctWorloadAndNamespace: jest.fn(async () => ({})) },
}));
jest.mock('@api1/account', () => ({ __esModule: true, default: { getDefaultProvider: jest.fn() } }));
jest.mock('@api1/observability', () => ({ __esModule: true, default: { logIndexList: jest.fn(async () => ({})) } }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { listFrameworkResources: jest.fn(async () => ({})) } }));
jest.mock('@api1/user', () => ({ __esModule: true, default: { getUserPreferencesTablePageSize: () => 10 } }));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({ assistantName: 'Assistant' }), getNubiIconUrl: () => '' }));
jest.mock('@context/NubiGlobalChatContext', () => ({ useNubiGlobalChat: () => mockChat }));
// Stable references: the listing re-initialises whenever selectedCluster changes identity.
const mockData = { selectedCluster: {} };
const mockChat = {};
jest.mock('@context/DataContext', () => ({ useData: () => mockData }));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => () => null);
jest.mock('@components/k8s/details/KubernetesLogs', () => () => null);
jest.mock('@components/k8s/common/CloudTraceLogs', () => () => null);
jest.mock('@components/k8s/common/KubernetesTraceServiceOperation', () => ({ KubernetesTraceServiceOperation: () => null }));
jest.mock('@ui/ToggleGroup', () => ({
  ToggleGroup: ({ options, onChange }: any) => (
    <div>
      {options.map((o: any) => (
        <button key={o.value} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  ),
}));
// The table is reduced to what this suite asserts on: which headers are sortable, the
// sort it is shown, and a way to pick the Duration sort.
jest.mock('@shared/tables/CustomTable', () => ({ headers, sort, onSortChange }: any) => (
  <div>
    <div data-testid='sortable'>
      {headers
        .filter((h: any) => h?.sortEnabled)
        .map((h: any) => h.name)
        .join(',')}
    </div>
    <div data-testid='sort'>{sort?.name ? `${sort.name} ${sort.order}` : ''}</div>
    <button onClick={() => onSortChange({ name: 'Duration', order: 'asc' })}>sort-duration</button>
  </div>
));

// The capability drives the behaviour, not the provider name — so these fixtures vary the
// capability and deliberately keep the provider name irrelevant.
const providerResponse = (provider: string, capabilities?: Record<string, unknown>) => ({
  data: { data: { observability_get_default_provider: { provider, capabilities } } },
});
const lastTraceRequest = () => (apiTrace.traceV2 as jest.Mock).mock.calls.at(-1)[0];

describe('KubernetesTracesListing sort by view', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    (apiTrace.traceV2 as jest.Mock).mockResolvedValue({ traces_list: [], traces_counts: { count: 0 } });
  });

  // #41495: a provider that ranks By Traces pages by each trace's newest span lists them
  // newest first only and 400s any other order, which the table showed as "No Data
  // Available". Splunk Enterprise is the one that declares it today.
  it('does not offer or send the Duration sort in By Traces when the provider declares the constraint, and keeps it for By Spans', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(providerResponse('splunk_enterprise', { by_trace_natural_order_only: true }));
    render(<KubernetesTracesListing accountId='account-1' />);
    await waitFor(() => expect(apiTrace.traceV2).toHaveBeenCalled());

    fireEvent.click(screen.getByText('sort-duration'));
    await waitFor(() => expect(lastTraceRequest()).toMatchObject({ byTrace: false, sortCol: 'duration_ns', sortOrder: 'asc' }));

    fireEvent.click(screen.getByText('By Traces'));
    await waitFor(() => expect(lastTraceRequest()).toMatchObject({ byTrace: true, sortCol: 'timestamp', sortOrder: 'desc' }));
    expect(screen.getByTestId('sortable')).toHaveTextContent(/^$/);
    expect(screen.getByTestId('sort')).toHaveTextContent('Timestamp desc');

    fireEvent.click(screen.getByText('By Spans'));
    await waitFor(() => expect(lastTraceRequest()).toMatchObject({ byTrace: false, sortCol: 'duration_ns', sortOrder: 'asc' }));
    expect(screen.getByTestId('sortable')).toHaveTextContent('Duration');
  });

  it('keeps the Duration sort in By Traces when the provider declares the constraint false', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(providerResponse('jaeger', { by_trace_natural_order_only: false }));
    render(<KubernetesTracesListing accountId='account-1' />);
    await waitFor(() => expect(apiTrace.traceV2).toHaveBeenCalled());

    fireEvent.click(screen.getByText('By Traces'));
    fireEvent.click(screen.getByText('sort-duration'));
    await waitFor(() => expect(lastTraceRequest()).toMatchObject({ byTrace: true, sortCol: 'duration_ns', sortOrder: 'asc' }));
    expect(screen.getByTestId('sortable')).toHaveTextContent('Duration');
  });

  // The field is omitempty on the wire and the GraphQL type is nullable, so an api-server
  // that predates it sends no capabilities at all. That must read as "order it however you
  // like" — the behaviour every provider but one has — not as a silently disabled sort.
  it('offers the Duration sort in By Traces when the capability is absent', async () => {
    (apiAccount.getDefaultProvider as jest.Mock).mockResolvedValue(providerResponse('splunk_enterprise'));
    render(<KubernetesTracesListing accountId='account-1' />);
    await waitFor(() => expect(apiTrace.traceV2).toHaveBeenCalled());

    fireEvent.click(screen.getByText('By Traces'));
    fireEvent.click(screen.getByText('sort-duration'));
    await waitFor(() => expect(lastTraceRequest()).toMatchObject({ byTrace: true, sortCol: 'duration_ns', sortOrder: 'asc' }));
    expect(screen.getByTestId('sortable')).toHaveTextContent('Duration');
  });
});
