import {
  alignSeries,
  combineOf,
  CONSOLIDATED_LABEL,
  consolidatedSeries,
  lastValue,
  metricLabel,
  seriesLabel,
  statCaption,
  statTotal,
  toRawSeries,
} from '../panelSeries';

describe('statCaption', () => {
  it('drops a caption that is only the query ref id', () => {
    // An aggregate returns a series with no labels, so seriesLabel falls back to
    // the ref id — "A" under every stat means nothing to the viewer.
    expect(statCaption('A', ['A'])).toBe('');
    expect(statCaption('B', ['A', 'B'])).toBe('');
    expect(statCaption('series', ['A'])).toBe('');
    expect(statCaption(undefined, ['A'])).toBe('');
  });

  it('keeps a caption that names the series', () => {
    expect(statCaption('up{job="api"}', ['A'])).toBe('up{job="api"}');
    // A legend_format rendering to the same text as a ref id is indistinguishable
    // from the fallback, and losing it is the cheaper mistake.
    expect(statCaption('api-7f', ['A'])).toBe('api-7f');
  });
});

describe('alignSeries across accounts', () => {
  const DEV = { label: 'dev · x', accountLabel: 'dev', timestamps: [1000, 1060, 1120], values: [1, 2, 3] };
  const PROD = { label: 'prod · y', accountLabel: 'prod', timestamps: [1030, 1090, 1150], values: [4, 5, 6] };

  it('puts offset accounts on one grid so the chart can draw a line', () => {
    // Two clusters answering the same start/end/step a few seconds apart used to
    // interleave on the union axis: every series null wherever the other
    // reported, so no series had two CONSECUTIVE points and Chart.js drew
    // nothing — an empty plot under a correct axis.
    const out = alignSeries([DEV, PROD], 60);
    expect(out.series[0].values).toEqual([1, 2, 3]);
    expect(out.series[1].values).toEqual([4, 5, 6]);
    expect(out.timestamps).toHaveLength(3);
  });

  it('interleaves — and so draws nothing — without a step to snap to', () => {
    // Guards the fix: this is the broken shape it exists to prevent.
    const out = alignSeries([DEV, PROD]);
    expect(out.series[0].values).toEqual([1, null, 2, null, 3, null]);
    const consecutive = out.series[0].values.some((v, i) => v !== null && out.series[0].values[i + 1] != null);
    expect(consecutive).toBe(false);
  });

  it('leaves a single-account panel alone rather than shifting points that were right', () => {
    const out = alignSeries([DEV], 60);
    expect(out.timestamps).toEqual([1_000_000, 1_060_000, 1_120_000]);
  });

  it('keeps the later answer when a backend replies finer than the step', () => {
    const fine = { label: 'dev · x', accountLabel: 'dev', timestamps: [1000, 1010], values: [1, 9] };
    const out = alignSeries([fine, PROD], 60);
    expect(out.series[0].values[0]).toBe(9);
  });
});

