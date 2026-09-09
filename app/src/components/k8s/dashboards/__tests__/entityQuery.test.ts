import {
  ENTITY_TABLES,
  buildEntityQuery,
  defaultDraft,
  draftFromQuery,
  findTable,
  operatorTakesList,
  operatorTakesValue,
  operatorsFor,
  renderEntityQuery,
  selectableColumns,
  tablesFor,
} from '../entityQuery';

describe('defaultDraft', () => {
  it('opens on a query that already runs', () => {
    const draft = defaultDraft();
    expect(draft.table).toBe('events_v2');
    expect(draft.columns.length).toBeGreaterThan(0);
    expect(draft.timeColumn).toBe('starts_at');
    expect(draft.applyTimeRange).toBe(true);
  });

  it('gives the grouping table its own defaults', () => {
    // events_v2 is a row table; counts only exist on the aggregate twin, so the
    // two cannot share a column list.
    const draft = defaultDraft('event_groupings_v2');
    expect(draft.columns).toContain('event_count');
    expect(draft.sortColumn).toBe('event_count');
  });
});

describe('tablesFor', () => {
  it('offers each datasource only its own tables', () => {
    // The server refuses a cross-datasource table, so the picker must not
    // offer one — a traces panel reading events would fail at render.
    // This list is the mirror of `entityQueryTables` in
    // api-server/services/dashboard/entity_query.go. A table offered here but
    // absent there is a panel that saves and then fails at render.
    expect(tablesFor('nudgebee').map((t) => t.value)).toEqual([
      'events_v2',
      'event_groupings_v2',
      'recommendations_v2',
      'recommendation_groupings_v2',
      'spend_groupings_v2',
      'k8s_cluster_groupings_v2',
      'k8s_nodes_v2',
      'ticket_groupings_v2',
      'anomaly_grouping_v2',
      'anomaly_v2',
      'recommendation_security_cis_groupings_v2',
      'recommendation_security_v2',
      'auto_pilot_task_groupings_v2',
      'auto_pilot_approvals_v2',
      'audits_v2',
      'get_agent_health_v2',
      'llm_conversation_groupings_v2',
    ]);
    expect(tablesFor('traces').map((t) => t.value)).toEqual(['traces_groupings_v2', 'traces_v2']);
    expect(tablesFor('metrics')).toEqual([]);
    expect(tablesFor('logs')).toEqual([]);
  });

  it('names the permission module of every nudgebee table', () => {
    // The mirror of `PermissionModule` in api-server/services/query/metadata.go
    // (pinned there by TestEveryExecutableTableNamesAPermissionModule). It is
    // what the editor greys a table out on and what the tooltip names, so a
    // missing entry silently un-gates a table the engine will still refuse, and
    // a wrong one sends the author to their admin for the wrong grant.
    expect(Object.fromEntries(tablesFor('nudgebee').map((t) => [t.value, t.permissionModule]))).toEqual({
      events_v2: 'events',
      event_groupings_v2: 'events',
      recommendations_v2: 'recommendations',
      recommendation_groupings_v2: 'recommendations',
      spend_groupings_v2: 'spend',
      k8s_cluster_groupings_v2: 'accounts',
      k8s_nodes_v2: 'k8s',
      ticket_groupings_v2: 'tickets',
      anomaly_grouping_v2: 'anomalies',
      anomaly_v2: 'anomalies',
      recommendation_security_cis_groupings_v2: 'recommendations',
      recommendation_security_v2: 'recommendations',
      auto_pilot_task_groupings_v2: 'autooptimize',
      auto_pilot_approvals_v2: 'autooptimize',
      audits_v2: 'audits',
      get_agent_health_v2: 'accounts',
      llm_conversation_groupings_v2: 'ai_conversations',
    });
    // Traces are read through the traces service, not the query engine, so the
    // module is not what authorizes them and declaring one would gate on the
    // wrong thing.
    expect(tablesFor('traces').every((t) => t.permissionModule === undefined)).toBe(true);
  });

  it('opens a table with no filterable timestamp with the time range off', () => {
    // Clusters are a current-state snapshot and carry no date column at all;
    // leaving the switch on would filter the panel against nothing.
    const clusters = defaultDraft('k8s_cluster_groupings_v2');
    expect(clusters.applyTimeRange).toBe(false);
    expect(clusters.timeColumn).toBe('');

    const events = defaultDraft('events_v2');
    expect(events.applyTimeRange).toBe(true);
    expect(events.timeColumn).toBe('starts_at');
  });

  it('starts a traces panel on the grouping table', () => {
    // "What is slow" is the question a traces panel is usually built to answer,
    // and latency percentiles exist only on the aggregate.
    const draft = defaultDraft('traces');
    expect(draft.table).toBe('traces_groupings_v2');
    expect(draft.columns).toContain('p99_latency');
    expect(draft.timeColumn).toBe('timestamp');
  });
});

