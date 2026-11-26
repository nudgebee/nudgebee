import apiTrace from '@api1/kubernetes/trace';
import TracesCard from '../TracesCard';

jest.mock('@api1/kubernetes/trace', () => ({ __esModule: true, default: { traceV2: jest.fn() } }));
jest.mock('@components/k8s/details/KubernetesTracesListing', () => () => null);
jest.mock('@components/k8s/common/KubernetesTraceServiceOperation', () => ({ KubernetesTraceServiceOperation: () => null }));

describe('TracesCard.fetchTraceData', () => {
  beforeEach(() => apiTrace.traceV2.mockResolvedValue({ traces_counts_v3: { count: 0 } }));

  it.each(['2025-01-15T02:48:08Z', '2025-01-15T02:48:08+05:30'])('builds a valid 10-minute query window for %s', async (starts_at) => {
    const event = { starts_at, cloud_account_id: 'account-1' };
    const card = new TracesCard({}, event, 0);
    await card.fetchTraceData(event, {
      srcNamespace: [],
      srcWorkload: [],
      destinationWorkloadNamespace: [],
      destinationWorkloadNames: [],
    });

    const { startDate, endDate } = apiTrace.traceV2.mock.calls.at(-1)[0];
    const eventTime = new Date(starts_at).getTime();
    expect(Date.parse(startDate)).toBe(eventTime - 10 * 60 * 1000);
    expect(Date.parse(endDate)).toBe(eventTime + 10 * 60 * 1000);
  });
});
