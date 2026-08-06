import {
  ENTITY_TABLES,
  buildEntityQuery,
  defaultDraft,
  draftFromQuery,
  findTable,
  operatorTakesList,
  operatorTakesValue,
  operatorsFor,
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
    expect(tablesFor('nudgebee').map((t) => t.value)).toEqual([
      'events_v2',
      'event_groupings_v2',
      'recommendations_v2',
      'recommendation_groupings_v2',
    ]);
    expect(tablesFor('traces').map((t) => t.value)).toEqual(['traces_groupings_v2', 'traces_v2']);
    expect(tablesFor('metrics')).toEqual([]);
    expect(tablesFor('logs')).toEqual([]);
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