describe('statTotal', () => {
  const ACCOUNTS = [
    { label: 'prod-eu · A', accountLabel: 'prod-eu', values: [90] },
    { label: 'prod-us · A', accountLabel: 'prod-us', values: [0] },
    { label: 'dev · A', accountLabel: 'dev', values: [10] },
  ];

  it('adds up every account, and keeps the parts', () => {
    // The regression this exists for: a stat over three accounts rendered
    // series[0] — 90, which reads as the total and is one account's figure.
    const stat = statTotal(ACCOUNTS, ['A']);
    expect(stat.total).toBe(100);
    expect(stat.rows).toEqual([
      { account: 'prod-eu', value: 90, failed: false },
      { account: 'prod-us', value: 0, failed: false },
      { account: 'dev', value: 10, failed: false },
    ]);
  });

  it('keeps a real zero in the total and in the breakdown', () => {
    // Zero is an answer. Dropping it would make "3 accounts" a lie on hover.
    expect(statTotal(ACCOUNTS, ['A']).rows[1]).toEqual({ account: 'prod-us', value: 0, failed: false });
  });

  it('names an account that could not answer instead of counting it as zero', () => {
    // The sharp edge of a total: a failed account and an account reporting zero
    // are the same arithmetic and completely different facts.
    const stat = statTotal(ACCOUNTS, ['A'], ['sandbox']);
    expect(stat.total).toBe(100);
    expect(stat.partial).toBe(true);
    expect(stat.rows[3]).toEqual({ account: 'sandbox', value: undefined, failed: true });
  });

  it('sums several series from one account rather than taking its first', () => {
    const stat = statTotal(
      [
        { label: 'prod · up{pod="a"}', accountLabel: 'prod', values: [1] },
        { label: 'prod · up{pod="b"}', accountLabel: 'prod', values: [2] },
      ],
      ['A']
    );
    expect(stat.total).toBe(3);
    expect(stat.rows).toEqual([{ account: 'prod', value: 3, failed: false }]);
  });

  it('reads back past a trailing gap', () => {
    expect(statTotal([{ label: 'A', accountLabel: 'prod', values: [3, null] }], ['A']).total).toBe(3);
  });

  it('reports nothing at all as undefined, not as zero', () => {
    // A panel that matched nothing must render a dash. Zero is a measurement.
    const stat = statTotal([{ label: 'A', accountLabel: 'prod', values: [null] }], ['A']);
    expect(stat.total).toBeUndefined();
  });

  it('leaves a single-account stat captioned exactly as before', () => {
    // No accountLabel is set when the panel queried one account, and an
    // aggregate carries no labels — so statCaption drops the bare ref id.
    const stat = statTotal([{ label: 'A', values: [9] }], ['A']);
    expect(stat).toEqual({ total: 9, combine: 'sum', rows: [{ account: '', value: 9, failed: false }], caption: '', partial: false });
  });

  it('averages the accounts when asked, and still keeps the parts', () => {
    // A gauge reads as a percentage: 90 %, 0 % and 10 % across three clusters is
    // 33 %, not the 100 % their sum says.
    const stat = statTotal(ACCOUNTS, ['A'], [], [], 'avg');
    expect(stat.total).toBeCloseTo(100 / 3);
    expect(stat.combine).toBe('avg');
    expect(stat.rows.map((r) => r.value)).toEqual([90, 0, 10]);
  });

  it('averages over the accounts that answered, not over the ones asked', () => {
    // A silent cluster is not a cluster at zero: it is left out of the mean as
    // it is left out of the sum, and the stat says the answer is partial.
    const stat = statTotal(ACCOUNTS, ['A'], ['sandbox'], ['staging'], 'avg');
    expect(stat.total).toBeCloseTo(100 / 3);
    expect(stat.partial).toBe(true);
    expect(stat.rows).toHaveLength(5);
  });

  it('averages a real zero in', () => {
    expect(statTotal(ACCOUNTS.slice(0, 2), ['A'], [], [], 'avg').total).toBe(45);
  });

  it('leaves an account whose newest sample is absent out of neither the row nor the mean', () => {
    // As it arrives off the wire: one account ends on a null, one reports only a
    // null. The first still has a value to show; the second answered with nothing
    // and must not be averaged in as a zero.
    const wire = (account: string, values: unknown[]) =>
      toRawSeries([{ query_key: 'A', payload: [{ metric: {}, timestamps: values.map((_, i) => i + 1), values }] }], {}).map((s) => ({
        ...s,
        accountLabel: account,
      }));
    const stat = statTotal([...wire('prod', [12.5]), ...wire('staging', [7, null]), ...wire('dev', [null])], ['A'], [], [], 'avg');
    expect(stat.rows.map((r) => r.value)).toEqual([12.5, 7, undefined]);
    expect(stat.total).toBe(9.75);
    expect(stat.partial).toBe(true);
  });

  /*
   * #40342: a gauge over a per-node query added each account's nodes up before
   * averaging the accounts, so nodes at 50, 60 and 70 % read 180 and the dial
   * pinned at 100.
   */
  it('averages an account’s own series too, so a per-node gauge reads as their mean', () => {
    const perNode = [
      { label: 'prod-eu · n1', accountLabel: 'prod-eu', values: [50] },
      { label: 'prod-eu · n2', accountLabel: 'prod-eu', values: [60] },
      { label: 'prod-eu · n3', accountLabel: 'prod-eu', values: [70] },
      { label: 'prod-us · m1', accountLabel: 'prod-us', values: [40] },
    ];
    const stat = statTotal(perNode, ['A'], [], [], 'avg');
    expect(stat.rows.map((r) => r.value)).toEqual([60, 40]);
    expect(stat.total).toBe(50);
  });

  it('averages one account’s per-node series on a single-account gauge, and says so', () => {
    const nodes = [50, 60, 70].map((v, i) => ({ label: `n${i}`, values: [v] }));
    const stat = statTotal(nodes, ['A'], [], [], 'avg');
    expect(stat.total).toBe(60);
    // Not "n0": the number is all three nodes, not the first one.
    expect(stat.caption).toBe('Average of 3 series');
    expect(statTotal(nodes, ['A']).caption).toBe('Sum of 3 series');
  });

  it('leaves a series with no newest sample out of its account’s mean', () => {
    const stat = statTotal(
      [
        { label: 'n1', accountLabel: 'prod', values: [50] },
        { label: 'n2', accountLabel: 'prod', values: [null] },
      ],
      ['A'],
      [],
      [],
      'avg'
    );
    expect(stat.rows.map((r) => r.value)).toEqual([50]);
  });

  it('captions a single account with the series that reported, when only one did', () => {
    const stat = statTotal(
      [
        { label: 'n1', values: [null] },
        { label: 'n2', values: [55] },
      ],
      ['A'],
      [],
      [],
      'avg'
    );
    expect(stat.total).toBe(55);
    expect(stat.caption).toBe('n2');
  });

  it('still adds an account’s series up when the panel sums', () => {
    const nodes = [50, 60, 70].map((v, i) => ({ label: `n${i}`, accountLabel: 'prod', values: [v] }));
    expect(statTotal([...nodes, { label: 'm', accountLabel: 'dev', values: [40] }], ['A']).rows.map((r) => r.value)).toEqual([180, 40]);
  });

  it('has no average of nothing', () => {
    expect(statTotal([{ label: 'A', accountLabel: 'prod', values: [null] }], ['A'], [], [], 'avg').total).toBeUndefined();
  });
});

