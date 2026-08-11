import { discreteSignature, fetchSignature, isRunnable, previewRangeLabel, sampleData, usesManualRun } from '../panelPreviewRules';
import type { Panel } from '@api1/dashboards';

function panel(over: Partial<Panel> = {}): Panel {
  return {
    id: 1,
    title: 'p1',
    type: 'timeseries',
    datasource: 'metrics',
    account_type: 'AWS',
    account_ids: [],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', expr: 'up' }],
    ...over,
  };
}

describe('isRunnable', () => {
  it('does not wait for a title', () => {
    // The preview is the reason to open the editor; gating it on a field that
    // has nothing to do with the result would hide it while you author.
    expect(isRunnable(panel({ title: '' }))).toBe(true);
  });

  it('needs an account, because every backend resolves its provider from one', () => {
    expect(isRunnable(panel({ account_type: undefined, account_ids: [] }))).toBe(false);
    expect(isRunnable(panel({ account_type: undefined, account_ids: ['acc-1'] }))).toBe(true);
  });

  it('treats a blank or whitespace query as nothing to run', () => {
    expect(isRunnable(panel({ targets: [{ ref_id: 'A', expr: '' }] }))).toBe(false);
    expect(isRunnable(panel({ targets: [{ ref_id: 'A', expr: '   ' }] }))).toBe(false);
    expect(isRunnable(panel({ targets: undefined }))).toBe(false);
  });

  it('reads the structured query for entity datasources, not the text one', () => {
    // nudgebee / traces are authored through the builder, so `expr` is never set
    // and checking it would say "not ready" for a complete panel.
    const built = panel({ datasource: 'nudgebee', targets: [{ ref_id: 'A', query: { table: 'events' } as any }] });
    expect(isRunnable(built)).toBe(true);
    expect(isRunnable(panel({ datasource: 'nudgebee', targets: [{ ref_id: 'A', expr: 'up' }] }))).toBe(false);
  });

  it('runs a text panel with nothing else on it', () => {
    expect(isRunnable(panel({ type: 'text', account_type: undefined, targets: undefined, content: 'hi' }))).toBe(true);
  });
});

describe('usesManualRun', () => {
  it('holds back the datasources that execute against a live server', () => {
    expect(usesManualRun('redis')).toBe(true);
    expect(usesManualRun('rabbitmq')).toBe(true);
    expect(usesManualRun('postgresql')).toBe(true);
  });

  it('lets the read-only datasources preview as you type', () => {
    expect(usesManualRun('metrics')).toBe(false);
    expect(usesManualRun('logs')).toBe(false);
    expect(usesManualRun('traces')).toBe(false);
    expect(usesManualRun('nudgebee')).toBe(false);
  });
});

describe('signatures', () => {
  it('ignores fields the query does not depend on', () => {
    // Renaming a panel must not cost a provider request.
    expect(fetchSignature(panel({ title: 'a' }))).toBe(fetchSignature(panel({ title: 'b', description: 'x', unit: 'ms' })));
  });

  it('moves the fetch signature but not the discrete one when only the query is typed', () => {
    const before = panel();
    const after = panel({ targets: [{ ref_id: 'A', expr: 'up{job="api"}' }] });
    expect(fetchSignature(after)).not.toBe(fetchSignature(before));
    // This is what keeps typing on the debounce and clicks off it.
    expect(discreteSignature(after)).toBe(discreteSignature(before));
  });

  it('moves both when the datasource, visualisation or scope changes', () => {
    const before = panel();
    for (const after of [panel({ datasource: 'logs' }), panel({ type: 'stat' }), panel({ account_type: 'GCP' })]) {
      expect(discreteSignature(after)).not.toBe(discreteSignature(before));
      expect(fetchSignature(after)).not.toBe(fetchSignature(before));
    }
  });
});

describe('sampleData', () => {
  const HOUR = 60 * 60 * 1000;

  it('draws the same chart every time it is called', () => {
    // `Math.random` here would redraw on every keystroke, and the preview would
    // look like it was reacting to input it never received.
    expect(sampleData(0, HOUR)).toEqual(sampleData(0, HOUR));
  });

  it('gives every series a value at every point', () => {
    // A gap renders as a break in the line. The sample is meant to show what the
    // visualisation looks like working, not what a patchy series looks like.
    const { labels, series } = sampleData(0, HOUR);
    expect(series.length).toBeGreaterThan(1);
    for (const s of series) {
      expect(s.values).toHaveLength(labels.length);
      expect(s.values.every((v) => typeof v === 'number' && Number.isFinite(v))).toBe(true);
    }
  });

  it('spans the window it is given', () => {
    // The axis is the real range, so the sample is not obvious from its axis
    // alone — it is the caption's job to say it is a sample.
    const wide = sampleData(0, 24 * HOUR);
    const narrow = sampleData(0, HOUR);
    expect(wide.labels[0]).not.toBe(wide.labels[wide.labels.length - 1]);
    expect(wide.labels[wide.labels.length - 1]).not.toBe(narrow.labels[narrow.labels.length - 1]);
  });
});

describe('previewRangeLabel', () => {
  const MIN = 60 * 1000;

  it('states the window in the unit that reads naturally', () => {
    expect(previewRangeLabel(0, 15 * MIN)).toBe('Last 15 min');
    expect(previewRangeLabel(0, 60 * MIN)).toBe('Last 60 min');
    expect(previewRangeLabel(0, 6 * 60 * MIN)).toBe('Last 6 h');
    expect(previewRangeLabel(0, 7 * 24 * 60 * MIN)).toBe('Last 7 d');
  });

  it('never reports a zero-length window', () => {
    // A range under a minute is a picker artefact, not "no time at all".
    expect(previewRangeLabel(0, 0)).toBe('Last 1 min');
  });
});
