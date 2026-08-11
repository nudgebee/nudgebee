import { alignSeries, lastValue, seriesLabel, statCaption, toRawSeries } from '../panelSeries';

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
