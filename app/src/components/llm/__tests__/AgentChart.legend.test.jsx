import React from 'react';
import { render } from '@testing-library/react';
import AgentChart from '../common/AgentChart';

// A chart built from queried metrics shows each line's latest value (levels and
// rates do not sum) and draws its threshold lines thin; any other chart keeps its
// per-series totals.
let props;
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: {
    TimeSeries: (p) => {
      props = p;
      return null;
    },
    Doughnut: () => null,
  },
}));

const metric = {
  type: 'line',
  labels: ['10:00', '11:00'],
  series: [
    { key: 'cpu', data: [10, 11] },
    { key: 'limit', data: [20, 20] },
  ],
  source: 'metrics',
  reference: ['limit', 'not a series'],
};

describe('AgentChart legend value and reference lines', () => {
  it('keeps totals for a chart not built from metrics', () => {
    render(<AgentChart spec={{ type: 'line', labels: ['a', 'b'], series: [{ key: 'spend', data: [1, 2] }] }} />);
    expect(props.legendValue).toBeUndefined();
    expect(props.referenceKeys).toBeUndefined();
    expect(props.integerY).toBe(true);
  });

  it('shows the latest value for a metrics chart and draws its thresholds as reference lines', () => {
    render(<AgentChart spec={metric} />);
    expect(props.legendValue).toBe('last');
    expect(props.integerY).toBe(false);
    expect(props.referenceKeys).toEqual(['limit']);
  });

  it('shows the latest value for a metrics chart without thresholds', () => {
    render(<AgentChart spec={{ ...metric, reference: undefined }} />);
    expect(props.legendValue).toBe('last');
    expect(props.referenceKeys).toBeUndefined();
  });
});