describe('combineOf', () => {
  it('averages a gauge and adds up a stat unless the panel says otherwise', () => {
    expect(combineOf({ type: 'gauge' })).toBe('avg');
    expect(combineOf({ type: 'stat' })).toBe('sum');
    expect(combineOf({ type: 'gauge', options: { combine: 'sum' } })).toBe('sum');
    expect(combineOf({ type: 'stat', options: { combine: 'avg' } })).toBe('avg');
  });

  it('reads a value it does not know as the default, not as either choice', () => {
    expect(combineOf({ type: 'gauge', options: { combine: 'median' as never } })).toBe('avg');
  });
});

describe('seriesLabel', () => {
  it('names a series by its metric labels, Prometheus-style', () => {
    expect(seriesLabel({ __name__: 'up', job: 'api', instance: '10.0.0.1:9090' }, undefined, 'A')).toBe('up{job="api", instance="10.0.0.1:9090"}');
  });

  it('falls back to the query key only when there is nothing else', () => {
    // Every series of one query shares the key, so this must be the last
    // resort — otherwise 30 lines are all called "A".
    expect(seriesLabel({}, undefined, 'A')).toBe('A');
    expect(seriesLabel(undefined, undefined, 'A')).toBe('A');
    expect(seriesLabel(null, undefined, '')).toBe('series');
  });

  it('uses the metric name alone when it carries no labels', () => {
    expect(seriesLabel({ __name__: 'up' }, undefined, 'A')).toBe('up');
  });

  it('renders a legend_format against the series labels', () => {
    expect(seriesLabel({ pod: 'api-7f', node: 'ip-10-0-0-1' }, '{{pod}} on {{node}}', 'A')).toBe('api-7f on ip-10-0-0-1');
    expect(seriesLabel({ pod: 'api-7f' }, '{{ pod }}', 'A')).toBe('api-7f');
  });

  it('ignores a legend_format that renders to nothing', () => {
    // The named labels aren't on this series; an empty legend would leave the
    // series anonymous.
    expect(seriesLabel({ __name__: 'up', job: 'api' }, '{{pod}}', 'A')).toBe('up{job="api"}');
  });
});

