import React from 'react';
import { render, screen } from '@testing-library/react';
import TracesCard from '@components/k8s/investigate/cards/TracesCard';

// The span views are exercised by their own tests; stub them so these assertions are
// about the card's provenance header, not the trace table render.
jest.mock('@components/k8s/details/KubernetesTracesListing', () => ({
  __esModule: true,
  default: () => React.createElement('div', { 'data-testid': 'traces-listing' }),
}));
jest.mock('@components/k8s/common/KubernetesTraceServiceOperation', () => ({
  __esModule: true,
  KubernetesTraceServiceOperation: () => React.createElement('div', { 'data-testid': 'trace-service-operation' }),
}));

// Shape of a `traces` evidence as the api-server enrichers store it on the event: the
// spans under data, and the query GetTraces actually ran (plus the resolved provider)
// under additional_info — the same two keys the log cards read.
const traceEvidence = (additionalInfo) => ({
  type: 'json',
  additional_info: { action_name: 'traces', ...additionalInfo },
  data: JSON.stringify({
    data: [
      {
        timestamp: '2026-09-01T10:42:03.711Z',
        trace_id: '17b6ab73546fffbb4bcebf99dc89e880',
        span_id: '24578080047015802',
        parent_span_id: '',
        span_name: 'GET /cart',
        span_kind: 'SPAN_KIND_SERVER',
        service_name: 'cart',
        status_code: 'STATUS_CODE_ERROR',
        duration_ns: 12000000,
        span_attributes: {},
      },
    ],
  }),
});

const event = {
  id: 'event-under-test',
  subject_type: 'deployment',
  subject_name: 'cart',
  subject_namespace: 'shop',
  evidences: [],
};

const renderCardContent = async (evidence) => {
  const card = new TracesCard(evidence, event, 0);
  expect(await card.canRenderContent()).toBe(true);
  const [Content] = card.getContentComponents();
  return render(<Content />);
};

describe('TracesCard executed query', () => {
  it('shows the executed provider query and the provider alongside the spans', async () => {
    const executedQuery = "SELECT * FROM traces_v2 WHERE workload_name='cart' AND status_code='Error'";
    await renderCardContent(traceEvidence({ executed_query: executedQuery, provider: 'clickhouse' }));

    expect(screen.getByTestId('evidence-executed-query')).toBeInTheDocument();
    expect(screen.getByText(executedQuery)).toBeInTheDocument();
    expect(screen.getByText('clickhouse')).toBeInTheDocument();
  });

  it('still renders the spans for evidence stored before the query was recorded', async () => {
    await renderCardContent(traceEvidence({}));

    expect(screen.queryByTestId('evidence-executed-query')).not.toBeInTheDocument();
  });
});
