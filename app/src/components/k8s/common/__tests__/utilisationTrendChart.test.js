import { alignToGrid, buildTrendChart } from '@components/k8s/common/utilisationTrendChart';

const CFG = { divisor: 1, keys: { usage: 'u', total: 't', request: 'r', limit: 'l' } };
const result = (key, timestamps, values) => ({ query_key: key, payload: [{ timestamps, values: values.map(String) }] });
const seriesData = (chart, label) => chart.dataset.find((d) => d.label === label).data;

describe('alignToGrid', () => {
  it('returns a series on the same grid unchanged', () => {
    expect(alignToGrid([0, 60, 120], { timestamps: [0, 60, 120], values: [1, 2, 3] })).toEqual([1, 2, 3]);
  });

  it('places a coarser series by time, holding each sample for one of its own steps', () => {
    const grid = [0, 60, 120, 180, 240, 300, 360];
    expect(alignToGrid(grid, { timestamps: [0, 180], values: [5, 7] })).toEqual([5, 5, 5, 7, 7, 7, null]);
  });

  it('leaves slots before the first sample empty and uses a sample that starts before the grid', () => {
    expect(alignToGrid([100, 200, 300], { timestamps: [250], values: [9] })).toEqual([null, null, 9]);
    expect(alignToGrid([100, 200], { timestamps: [40, 140], values: [1, 2] })).toEqual([1, 2]);
  });

  it('does not stretch a sample across a gap in the series', () => {
    const grid = [0, 60, 120, 180, 240, 300];
    expect(alignToGrid(grid, { timestamps: [0, 60, 300], values: [1, 2, 3] })).toEqual([1, 2, null, null, null, 3]);
  });
});

describe('buildTrendChart', () => {
  it('spreads coarse request and limit samples across the whole window instead of its start', () => {
    // 24h on Elasticsearch: usage every 288s, limits roughly hourly.
    const grid = Array.from({ length: 301 }, (_, i) => 1_000_000 + i * 288);
    const hourly = Array.from({ length: 25 }, (_, i) => 1_000_000 + i * 3600);
    const chart = buildTrendChart(
      [
        result(
          'u',
          grid,
          grid.map(() => 1)
        ),
        result(
          't',
          grid,
          grid.map(() => 2)
        ),
        result(
          'r',
          hourly,
          hourly.map(() => 3)
        ),
        result(
          'l',
          hourly,
          hourly.map(() => 4)
        ),
      ],
      CFG
    );
    expect(chart.labels).toHaveLength(301);
    const limit = seriesData(chart, 'Limit');
    expect(limit).toHaveLength(301);
    expect(limit[0]).toBe(4);
    expect(limit[150]).toBe(4); // mid-window: was empty when drawn by index
    expect(limit[300]).toBe(4);
  });

  it('keeps empty and non-numeric values as gaps', () => {
    const chart = buildTrendChart([result('u', [0, 60], ['1', 'NaN'])], CFG);
    expect(seriesData(chart, 'Usage')).toEqual([1, null]);
    expect(seriesData(chart, 'Request')).toEqual([null, null]);
  });
});
