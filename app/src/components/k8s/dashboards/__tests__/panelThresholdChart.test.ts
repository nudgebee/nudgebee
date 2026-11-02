import { mergedBands } from '../panelThresholdChart';
import type { PanelThresholdStep } from '@api1/dashboards';

/*
 * One translucent band per breaching line stacked into a solid block on a busy
 * fleet: eight lines holding the same step at once hid the lines entirely. The
 * bands for one step are merged and drawn once.
 */
describe('the hold bands a chart draws', () => {
  const red: PanelThresholdStep = { value: 70, color: 'red', for: 300 };
  const amber: PanelThresholdStep = { value: 50, color: 'amber', for: 300 };
  const visible = { isDatasetVisible: () => true };

  it('draws overlapping runs of one step as a single band', () => {
    const spans = Array.from({ length: 8 }, (_, i) => ({ series: i, step: red, from: i, to: 20 }));
    expect(mergedBands(visible, spans)).toEqual([{ step: red, from: 0, to: 20 }]);
  });

  it('keeps separate runs and separate steps apart', () => {
    const bands = mergedBands(visible, [
      { series: 0, step: red, from: 0, to: 4 },
      { series: 1, step: red, from: 10, to: 20 },
      { series: 2, step: amber, from: 2, to: 8 },
    ]);
    expect(bands).toEqual([
      { step: red, from: 0, to: 4 },
      { step: red, from: 10, to: 20 },
      { step: amber, from: 2, to: 8 },
    ]);
  });

  it('leaves out a line the viewer hid from the legend', () => {
    const chart = { isDatasetVisible: (i: number) => i !== 0 };
    expect(mergedBands(chart, [{ series: 0, step: red, from: 0, to: 20 }])).toEqual([]);
  });
});
