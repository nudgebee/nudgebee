import TracesCard from '@components/k8s/investigate/cards/TracesCard';

/**
 * Issue #34377 — the Traces card never appeared for GCP Cloud SQL events.
 *
 * The cause was dispatch, not rendering: investigate.jsx mapped the plain
 * 'traces' action type to TracesCard only inside its `isK8s` branch, while a
 * GCP account resolves to source 'cloud'. 154 Cloud SQL events carried span
 * evidence under action_name 'traces' that the UI dropped on the floor; only
 * Cloud Run rendered, because it happens to use the 'cloud_traces' name.
 *
 * These tests pin the other half of that contract — that the card genuinely
 * populates from a cloud span payload, so routing 'traces' to it produces a
 * real card rather than an empty one. The payload below is shaped from a real
 * cloudsql_database CPU-alert event, with identifiers replaced by examples.
 */

// Shaped like the real evidence: two of the 45 "Cloud SQL Query" spans.
const gcpCloudSqlSpans = {
  data: [
    {
      timestamp: '2026-08-18T11:08:49.589Z',
      trace_id: '17b6ab73546fffbb4bcebf99dc89e880',
      span_id: '24578080047015802',
      parent_span_id: '',
      span_name: 'Cloud SQL Query',
      span_kind: 'SPAN_KIND_UNSPECIFIED',
      service_name: 'sample-service',
      duration_ns: 12000000,
      span_attributes: {
        database: 'appdb',
        'gcp.project_id': 'example-project',
        instance: 'example-project:sample-db',
      },
    },
    {
      timestamp: '2026-08-18T11:08:51.101Z',
      trace_id: '9c1f0a4b2d3e4f5061728394a5b6c7d8',
      span_id: '24578080047015803',
      parent_span_id: '',
      span_name: 'Cloud SQL Query',
      span_kind: 'SPAN_KIND_UNSPECIFIED',
      service_name: 'sample-service',
      duration_ns: 8000000,
      span_attributes: {
        database: 'appdb',
        'gcp.project_id': 'example-project',
        instance: 'example-project:sample-db',
      },
    },
  ],
};

const cloudSqlEvent = {
  id: 'event-under-test',
  source: 'GCP_Metric_Alert',
  subject_type: 'cloudsql_database',
  subject_name: 'example-project:sample-db',
  subject_namespace: 'Cloud SQL',
  evidences: [],
};

describe('TracesCard with GCP cloud evidence', () => {
  it('renders from a stringified traces payload (how the evidence is stored)', async () => {
    const evidence = {
      type: 'json',
      data: JSON.stringify(gcpCloudSqlSpans),
      additional_info: { action_name: 'traces' },
    };

    const card = new TracesCard(evidence, cloudSqlEvent, 0);

    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.text).toBe('Traces');
    expect(card.traceDataFromEvidence).toHaveLength(2);
  });

  it('renders from an already-parsed payload', async () => {
    const evidence = {
      type: 'json',
      data: gcpCloudSqlSpans,
      additional_info: { action_name: 'traces' },
    };

    const card = new TracesCard(evidence, cloudSqlEvent, 0);

    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.traceDataFromEvidence).toHaveLength(2);
  });

  it('does not claim renderable content when the payload carries no spans', async () => {
    const evidence = {
      type: 'json',
      data: JSON.stringify({ data: [] }),
      additional_info: { action_name: 'traces' },
    };

    // subject_type is not 'pod', so the K8s live-fetch fallback bails out too.
    const card = new TracesCard(evidence, cloudSqlEvent, 0);

    await expect(card.canRenderContent()).resolves.toBe(false);
  });

  it('surfaces evidence insights as card highlights', async () => {
    const evidence = {
      type: 'json',
      data: JSON.stringify(gcpCloudSqlSpans),
      insight: [{ message: 'Slow Cloud SQL queries observed', severity: 'warning' }],
      additional_info: { action_name: 'traces' },
    };

    const card = new TracesCard(evidence, cloudSqlEvent, 0);

    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.getHighLightsData()).toEqual([{ message: 'Slow Cloud SQL queries observed', severity: 'warning' }]);
  });
});
