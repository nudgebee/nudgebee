import { labelEntityColumns } from '../usePanelData';
import { defaultDraft } from '../entityQuery';

describe('labelEntityColumns', () => {
  const result = {
    columns: ['starts_at', 'title', 'id', 'account_id'],
    rows: [['2026-08-18T00:00:00Z', 'Github Actions Job Failed', 'evt-1', 'acc-1']],
  };

  it('shows labels but keeps the query’s own column names', () => {
    // The names are what a panel's link and hidden columns are configured
    // against — dropping them left `{{id}}` resolving against "Event id",
    // which rendered an Investigate column of empty cells.
    const table = labelEntityColumns(result, defaultDraft('events_v2'));
    expect(table.columns).toEqual(['Started at', 'Title', 'Event id', 'Account id']);
    expect(table.column_names).toEqual(['starts_at', 'title', 'id', 'account_id']);
  });

  it('marks the datetime columns', () => {
    expect(labelEntityColumns(result, defaultDraft('events_v2')).column_kinds).toEqual(['time', 'text', 'text', 'text']);
  });

  it('marks money, memory, cores and counts so they render like the listings do', () => {
    // A savings figure printed as 1234.5600000001 is the reason this exists.
    const recs = { columns: ['created_at', 'estimated_savings', 'resource_name'], rows: [] };
    expect(labelEntityColumns(recs, defaultDraft('recommendations_v2')).column_kinds).toEqual(['time', 'currency', 'text']);

    const nodes = { columns: ['name', 'cpu_capacity', 'memory_capacity', 'pod_count', 'cost'], rows: [] };
    expect(labelEntityColumns(nodes, defaultDraft('k8s_nodes_v2')).column_kinds).toEqual(['text', 'cpu', 'memory', 'number', 'currency']);
  });
});