describe('toRawSeries', () => {
  const RESULTS = [
    {
      query_key: 'A',
      payload: [
        { metric: { __name__: 'up', job: 'api' }, timestamps: [1, 2], values: ['1', '0.5'] },
        { metric: { __name__: 'up', job: 'db' }, timestamps: [2], values: [1] },
      ],
    },
  ];

  it('flattens every query result into one labelled series list', () => {
    expect(toRawSeries(RESULTS, {})).toEqual([
      { label: 'up{job="api"}', timestamps: [1, 2], values: [1, 0.5] },
      { label: 'up{job="db"}', timestamps: [2], values: [1] },
    ]);
  });

  it('parses string values and treats unparseable ones as gaps', () => {
    // Providers send numbers as strings as often as numbers; NaN must not reach
    // the chart.
    const results = [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1, 2], values: ['2.5', 'NaN'] }] }];
    expect(toRawSeries(results, {})[0].values).toEqual([2.5, null]);
  });

  it('reads an absent value as a gap, never as zero', () => {
    // The server sends a non-finite sample as JSON null, and Number(null) is 0 —
    // which would put a cluster that reported nothing on the chart at zero.
    const results = [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1, 2, 3, 4, 5], values: [7, null, '', '  ', undefined] }] }];
    expect(toRawSeries(results, {})[0].values).toEqual([7, null, null, null, null]);
  });

  it('keeps a reported zero a zero', () => {
    const results = [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1, 2], values: [0, '0'] }] }];
    expect(toRawSeries(results, {})[0].values).toEqual([0, 0]);
  });

  it('folds millisecond timestamps down to seconds', () => {
    // The cloud sources (CloudWatch and the Azure / GCP paths beside it) report
    // epoch ms where Prometheus reports seconds. alignSeries multiplies by 1000,
    // so an unconverted ms value lands tens of thousands of years out.
    const results = [{ query_key: 'A', payload: [{ metric: {}, timestamps: [1785988801250, 1785988861250], values: [1, 2] }] }];
    expect(toRawSeries(results, {})[0].timestamps).toEqual([1785988801, 1785988861]);
  });

  it('survives an empty or malformed response', () => {
    expect(toRawSeries([], {})).toEqual([]);
    expect(toRawSeries(undefined as any, {})).toEqual([]);
    expect(toRawSeries([{ query_key: 'A' }], {})).toEqual([]);
  });

  it('applies the legend_format of the target that produced the series', () => {
    expect(toRawSeries(RESULTS, { A: '{{job}}' }).map((s) => s.label)).toEqual(['api', 'db']);
  });
});

describe('alignSeries', () => {
  it('puts every series on the union of their timestamps', () => {
    // A pod that lived for two of the three scrapes must sit at the RIGHT two
    // points, not at index 0 and 1 of a foreign axis.
    const aligned = alignSeries([
      { label: 'a', timestamps: [1, 2, 3], values: [1, 2, 3] },
      { label: 'b', timestamps: [3], values: [9] },
    ]);
    expect(aligned.labels).toHaveLength(3);
    expect(aligned.series[0].values).toEqual([1, 2, 3]);
    expect(aligned.series[1].values).toEqual([null, null, 9]);
  });

  it('sorts the axis numerically, whatever order the accounts answered in', () => {
    const aligned = alignSeries([
      { label: 'a', timestamps: [30, 10], values: [3, 1] },
      { label: 'b', timestamps: [20], values: [2] },
    ]);
    // Axis is 10, 20, 30 — the default Array#sort compares as text, so this
    // needs the numeric comparator to hold once timestamps pass 10 digits.
    expect(aligned.series[0].values).toEqual([1, null, 3]);
    expect(aligned.series[1].values).toEqual([null, 2, null]);
  });

  it('leaves gaps as null rather than zero', () => {
    // 0 would draw a drop to the axis that never happened.
    const aligned = alignSeries([
      { label: 'a', timestamps: [1], values: [5] },
      { label: 'b', timestamps: [2], values: [5] },
    ]);
    expect(aligned.series[0].values).toEqual([5, null]);
  });

  it('is empty for no series', () => {
    expect(alignSeries([])).toEqual({ labels: [], timestamps: [], series: [] });
  });

  it('reports the axis in milliseconds beside the printed labels', () => {
    // The chart formats its own ticks and tooltip from these; the provider
    // answered in seconds, and charting those would date every point to 1970.
    const aligned = alignSeries([{ label: 'a', timestamps: [1_755_000_000, 1_755_000_060], values: [1, 2] }]);
    expect(aligned.timestamps).toEqual([1_755_000_000_000, 1_755_000_060_000]);
    expect(aligned.timestamps).toHaveLength(aligned.labels.length);
  });
});