describe('selectableColumns', () => {
  it('hides filter-only columns from the column and sort pickers', () => {
    // A trace grouping can be NARROWED by a column its fixed response never
    // returns; selecting or sorting by one would render a blank column.
    const groupings = findTable('traces_groupings_v2');
    const selectable = selectableColumns(groupings).map((c) => c.name);
    expect(selectable).toContain('span_name');
    expect(selectable).not.toContain('trace_source');
    // Filter-only only makes sense on a column you can filter on.
    for (const column of groupings.columns.filter((c) => c.filterOnly)) {
      expect([column.name, column.filterable]).toEqual([column.name, true]);
    }
  });
});

describe('operatorsFor', () => {
  const events = findTable('events_v2');

  it('offers operators the column type actually supports', () => {
    expect(operatorsFor(events, 'title').map((o) => o.value)).toContain('_ilike');
    expect(operatorsFor(events, 'computed_score').map((o) => o.value)).toContain('_gte');
    // A numeric column has no substring match.
    expect(operatorsFor(events, 'computed_score').map((o) => o.value)).not.toContain('_ilike');
    expect(operatorsFor(events, 'labels').map((o) => o.value)).toContain('_has_key');
  });

  it('offers only operators the SQL generator implements', () => {
    // `_icontains` / `_regex` are declared in the engine's operator constants
    // but only the log providers implement them — the entity path answers
    // "binary clause type not supported", i.e. a panel that breaks at render.
    const implemented = new Set([
      '_eq',
      '_neq',
      '_in',
      '_not_in',
      '_like',
      '_nlike',
      '_ilike',
      '_lt',
      '_lte',
      '_gt',
      '_gte',
      '_between',
      '_is_null',
      '_contains',
      '_has_key',
    ]);
    for (const table of ENTITY_TABLES) {
      for (const column of table.columns) {
        for (const operator of operatorsFor(table, column.name)) {
          expect([column.name, operator.value, implemented.has(operator.value)]).toEqual([column.name, operator.value, true]);
        }
      }
    }
  });

  it('does not offer "is empty" on traces', () => {
    // It compiles to IS NULL, and a span store's columns are not nullable — the
    // filter would silently empty the panel rather than find the blank rows.
    for (const table of ENTITY_TABLES.filter((t) => t.datasource === 'traces')) {
      for (const column of table.columns) {
        const values = operatorsFor(table, column.name).map((o) => o.value);
        expect([table.value, column.name, values.includes('_is_null')]).toEqual([table.value, column.name, false]);
      }
    }
    // Still offered on the query-engine tables, where the columns ARE nullable.
    expect(operatorsFor(events, 'ends_at').map((o) => o.value)).toContain('_is_null');
  });

  it('knows which operators take a list or no value', () => {
    expect(operatorTakesList('_in')).toBe(true);
    expect(operatorTakesList('_eq')).toBe(false);
    expect(operatorTakesValue('_is_null')).toBe(false);
    expect(operatorTakesValue('_eq')).toBe(true);
  });
});

