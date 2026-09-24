import {
  breachedStep,
  hasThresholds,
  isCompleteStep,
  panelBreach,
  panelThresholdsOf,
  panelThresholdStepsOf,
  THRESHOLD_COLORS,
  thresholdTone,
} from '../panelThresholds';
import type { Panel, PanelThresholdStep } from '@api1/dashboards';

const panelWith = (options: unknown, type = 'stat'): Panel =>
  ({ id: 1, title: 'CPU', type, datasource: 'metrics', grid_pos: { x: 0, y: 0, w: 4, h: 4 }, options } as unknown as Panel);

const steps = (...values: [number, string][]): PanelThresholdStep[] => values.map(([value, color]) => ({ value, color } as PanelThresholdStep));

describe('reading a panel’s thresholds', () => {
  it('sorts the steps ascending, whatever order they were authored in', () => {
    const read = panelThresholdsOf(panelWith({ thresholds: steps([90, 'red'], [70, 'amber'], [80, 'green']) }));
    expect(read.map((s) => s.value)).toEqual([70, 80, 90]);
  });

  it('has nothing to say about a panel that never heard of thresholds', () => {
    expect(panelThresholdsOf(panelWith(undefined))).toEqual([]);
    expect(panelThresholdsOf(panelWith({ columns: [] }))).toEqual([]);
  });

  /*
   * A dashboard can arrive as raw JSON — nativeImport passes `options` through
   * whole, and the server keeps it as an opaque map — so this is the only place
   * an unusable step is ever caught.
   */
  it('drops a step an import may have written that nothing could draw', () => {
    const read = panelThresholdsOf(
      panelWith({
        thresholds: [
          { value: 80, color: 'red' },
          { value: 'eighty', color: 'red' },
          { value: 90, color: 'chartreuse' },
          { value: Number.NaN, color: 'amber' },
          null,
          'red',
        ],
      })
    );
    expect(read).toEqual([{ value: 80, color: 'red' }]);
  });

  it('ignores thresholds stored as something other than a list', () => {
    expect(panelThresholdsOf(panelWith({ thresholds: { value: 80, color: 'red' } }))).toEqual([]);
  });
});

describe('reading the steps the editor shows', () => {
  /*
   * The editor keeps a step it cannot draw yet — that is a row being typed — but
   * `null` in the list is not a row, it is JSON an import let through, and
   * reading `.value` off one used to take the whole editor modal down.
   */
  it('keeps an incomplete step and drops what is not a step at all', () => {
    const read = panelThresholdStepsOf(panelWith({ thresholds: [null, { value: Number.NaN, color: 'red' }, 'red', 7, { value: 80, color: 'red' }] }));
    expect(read).toEqual([
      { value: Number.NaN, color: 'red' },
      { value: 80, color: 'red' },
    ]);
  });

  it('holds Save for a step that could not be drawn, whichever half is missing', () => {
    expect(isCompleteStep({ value: 80, color: 'red' })).toBe(true);
    expect(isCompleteStep({ value: Number.NaN, color: 'red' })).toBe(false);
    expect(isCompleteStep({ value: 80, color: 'chartreuse' } as unknown as PanelThresholdStep)).toBe(false);
  });
});

describe('which step a value lands in', () => {
  const scale = steps([80, 'amber'], [90, 'red']);

  it('is none of them below the lowest step — the panel draws plain', () => {
    expect(breachedStep(scale, 79.9)).toBeUndefined();
  });

  it('starts AT the step, as Grafana does', () => {
    expect(breachedStep(scale, 80)).toEqual({ value: 80, color: 'amber' });
  });

  it('takes the highest step crossed, not the first', () => {
    expect(breachedStep(scale, 95)).toEqual({ value: 90, color: 'red' });
  });

  it('reads an unsorted scale the same way, since the read sorts it', () => {
    expect(breachedStep(panelThresholdsOf(panelWith({ thresholds: steps([90, 'red'], [80, 'amber']) })), 95)).toEqual({ value: 90, color: 'red' });
  });

  it('has no answer for a panel that measured nothing', () => {
    expect(breachedStep(scale, undefined)).toBeUndefined();
    expect(breachedStep(scale, null)).toBeUndefined();
    expect(breachedStep(scale, Number.NaN)).toBeUndefined();
  });

  it('works below zero, where a step is still a floor', () => {
    expect(breachedStep(steps([-5, 'red']), -4)).toEqual({ value: -5, color: 'red' });
    expect(breachedStep(steps([-5, 'red']), -6)).toBeUndefined();
  });
});

describe('which panels evaluate thresholds', () => {
  it('is the two that show one number', () => {
    expect(hasThresholds('stat')).toBe(true);
    expect(hasThresholds('gauge')).toBe(true);
    for (const type of ['timeseries', 'bar', 'table', 'text'] as const) expect(hasThresholds(type)).toBe(false);
  });

  /*
   * The editor drops thresholds when the visualisation changes, but a panel
   * saved before that, or imported, can still carry them — and a chart has a
   * value per series, so there is no one number the tint could be about.
   */
  it('leaves a chart plain even when it carries steps', () => {
    expect(panelBreach(panelWith({ thresholds: steps([80, 'red']) }, 'timeseries'), 95)).toBeUndefined();
    expect(panelBreach(panelWith({ thresholds: steps([80, 'red']) }, 'stat'), 95)).toEqual({ value: 80, color: 'red' });
  });

  /* A gauge pins its dial at 100; the threshold is about what was measured. */
  it('compares a gauge’s raw value, not the pinned dial', () => {
    expect(panelBreach(panelWith({ thresholds: steps([120, 'red']) }, 'gauge'), 150)).toEqual({ value: 120, color: 'red' });
  });
});

describe('how a step is drawn', () => {
  it('gives every colour a border, a tint and a chip tone', () => {
    for (const color of THRESHOLD_COLORS) {
      const tone = thresholdTone(color);
      expect(tone.border).toMatch(/^var\(--ds-/);
      expect(tone.tint).toMatch(/^var\(--ds-/);
      expect(tone.chip).toBeTruthy();
    }
  });
});