describe('lastValue', () => {
  it('reads back past a trailing gap to the newest reported value', () => {
    expect(lastValue([1, 2, null])).toBe(2);
    expect(lastValue([1, 2, 3])).toBe(3);
  });

  it('keeps a legitimate zero', () => {
    expect(lastValue([1, 0])).toBe(0);
  });

  it('is undefined when nothing was reported', () => {
    expect(lastValue([null, null])).toBeUndefined();
    expect(lastValue([])).toBeUndefined();
    expect(lastValue(undefined)).toBeUndefined();
  });
});

describe('consolidatedSeries', () => {
  const acc = (account: string, label: string, values: (number | null)[]) => ({
    label: `${account} · ${label}`,
    accountLabel: account,
    values,
  });

  it('adds the accounts up, one total for an aggregate query', () => {
    // sum(...) answers with one unlabelled series per account — the common
    // shape — so the chart gets the parts and one "All accounts" line.
    const totals = consolidatedSeries([acc('prod-eu', 'A', [90, 95]), acc('prod-us', 'A', [0, 5]), acc('dev', 'A', [10, 10])]);
    expect(totals).toEqual([{ label: CONSOLIDATED_LABEL, values: [100, 110], consolidated: true }]);
  });

  it('groups by the metric label the accounts share, and qualifies each total', () => {
    // sum by (namespace) across two clusters: one total per namespace, named
    // so the two totals are not both called "All accounts".
    const totals = consolidatedSeries([
      acc('prod-eu', 'kube-system', [1]),
      acc('prod-eu', 'default', [2]),
      acc('prod-us', 'kube-system', [10]),
      acc('prod-us', 'default', [20]),
    ]);
    expect(totals.map((t) => [t.label, t.values])).toEqual([
      ['All accounts · kube-system', [11]],
      ['All accounts · default', [22]],
    ]);
  });

  it('skips a label only one account reports', () => {
    // A per-pod query: pod names differ per cluster, and a total of one line
    // is that line drawn twice.
    const totals = consolidatedSeries([acc('prod-eu', 'pod-a', [1]), acc('prod-us', 'pod-b', [2])]);
    expect(totals).toEqual([]);
  });

  it('sums what was reported past a gap, and is a gap only where nothing was', () => {
    // 95 + gap + 10 is 105. Only an instant every account missed stays null.
    const totals = consolidatedSeries([acc('prod-eu', 'A', [1, null, 3, null]), acc('prod-us', 'A', [10, 20, null, null])]);
    expect(totals[0].values).toEqual([11, 20, 3, null]);
  });

  it('counts a real zero', () => {
    const totals = consolidatedSeries([acc('prod-eu', 'A', [0]), acc('prod-us', 'A', [5])]);
    expect(totals[0].values).toEqual([5]);
  });

  it('is empty for a single-account panel', () => {
    // Nothing to add — and the series carry no account prefix to strip.
    expect(consolidatedSeries([{ label: 'A', values: [1, 2] }])).toEqual([]);
    expect(consolidatedSeries([acc('prod-eu', 'A', [1]), acc('prod-eu', 'B', [2])])).toEqual([]);
  });

  it('does not count a series with no account as a second account', () => {
    const totals = consolidatedSeries([acc('prod-eu', 'A', [1]), acc('prod-us', 'B', [2]), { label: 'A', values: [5] }]);
    expect(totals).toEqual([]);
  });

  it('does not add a total into a total', () => {
    const once = consolidatedSeries([acc('prod-eu', 'A', [1]), acc('prod-us', 'A', [2])]);
    expect(consolidatedSeries([acc('prod-eu', 'A', [1]), acc('prod-us', 'A', [2]), ...once])).toEqual(once);
  });
});

describe('metricLabel', () => {
  it('strips the account prefix a multi-account panel adds', () => {
    expect(metricLabel({ label: 'prod-eu · up{job="x"}', accountLabel: 'prod-eu' })).toBe('up{job="x"}');
  });

  it('leaves a label alone when there is no account, or the prefix is not there', () => {
    expect(metricLabel({ label: 'up{job="x"}' })).toBe('up{job="x"}');
    expect(metricLabel({ label: 'up{job="x"}', accountLabel: 'prod-eu' })).toBe('up{job="x"}');
  });
});