describe('buildEntityQuery', () => {
  it('compiles the draft into a query-engine request', () => {
    const query = buildEntityQuery({
      ...defaultDraft(),
      columns: ['starts_at', 'title'],
      filters: [{ column: 'priority', operator: '_in', value: 'P0, P1' }],
      sortColumn: 'starts_at',
      sortDesc: true,
      limit: 50,
    });
    expect(query).toEqual({
      table: 'events_v2',
      columns: [{ name: 'starts_at' }, { name: 'title' }],
      where: { _and: [{ _binary: { priority: { _in: ['P0', 'P1'] } } }] },
      order_by: [{ column: 'starts_at', order: 'desc' }],
      limit: 50,
    });
  });

  it('never emits the account or time filter', () => {
    // Both are appended server-side from the panel's scope and the dashboard's
    // picker, so a saved query stays portable across accounts and ranges.
    const query = buildEntityQuery({ ...defaultDraft(), filters: [{ column: 'status', operator: '_eq', value: 'firing' }] });
    expect(JSON.stringify(query)).not.toContain('account_id');
    expect(JSON.stringify(query)).not.toContain('_between');
  });

  it('coerces values to the column type', () => {
    // The engine compares against the real column type, so "5" would not match
    // an integer column.
    const numeric = buildEntityQuery({ ...defaultDraft(), filters: [{ column: 'computed_score', operator: '_gte', value: '80' }] });
    expect((numeric.where as any)._and[0]._binary.computed_score._gte).toBe(80);

    const bool = buildEntityQuery({ ...defaultDraft(), filters: [{ column: 'is_new_issue', operator: '_eq', value: 'true' }] });
    expect((bool.where as any)._and[0]._binary.is_new_issue._eq).toBe(true);

    const nullish = buildEntityQuery({ ...defaultDraft(), filters: [{ column: 'ends_at', operator: '_is_null', value: '' }] });
    expect((nullish.where as any)._and[0]._binary.ends_at._is_null).toBe(true);
  });

  it('drops incomplete filter rows instead of emitting a blank comparison', () => {
    // An empty row is a filter the author has not finished, not "= empty
    // string", which would match nothing and look like a broken panel.
    const query = buildEntityQuery({
      ...defaultDraft(),
      filters: [
        { column: 'status', operator: '_eq', value: '' },
        { column: 'priority', operator: '_eq', value: 'P0' },
      ],
    });
    expect((query.where as any)._and).toHaveLength(1);
  });

  it('omits where entirely when nothing is filtered', () => {
    expect(buildEntityQuery({ ...defaultDraft(), filters: [] }).where).toBeUndefined();
  });

  it('ignores columns that are not on the chosen table', () => {
    // Switching tables must not leave a column the engine would reject.
    const query = buildEntityQuery({ ...defaultDraft('event_groupings_v2'), columns: ['event_count', 'description'] });
    expect(query.columns).toEqual([{ name: 'event_count' }]);
  });

  it('keeps a template variable as written', () => {
    // $namespace is substituted at render from the host page's context.
    const query = buildEntityQuery({ ...defaultDraft(), filters: [{ column: 'subject_namespace', operator: '_eq', value: '$namespace' }] });
    expect((query.where as any)._and[0]._binary.subject_namespace._eq).toBe('$namespace');
  });
});

describe('draftFromQuery', () => {
  it('reads a stored query back into the builder', () => {
    const original = { ...defaultDraft(), filters: [{ column: 'priority', operator: '_in', value: 'P0, P1' }], limit: 25, sortDesc: false };
    const restored = draftFromQuery(buildEntityQuery(original));
    expect(restored.table).toBe('events_v2');
    expect(restored.columns).toEqual(original.columns);
    expect(restored.filters).toEqual([{ column: 'priority', operator: '_in', value: 'P0, P1' }]);
    expect(restored.limit).toBe(25);
    expect(restored.sortDesc).toBe(false);
  });

  it('falls back to the defaults for a panel that has no query yet', () => {
    expect(draftFromQuery(undefined)).toEqual(defaultDraft());
    expect(draftFromQuery({})).toEqual(defaultDraft());
  });

  it('restores the grouping table rather than assuming events', () => {
    const restored = draftFromQuery(buildEntityQuery(defaultDraft('event_groupings_v2')));
    expect(restored.table).toBe('event_groupings_v2');
    expect(restored.columns).toContain('event_count');
  });
});

describe('aggregate filters', () => {
  it('routes a filter on a computed column into HAVING, not WHERE', () => {
    // The engine refuses an aggregate in a where clause outright — "column
    // event_count defined in where clause is aggregated and cannot be used in
    // where clause" — so a panel filtering on Event count used to fail to render.
    const query = buildEntityQuery({
      ...defaultDraft('event_groupings_v2'),
      filters: [
        { column: 'cluster', operator: '_eq', value: 'prod' },
        { column: 'event_count', operator: '_gt', value: '100' },
      ],
    });
    expect((query.where as any)._and).toEqual([{ _binary: { cluster: { _eq: 'prod' } } }]);
    expect((query.having as any)._and).toEqual([{ _binary: { event_count: { _gt: 100 } } }]);
  });

  it('leaves HAVING off a query with no aggregate filter', () => {
    const query = buildEntityQuery({ ...defaultDraft('events_v2'), filters: [{ column: 'title', operator: '_ilike', value: '%oom%' }] });
    expect(query.having).toBeUndefined();
  });

  it('reads an aggregate filter back into the builder', () => {
    const original = {
      ...defaultDraft('recommendation_groupings_v2'),
      filters: [{ column: 'count', operator: '_gte', value: '5' }],
    };
    const restored = draftFromQuery(buildEntityQuery(original));
    expect(restored.filters).toEqual([{ column: 'count', operator: '_gte', value: '5' }]);
  });

  it('never offers a traces aggregate as a filter', () => {
    // Traces have no HAVING surface: their filters go to the traces service, so
    // an aggregate there can only rebuild the bug this replaced.
    for (const table of ENTITY_TABLES.filter((t) => t.datasource === 'traces')) {
      for (const column of table.columns.filter((c) => c.aggregate)) {
        expect([table.value, column.name, column.filterable]).toEqual([table.value, column.name, undefined]);
      }
    }
    expect(findTable('traces_groupings_v2').columns.filter((c) => c.aggregate).length).toBe(5);
  });

  it('marks every column the engine computes from the GROUP BY', () => {
    // Mirrors IsAggregated in api-server/services/query/metadata.go. A column
    // that gains an aggregate Def there and is not marked here becomes a filter
    // that builds a query the engine rejects.
    const expected: Record<string, string[]> = {
      event_groupings_v2: [
        'event_count',
        'count_priority_p0',
        'count_priority_p1',
        'count_priority_p2',
        'count_priority_p3',
        'count_new_issues',
        'count_pod_issues',
        'count_node_issues',
        'count_application_issues',
        'max_created_at',
        'min_created_at',
      ],
      recommendation_groupings_v2: ['count', 'sum_estimated_savings'],
      spend_groupings_v2: ['spend_amount', 'spend_count', 'resource_count', 'account_count'],
      ticket_groupings_v2: ['count'],
      anomaly_grouping_v2: ['count'],
      recommendation_security_cis_groupings_v2: ['count', 'updated_at'],
      auto_pilot_task_groupings_v2: ['count'],
      llm_conversation_groupings_v2: ['count'],
    };
    for (const [table, columns] of Object.entries(expected)) {
      const marked = findTable(table)
        .columns.filter((c) => c.aggregate)
        .map((c) => c.name);
      expect([table, marked.sort()]).toEqual([table, [...columns].sort()]);
    }
  });
});

