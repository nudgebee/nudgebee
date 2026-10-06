import {
  breachedStep,
  breachingLinePreference,
  hasThresholds,
  holdLabel,
  isCompleteStep,
  lineBreaches,
  linePreferenceKey,
  panelBreach,
  panelBreaches,
  panelThresholdsOf,
  panelThresholdStepsOf,
  THRESHOLD_COLORS,
  thresholdOverlaps,
  thresholdTone,
} from '../panelThresholds';
import type { Panel, PanelThresholdStep } from '@api1/dashboards';
import type { StatTotal } from '../panelSeries';
import type { PanelSeries } from '../usePanelData';

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
  it('is the two that show one number, and the chart that judges each line', () => {
    expect(hasThresholds('stat')).toBe(true);
    expect(hasThresholds('gauge')).toBe(true);
    expect(hasThresholds('timeseries')).toBe(true);
    for (const type of ['bar', 'table', 'text'] as const) expect(hasThresholds(type)).toBe(false);
  });

  /*
   * A chart has a value per series, so there is no one number the shown-number
   * path could be about: its steps go through lineBreaches, never panelBreach.
   */
  it('never judges a chart by one number', () => {
    expect(panelBreach(panelWith({ thresholds: steps([80, 'red']) }, 'timeseries'), 95)).toBeUndefined();
    expect(
      panelBreaches(
        panelWith({ thresholds: steps([80, 'red']) }, 'timeseries'),
        { total: 95, rows: [], caption: '', partial: false, combine: 'sum' },
        []
      ).frame
    ).toBeUndefined();
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

describe('a step that compares downwards, or strictly', () => {
  const at = (step: Partial<PanelThresholdStep>, value: number) => breachedStep([{ value: 10, color: 'red', ...step } as PanelThresholdStep], value);

  it('reads each comparison as written', () => {
    expect(at({ op: 'gte' }, 10)).toBeDefined();
    expect(at({ op: 'gt' }, 10)).toBeUndefined();
    expect(at({ op: 'gt' }, 11)).toBeDefined();
    expect(at({ op: 'lte' }, 10)).toBeDefined();
    expect(at({ op: 'lt' }, 10)).toBeUndefined();
    expect(at({ op: 'lt' }, 9)).toBeDefined();
  });

  it('takes the LOWEST lower bound crossed, as it takes the highest upper one', () => {
    const scale = [
      { value: 10, color: 'amber', op: 'lte' },
      { value: 5, color: 'red', op: 'lte' },
    ] as PanelThresholdStep[];
    expect(breachedStep(scale, 3)?.color).toBe('red');
    expect(breachedStep(scale, 8)?.color).toBe('amber');
    expect(breachedStep(scale, 12)).toBeUndefined();
  });

  it('settles an upper and a lower bound both holding by the more severe colour', () => {
    // 90 is ≥ 80 and ≤ 100: neither is further past its line than the other.
    const scale = [
      { value: 80, color: 'amber', op: 'gte' },
      { value: 100, color: 'red', op: 'lte' },
    ] as PanelThresholdStep[];
    expect(breachedStep(scale, 90)?.color).toBe('red');
  });

  it('refuses a comparison or a subject it does not know, rather than reading it as the default', () => {
    expect(isCompleteStep({ value: 1, color: 'red', op: 'between' } as unknown as PanelThresholdStep)).toBe(false);
    expect(isCompleteStep({ value: 1, color: 'red', applies_to: 'total' } as unknown as PanelThresholdStep)).toBe(false);
    expect(isCompleteStep({ value: 1, color: 'red', applies_to: 'account' } as PanelThresholdStep)).toBe(false);
    expect(isCompleteStep({ value: 1, color: 'red', applies_to: 'account', account_id: 'acc-1' } as PanelThresholdStep)).toBe(true);
  });
});

describe('which number a step is about', () => {
  const accounts = [
    { value: 'acc-1', label: 'prod-eu' },
    { value: 'acc-2', label: 'prod-us' },
    { value: 'acc-3', label: 'dev' },
  ];
  // 90 + 2 + 10 = 102.
  const fleet: StatTotal = {
    total: 102,
    combine: 'sum',
    rows: [
      { account: 'prod-eu', value: 90, failed: false },
      { account: 'prod-us', value: 2, failed: false },
      { account: 'dev', value: 10, failed: false },
    ],
    caption: '',
    partial: false,
  };
  const on = (thresholds: unknown[], stat: StatTotal = fleet, shown = accounts) => panelBreaches(panelWith({ thresholds }), stat, shown);

  it('compares a step with no subject against the number the panel shows, as every step did before', () => {
    const { frame } = on([{ value: 100, color: 'red' }]);
    expect(frame).toMatchObject({ account: null, value: 102 });
    // And never against an account's own figure: 90 is under 100.
    expect(on([{ value: 95, color: 'red' }]).rows.size).toBe(0);
  });

  it('checks every account on its own, and names the one that crossed', () => {
    const { frame, rows } = on([{ value: 5, color: 'red', op: 'lte', applies_to: 'every' }]);
    expect(frame).toMatchObject({ account: 'prod-us', value: 2 });
    expect([...rows.keys()]).toEqual(['prod-us']);
  });

  it('fires a step naming one account on that account alone — not the others, not the total', () => {
    const step = { value: 50, color: 'amber', applies_to: 'account', account_id: 'acc-1' };
    expect(on([step]).frame).toMatchObject({ account: 'prod-eu', value: 90 });
    const quiet = on([{ ...step, account_id: 'acc-3' }]);
    expect(quiet.frame).toBeUndefined();
  });

  it('never fires a step naming an account the panel is not showing', () => {
    // Filtered out of the view, deleted, or carried in by an import from another tenant.
    expect(on([{ value: 0, color: 'red', applies_to: 'account', account_id: 'acc-9' }]).frame).toBeUndefined();
    expect(on([{ value: 0, color: 'red', applies_to: 'account', account_id: 'acc-1' }], fleet, accounts.slice(1)).frame).toBeUndefined();
  });

  it('draws the frame for the most severe breach, and lists every one', () => {
    const { frame, all } = on([
      { value: 100, color: 'amber' },
      { value: 5, color: 'red', op: 'lt', applies_to: 'every' },
    ]);
    expect(frame).toMatchObject({ account: 'prod-us', step: { color: 'red' } });
    expect(all.map((b) => b.account)).toEqual(['prod-us', null]);
  });

  it('keeps the shown number on a tie, since that is the figure on the card', () => {
    const { frame } = on([
      { value: 100, color: 'red' },
      { value: 50, color: 'red', applies_to: 'every' },
    ]);
    expect(frame?.account).toBeNull();
  });

  it('does not name the account on a single-account panel, where there is one number', () => {
    const single: StatTotal = { total: 90, combine: 'sum', rows: [{ account: '', value: 90, failed: false }], caption: '', partial: false };
    expect(on([{ value: 50, color: 'red', applies_to: 'every' }], single, accounts.slice(0, 1)).frame).toMatchObject({ account: null, value: 90 });
    expect(on([{ value: 50, color: 'red', applies_to: 'account', account_id: 'acc-1' }], single, accounts.slice(0, 1)).frame).toBeDefined();
  });

  it('checks only the accounts on screen, even when the rows still hold one filtered out', () => {
    // The view was just narrowed to prod-eu and dev; the rows are the previous answer.
    const { frame, rows } = on([{ value: 5, color: 'red', op: 'lte', applies_to: 'every' }], fleet, [accounts[0], accounts[2]]);
    expect(frame).toBeUndefined();
    expect(rows.size).toBe(0);
  });

  it('counts two steps drawing the same line on the same number once', () => {
    // A single-account panel: the shown number and "every account" are one number.
    const single: StatTotal = { total: 50, combine: 'sum', rows: [{ account: '', value: 50, failed: false }], caption: '', partial: false };
    const { all } = on(
      [
        { value: 50, color: 'red' },
        { value: 50, color: 'red', applies_to: 'every' },
      ],
      single,
      accounts.slice(0, 1)
    );
    expect(all).toHaveLength(1);
  });

  it('skips an account that reported nothing — no value, nothing to compare', () => {
    const gap: StatTotal = { ...fleet, rows: [...fleet.rows.slice(0, 2), { account: 'dev', value: undefined, failed: true }] };
    expect(on([{ value: 1, color: 'red', op: 'lte', applies_to: 'every' }], gap).rows.has('dev')).toBe(false);
  });

  it('leaves panelBreach — the shown number — to steps about the shown number', () => {
    expect(panelBreach(panelWith({ thresholds: [{ value: 1, color: 'red', applies_to: 'every' }] }), 500)).toBeUndefined();
  });
});

describe('which overlapping steps the editor spells out', () => {
  const fleet = [
    { value: 'acc-1', label: 'prod-eu' },
    { value: 'acc-2', label: 'prod-us' },
  ];
  const overlaps = (list: unknown[], accounts = fleet) => thresholdOverlaps(list as PanelThresholdStep[], accounts).map((o) => o.message);

  it('says nothing about a plain scale — that is what thresholds are for', () => {
    expect(
      overlaps([
        { value: 80, color: 'amber' },
        { value: 90, color: 'red' },
      ])
    ).toEqual([]);
  });

  it('names the range where an upper and a lower bound both apply, and the colour that wins it', () => {
    expect(
      overlaps([
        { value: 80, color: 'red' },
        { value: 90, color: 'green', op: 'lte' },
      ])
    ).toEqual(['Steps 1 and 2 both apply from 80 to 90 — Red wins there, as the more severe colour.']);
  });

  it('says nothing when the two bounds never meet', () => {
    expect(
      overlaps([
        { value: 90, color: 'red' },
        { value: 10, color: 'amber', op: 'lte' },
      ])
    ).toEqual([]);
    // ≥ 80 and < 80 share no value.
    expect(
      overlaps([
        { value: 80, color: 'red' },
        { value: 80, color: 'green', op: 'lt' },
      ])
    ).toEqual([]);
  });

  it('points out two steps on the same line, which only authoring order settles', () => {
    expect(
      overlaps([
        { value: 80, color: 'red' },
        { value: 80, color: 'amber' },
      ])
    ).toEqual(['Steps 1 and 2 both start at 80 — step 2 (Shown number ≥ 80 · Amber) wins, as the later one.']);
  });

  it('warns when "every account" outranks an account’s own stricter step', () => {
    expect(
      overlaps([
        { value: 80, color: 'amber', applies_to: 'every' },
        { value: 70, color: 'red', applies_to: 'account', account_id: 'acc-2' },
      ])
    ).toEqual(['For prod-us, steps 1 and 2 both apply from 80 up — step 1 (Every account ≥ 80 · Amber) wins, as the higher line.']);
  });

  it('stays quiet when the account’s own step is the one that wins', () => {
    expect(
      overlaps([
        { value: 70, color: 'amber', applies_to: 'every' },
        { value: 80, color: 'red', applies_to: 'account', account_id: 'acc-2' },
      ])
    ).toEqual([]);
  });

  it('keeps steps about different numbers apart — the total is not an account’s figure', () => {
    expect(
      overlaps([
        { value: 80, color: 'red' },
        { value: 90, color: 'green', op: 'lte', applies_to: 'every' },
      ])
    ).toEqual([]);
    // …except on a single-account panel, where they are the same number.
    expect(
      overlaps(
        [
          { value: 80, color: 'red' },
          { value: 90, color: 'green', op: 'lte', applies_to: 'every' },
        ],
        fleet.slice(0, 1)
      )
    ).toHaveLength(1);
  });

  it('ignores steps of one colour, unfinished steps, and steps naming an account the panel lost', () => {
    expect(
      overlaps([
        { value: 80, color: 'red' },
        { value: 90, color: 'red', op: 'lte' },
        { value: Number.NaN, color: 'green', op: 'lte' },
        { value: 99, color: 'green', op: 'lte', applies_to: 'account', account_id: 'acc-9' },
      ])
    ).toEqual([]);
  });
});

/*
 * A chart judges every drawn line on the samples it is showing. These fixtures
 * are a one-account chart sampled every 30s for 10 minutes, ending at t=600s.
 */
describe('a chart’s thresholds, line by line', () => {
  const STEP = 30_000;
  const axis = Array.from({ length: 21 }, (_, i) => i * STEP);
  const chart = (thresholds: PanelThresholdStep[]) => panelWith({ thresholds }, 'timeseries');
  const line = (label: string, values: (number | null)[], extra: Partial<PanelSeries> = {}): PanelSeries => ({ label, values, ...extra });
  const accounts = [{ value: 'acc-1', label: 'prod-eu' }];
  const flat = (v: number) => axis.map(() => v);

  it('fires a step with no hold on a line’s latest sample, and names the line', () => {
    const out = lineBreaches(chart(steps([80, 'red'])), [line('api', flat(90)), line('web', flat(10))], axis, accounts);
    expect(out.frame?.line).toBe('api');
    expect(out.all).toHaveLength(1);
    expect(out.spans).toEqual([]);
  });

  it('holds a step only once every sample inside the hold is past the line', () => {
    const hold: PanelThresholdStep = { value: 70, color: 'red', for: 300 };
    // Above 70 for the last 6 minutes: 12 samples, more than the 10 the hold needs.
    const sustained = axis.map((t) => (t >= 240_000 ? 85 : 20));
    // A spike: above 70 for the last 90 seconds only.
    const spike = axis.map((t) => (t >= 510_000 ? 85 : 20));
    const out = lineBreaches(chart([hold]), [line('sustained', sustained), line('spike', spike)], axis, accounts);
    expect(out.all.map((b) => b.line)).toEqual(['sustained']);
    expect(out.frame?.from).toBe(240_000);
    expect(out.frame?.to).toBe(600_000);
    // The band covers the whole run, which started before the hold did.
    expect(out.spans).toEqual([{ series: 0, step: hold, from: 8, to: 20 }]);
  });

  it('resets the hold on a gap longer than the line’s own spacing, but not on the shared axis’s nulls', () => {
    const hold: PanelThresholdStep = { value: 70, color: 'red', for: 300 };
    // A coarser line, sampled every 60s on a 30s axis: every other instant is null, which is not a gap.
    const coarse = axis.map((t, i) => (i % 2 === 0 ? 85 : null));
    // A line that went quiet for two minutes inside the hold.
    const quiet = axis.map((t) => (t >= 420_000 && t < 540_000 ? null : 85));
    const out = lineBreaches(chart([hold]), [line('coarse', coarse), line('quiet', quiet)], axis, accounts);
    expect(out.all.map((b) => b.line)).toEqual(['coarse']);
  });

  it('does not extend the band back across a gap', () => {
    const hold: PanelThresholdStep = { value: 70, color: 'red', for: 300 };
    // Above the line at the start, then quiet for three and a half minutes, then above it for the last six.
    const values = axis.map((t) => (t < 60_000 ? 90 : t < 240_000 ? null : 90));
    const out = lineBreaches(chart([hold]), [line('api', values)], axis, accounts);
    expect(out.frame?.from).toBe(240_000);
    expect(out.spans[0]).toMatchObject({ from: 8, to: 20 });
  });

  it('ignores a line that ended well before the chart did', () => {
    // Six minutes before the end: past the five-minute allowance for a late provider.
    const dead = axis.map((t) => (t <= 240_000 ? 95 : null));
    const out = lineBreaches(chart(steps([80, 'red'])), [line('dead', dead)], axis, accounts);
    expect(out.frame).toBeUndefined();
  });

  it('draws the consolidated total but never judges it', () => {
    const out = lineBreaches(
      chart(steps([80, 'red'])),
      [
        line('a · cpu', flat(10), { accountLabel: 'a' }),
        line('b · cpu', flat(10), { accountLabel: 'b' }),
        line('All accounts', flat(90), { consolidated: true }),
      ],
      axis,
      [
        { value: 'a', label: 'a' },
        { value: 'b', label: 'b' },
      ]
    );
    expect(out.frame).toBeUndefined();
  });

  it('reports a hold the sampling is too coarse to judge instead of firing or ignoring it', () => {
    // Sampled every 10 minutes: a 5-minute hold has at most one sample in it.
    const coarseAxis = Array.from({ length: 7 }, (_, i) => i * 600_000);
    const hold: PanelThresholdStep = { value: 70, color: 'red', for: 300 };
    const out = lineBreaches(
      chart([hold, { value: 95, color: 'amber' }]),
      [
        line(
          'api',
          coarseAxis.map(() => 99)
        ),
      ],
      coarseAxis,
      accounts
    );
    expect(out.tooCoarse).toEqual([hold]);
    // The step without a hold still fires.
    expect(out.frame?.step.value).toBe(95);
  });

  it('points an account step at that account’s lines only', () => {
    const out = lineBreaches(
      chart([{ value: 80, color: 'red', applies_to: 'account', account_id: 'b' }]),
      [line('a · cpu', flat(90), { accountLabel: 'a' }), line('b · cpu', flat(90), { accountLabel: 'b' })],
      axis,
      [
        { value: 'a', label: 'a' },
        { value: 'b', label: 'b' },
      ]
    );
    expect(out.all.map((b) => b.line)).toEqual(['b · cpu']);
  });

  it('frames the panel for the most severe line and lists the rest', () => {
    const out = lineBreaches(chart(steps([80, 'red'], [50, 'amber'])), [line('warm', flat(60)), line('hot', flat(90))], axis, accounts);
    expect(out.frame?.line).toBe('hot');
    expect(out.all.map((b) => b.line)).toEqual(['hot', 'warm']);
  });

  it('rejects a hold outside two minutes to an hour', () => {
    expect(isCompleteStep({ value: 80, color: 'red', for: 60 })).toBe(false);
    expect(isCompleteStep({ value: 80, color: 'red', for: 120 })).toBe(true);
    expect(isCompleteStep({ value: 80, color: 'red', for: 3600 })).toBe(true);
    expect(isCompleteStep({ value: 80, color: 'red', for: 3601 })).toBe(false);
    expect(isCompleteStep({ value: 80, color: 'red', for: 150.5 })).toBe(false);
  });

  it('writes a hold the way the badge does', () => {
    expect(holdLabel(300)).toBe('5m');
    expect(holdLabel(3600)).toBe('1h');
    expect(holdLabel(5400)).toBe('90m');
  });
});

/*
 * Regressions found by re-testing #40268 with worst-case inputs: each of these
 * failed on the first version of the line evaluator.
 */
describe('a chart’s holds, worst cases', () => {
  const STEP = 30_000;
  const axis = Array.from({ length: 121 }, (_, i) => i * STEP); // an hour at 30s
  const END = axis[axis.length - 1];
  const range = { start: 0, end: END };
  const chart = (thresholds: PanelThresholdStep[], type = 'timeseries') => panelWith({ thresholds }, type);
  const line = (label: string, values: (number | null)[], extra: Partial<PanelSeries> = {}): PanelSeries => ({ label, values, ...extra });
  const accounts = [{ value: 'acc-1', label: 'prod-eu' }];
  const hold5m: PanelThresholdStep = { value: 80, color: 'red', for: 300 };

  it('does not fire for a line younger than the hold', () => {
    // A pod that appeared four minutes ago, already hot.
    const young = axis.map((t) => (t >= END - 240_000 ? 90 : null));
    expect(lineBreaches(chart([hold5m]), [line('young', young)], axis, accounts, range).frame).toBeUndefined();
  });

  it('fires once the run of crossing samples spans the hold exactly', () => {
    const exact = axis.map((t) => (t >= END - 300_000 ? 90 : 10));
    const out = lineBreaches(chart([hold5m]), [line('exact', exact)], axis, accounts, range);
    expect(out.frame?.from).toBe(END - 300_000);
  });

  it('judges currency against the requested end, so a panel whose every line went quiet stays plain', () => {
    // The only line stopped forty minutes ago at 95; the axis ends with it.
    const stale = axis.slice(0, 41);
    const out = lineBreaches(
      chart([hold5m]),
      [
        line(
          'stale',
          stale.map(() => 95)
        ),
      ],
      stale,
      accounts,
      range
    );
    expect(out.frame).toBeUndefined();
  });

  it('still counts a line a late provider has not caught up on', () => {
    // Three minutes behind the end: inside the five-minute allowance.
    const late = axis.map((t) => (t <= END - 180_000 ? 95 : null));
    expect(lineBreaches(chart([hold5m]), [line('late', late)], axis, accounts, range).frame?.line).toBe('late');
  });

  it('reports a hold longer than the time range instead of skipping it silently', () => {
    const fifteen = axis.slice(0, 31); // fifteen minutes
    const hold30: PanelThresholdStep = { value: 80, color: 'red', for: 1800 };
    const out = lineBreaches(
      chart([hold30]),
      [
        line(
          'hot',
          fifteen.map(() => 90)
        ),
      ],
      fifteen,
      accounts,
      { start: 0, end: fifteen[30] }
    );
    expect(out.frame).toBeUndefined();
    expect(out.tooLong).toEqual([hold30]);
  });

  it('takes holds in whole minutes only, so the badge never rounds one', () => {
    expect(isCompleteStep({ value: 80, color: 'red', for: 150 })).toBe(false);
    expect(isCompleteStep({ value: 80, color: 'red', for: 180 })).toBe(true);
  });

  it('leaves a stat or gauge step that carries a hold undrawn, rather than claiming a hold it cannot judge', () => {
    const stat = chart([hold5m, { value: 50, color: 'amber' }], 'stat');
    expect(panelThresholdsOf(stat)).toEqual([{ value: 50, color: 'amber' }]);
    expect(panelBreach(stat, 90)).toEqual({ value: 50, color: 'amber' });
  });

  it('keeps a quiet breaching line through the cap only for the account its step names', () => {
    const scoped = chart([{ value: 1, color: 'red', op: 'lte', applies_to: 'account', account_id: 'acc-1' }]);
    const accountOf = (s: { accountLabel?: string }) => (s.accountLabel === 'prod-eu' ? 'acc-1' : 'acc-2');
    const prefer = breachingLinePreference(scoped, accountOf)!;
    expect(prefer({ label: 'eu', accountLabel: 'prod-eu', timestamps: [0], values: [0] })).toBe(true);
    expect(prefer({ label: 'us', accountLabel: 'prod-us', timestamps: [0], values: [0] })).toBe(false);
    expect(breachingLinePreference(chart([]), accountOf)).toBeUndefined();
  });

  it('keys the cap preference on what decides it, and only for charts', () => {
    expect(linePreferenceKey(chart([hold5m]))).not.toBe('');
    expect(linePreferenceKey(chart([hold5m], 'stat'))).toBe('');
    // The colour and the hold do not change which lines cross.
    expect(linePreferenceKey(chart([{ ...hold5m, color: 'amber', for: 600 }]))).toBe(linePreferenceKey(chart([hold5m])));
  });
});
