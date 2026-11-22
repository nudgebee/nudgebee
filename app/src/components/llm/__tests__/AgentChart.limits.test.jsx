import { parseChartSpec } from '../common/AgentChart';

const spec = (type, n) => ({
  type,
  labels: Array.from({ length: n }, (_, i) => `t${i}`),
  series: [{ key: 'a', data: Array.from({ length: n }, (_, i) => i) }],
});

describe('parseChartSpec label limits', () => {
  it('accepts up to 120 points for trend charts', () => {
    expect(parseChartSpec(spec('line', 120))).not.toBeNull();
    expect(parseChartSpec(spec('area', 120))).not.toBeNull();
    expect(parseChartSpec(spec('line', 121))).toBeNull();
  });

  it('keeps 16 labels for bar charts', () => {
    expect(parseChartSpec(spec('bar', 16))).not.toBeNull();
    expect(parseChartSpec(spec('bar', 17))).toBeNull();
  });
});
