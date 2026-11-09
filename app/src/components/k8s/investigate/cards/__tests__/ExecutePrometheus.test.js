import apiKubernetes1 from '@api1/kubernetes1';
import ExecutePrometheus from '../ExecutePrometheus';

jest.mock('@components/k8s/details/KubernetesPrometheus', () => () => null);
jest.mock('@api1/kubernetes1', () => ({ __esModule: true, default: { consumePrometheusQueries: jest.fn() } }));

const HALF_HOUR_MS = 30 * 60 * 1000;
const ALERT_START_MS = Date.UTC(2025, 0, 15, 2, 48, 8);
const event = { starts_at: '2025-01-15T02:48:08Z', cloud_account_id: 'account-1' };

const chartRange = (startsAt) => {
  const card = new ExecutePrometheus({ data: [{ query: 'up == 0' }] }, { ...event, starts_at: startsAt });
  return card.renderPrometheusQueryResult().props.dateTime;
};

describe('ExecutePrometheus chart range', () => {
  // The API sends the first form. The chart reads a range it cannot use as "none given" and
  // shows the last hour, so an invalid date here is a wrong chart, not an error.
  [
    ['with a Z, as the API sends it', '2025-01-15T02:48:08Z'],
    ['with an offset', '2025-01-15T02:48:08+00:00'],
    ['without a zone', '2025-01-15T02:48:08'],
  ].forEach(([form, startsAt]) => {
    it(`covers 30 minutes either side of an alert start written ${form}`, () => {
      expect(chartRange(startsAt)).toEqual({
        startTime: ALERT_START_MS - HALF_HOUR_MS,
        endTime: ALERT_START_MS + HALF_HOUR_MS,
      });
    });
  });
});

// What api-server's application_performance_metrics action stores: the queries to run for the
// workload's language, and no results (api-server/services/observability/eventrule_actions_metrics.go).
const performanceEvidence = {
  additional_info: { action_name: 'application_performance_metrics', language: 'java' },
  data: [
    { title: 'Heap used', query: 'jvm_memory_used_bytes{area="heap"}' },
    { title: 'GC pause', query: 'rate(jvm_gc_pause_seconds_sum[5m])' },
  ],
};

// Answers the request with a payload per query, in the order the queries were asked.
const answerWith = (...payloads) => {
  apiKubernetes1.consumePrometheusQueries.mockImplementation(async (body) => ({
    data: { data: { metrics_list: { results: Object.keys(body.queries).map((key, i) => ({ query_key: key, payload: payloads[i] })) } } },
  }));
};
const series = [{ metric: { pod: 'api-7f9' }, timestamps: [1736909288], values: ['1'] }];

describe('ExecutePrometheus that requires data', () => {
  beforeEach(() => apiKubernetes1.consumePrometheusQueries.mockReset());

  it('asks for the time around the alert, on the alert account', async () => {
    answerWith(series, series);
    await new ExecutePrometheus(performanceEvidence, event, { requireData: true }).canRenderContent();
    const body = apiKubernetes1.consumePrometheusQueries.mock.calls[0][0];
    expect(body).toMatchObject({
      account_id: 'account-1',
      start_time: ALERT_START_MS - HALF_HOUR_MS,
      end_time: ALERT_START_MS + HALF_HOUR_MS,
      instant: false,
    });
    expect(Object.values(body.queries)).toEqual(performanceEvidence.data.map((q) => q.query));
  });

  it('is shown with only the queries that returned data', async () => {
    answerWith([], series);
    const card = new ExecutePrometheus(performanceEvidence, event, { requireData: true });
    expect(await card.canRenderContent()).toBe(true);
    expect(card.queries.map((q) => q.title)).toEqual(['GC pause']);
    expect(card.renderPrometheusQueryResult().props.queriesToExecute).toBe(card.queries);
  });

  // The page calls canRenderContent once when it builds the card and again when it places it.
  it('runs its queries once however often it is asked, and keeps the first answer', async () => {
    answerWith([], series);
    const card = new ExecutePrometheus(performanceEvidence, event, { requireData: true });
    expect(await card.canRenderContent()).toBe(true);
    apiKubernetes1.consumePrometheusQueries.mockRejectedValue(new Error('agent not connected'));
    expect(await card.canRenderContent()).toBe(true);
    expect(card.queries.map((q) => q.title)).toEqual(['GC pause']);
    expect(apiKubernetes1.consumePrometheusQueries).toHaveBeenCalledTimes(1);
  });

  it('is not shown when no query returned data', async () => {
    answerWith([], undefined);
    const card = new ExecutePrometheus(performanceEvidence, event, { requireData: true });
    expect(await card.canRenderContent()).toBe(false);
  });

  it('is not shown when the request fails', async () => {
    apiKubernetes1.consumePrometheusQueries.mockRejectedValue(new Error('agent not connected'));
    expect(await new ExecutePrometheus(performanceEvidence, event, { requireData: true }).canRenderContent()).toBe(false);

    // The API layer resolves a failed call to the response it got, or to nothing.
    apiKubernetes1.consumePrometheusQueries.mockResolvedValue(undefined);
    expect(await new ExecutePrometheus(performanceEvidence, event, { requireData: true }).canRenderContent()).toBe(false);
  });

  it('is not shown, and asks nothing, when the evidence has no queries', async () => {
    const card = new ExecutePrometheus({ ...performanceEvidence, data: [] }, event, { requireData: true });
    expect(await card.canRenderContent()).toBe(false);
    expect(apiKubernetes1.consumePrometheusQueries).not.toHaveBeenCalled();
  });
});

describe('ExecutePrometheus that does not require data', () => {
  beforeEach(() => apiKubernetes1.consumePrometheusQueries.mockReset());

  it('is shown without running its queries first', async () => {
    const card = new ExecutePrometheus({ data: [{ query: 'up == 0' }] }, event);
    expect(await card.canRenderContent()).toBe(true);
    expect(card.queries).toHaveLength(1);
    expect(apiKubernetes1.consumePrometheusQueries).not.toHaveBeenCalled();
  });
});