describe('renderEntityQuery', () => {
  const query = buildEntityQuery({
    ...defaultDraft('events_v2'),
    filters: [
      { column: 'subject_namespace', operator: '_eq', value: '$namespace' },
      { column: 'priority', operator: '_in', value: '$priority, P0' },
      { column: 'ends_at', operator: '_is_null', value: '' },
    ],
  });

  it('substitutes a variable into a filter value', () => {
    // An entity panel's query is an object, so it never passed through
    // renderTemplate — `$namespace` travelled to the engine verbatim and matched
    // nothing, while the builder's placeholder advertised it.
    const rendered = renderEntityQuery(query, (v) => v.replace('$namespace', 'prod').replace('$priority', 'P1'));
    const clauses = (rendered.where as any)._and;
    expect(clauses[0]._binary.subject_namespace._eq).toBe('prod');
    // A list value is substituted element by element.
    expect(clauses[1]._binary.priority._in).toEqual(['P1', 'P0']);
    // `_is_null` carries a boolean, which no template touches.
    expect(clauses[2]._binary.ends_at._is_null).toBe(true);
  });

  it('leaves column names and operators alone', () => {
    // Only values are authored by hand; a substitutable column name would let a
    // variable rewrite which column is filtered.
    const rendered = renderEntityQuery(query, () => 'REPLACED');
    const clauses = (rendered.where as any)._and;
    expect(Object.keys(clauses[0]._binary)).toEqual(['subject_namespace']);
    expect(Object.keys(clauses[0]._binary.subject_namespace)).toEqual(['_eq']);
  });

  it('survives a column whose operators are not an object', () => {
    // Stored dashboard JSON is hand-editable and predates the current shape, so
    // a null here is reachable. Object.entries(null) threw and took the whole
    // dashboard render with it.
    const malformed = { _and: [{ _binary: { subject_namespace: null } }, { _binary: { priority: 'not-an-object' } }] };
    const rendered = renderEntityQuery({ where: malformed }, (v) => v);
    const clauses = (rendered.where as any)._and;
    expect(clauses[0]._binary.subject_namespace).toEqual({});
    expect(clauses[1]._binary.priority).toEqual({});
  });

  it('substitutes into HAVING as well as WHERE', () => {
    const grouped = buildEntityQuery({
      ...defaultDraft('event_groupings_v2'),
      filters: [
        { column: 'cluster', operator: '_eq', value: '$cluster' },
        { column: 'event_count', operator: '_gt', value: '10' },
      ],
    });
    const rendered = renderEntityQuery(grouped, (v) => v.replace('$cluster', 'eu-1'));
    expect((rendered.where as any)._and[0]._binary.cluster._eq).toBe('eu-1');
    expect((rendered.having as any)._and[0]._binary.event_count._gt).toBe(10);
  });

  it('passes a query with no filters through untouched', () => {
    const bare = buildEntityQuery(defaultDraft('events_v2'));
    expect(renderEntityQuery(bare, () => 'REPLACED')).toEqual(bare);
  });
});
