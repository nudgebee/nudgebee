import {
  optionsFromResults,
  readVariables,
  regexValue,
  sameVariables,
  variableColumns,
  variableErrors,
  variableReach,
  variableTables,
  variableUses,
  variableValues,
} from '../dashboardVariables';
import { renderTemplate } from '../templating';

const scaleSet = { name: 'scale_set', query: 'gha_max_runners', label_key: 'name' };

describe('dashboard variables', () => {
  it('lists each label value once, sorted, and skips series without the label', () => {
    const results = [
      { payload: [{ metric: { name: 'b' } }, { metric: { name: 'a' } }] },
      { payload: [{ metric: { name: 'a' } }, { metric: { other: 'x' } }] },
    ];
    expect(optionsFromResults(results, 'name')).toEqual(['a', 'b']);
    expect(optionsFromResults(undefined as any, 'name')).toEqual([]);
  });

  it('matches everything until a value is picked', () => {
    expect(renderTemplate('$scale_set', variableValues([scaleSet], {}))).toBe('.*');
    expect(renderTemplate('$scale_set', variableValues([scaleSet], { scale_set: ['runner-8g8cs'] }))).toBe('runner-8g8cs');
  });

  it('escapes a value so it matches only itself, surviving a PromQL string', () => {
    expect(regexValue('a.b')).toBe('a\\\\.b');
    expect(regexValue('runner-l')).toBe('runner-l');
    expect(regexValue('a"b')).toBe('a\\"b');
    expect(renderTemplate('x{y=~"$v"}', { v: regexValue('a"} or vector(1) #') })).toBe('x{y=~"a\\"\\\\} or vector\\\\(1\\\\) #"}');
  });

  it('fills a panel query, and leaves an undeclared token visible', () => {
    const values = variableValues([scaleSet], { scale_set: ['runner-l'] });
    expect(renderTemplate('gha_busy_runners{name=~"$scale_set"}', values)).toBe('gha_busy_runners{name=~"runner-l"}');
    expect(renderTemplate('x{y=~"$typo"}', values)).toBe('x{y=~"$typo"}');
  });

  it('joins several picks into one regex, each escaped, and treats an empty pick as all', () => {
    expect(renderTemplate('$scale_set', variableValues([{ ...scaleSet, multi: true }], { scale_set: ['a.b', 'c'] }))).toBe('a\\\\.b|c');
    expect(renderTemplate('$scale_set', variableValues([scaleSet], { scale_set: [] }))).toBe('.*');
  });

  it('applies the same rules as the server', () => {
    expect(variableErrors([scaleSet])).toEqual([]);
    expect(variableErrors([{ name: 'env', type: 'custom', values: ['dev'] }])).toEqual([]);
    expect(variableErrors([{ name: 'a-b', query: 'q', label_key: 'l' }])).toHaveLength(1);
    expect(variableErrors([scaleSet, scaleSet])).toHaveLength(1);
    expect(variableErrors([{ name: 'x', query: 'q' }])).toHaveLength(1);
    expect(variableErrors([{ name: 'env', type: 'custom', values: [' '] }])).toHaveLength(1);
  });

  it('treats key order and a missing query type as no change', () => {
    const saved = [{ name: 'a', query: 'q', label_key: 'l' }];
    const reordered = [{ label_key: 'l', type: 'query' as const, query: 'q', name: 'a' }];
    expect(sameVariables(saved, reordered)).toBe(true);
    expect(sameVariables(saved, [{ ...saved[0], multi: true }])).toBe(false);
    expect(sameVariables(undefined, [])).toBe(true);
  });
});

describe('variableUses', () => {
  const panel = (title: string, datasource: string, expr = '', query?: unknown) =>
    ({ title, datasource, targets: [{ ref_id: 'A', expr, query }] } as any);

  it('tells apart a panel that works, one that half works, one that ignores it and one that cannot use it', () => {
    const uses = variableUses(
      [
        panel('regex', 'metrics', 'up{ns=~"$n"}'),
        panel('exact', 'metrics', 'up{ns="$n"}'),
        panel('other', 'metrics', 'up'),
        panel('cmd', 'redis', 'INFO'),
      ],
      'n'
    );
    expect(uses.map((u) => u.kind)).toEqual(['works', 'partial', 'unused', 'blocked']);
  });

  it('does not take a formatted token or a longer name for an exact match', () => {
    expect(variableUses([panel('a', 'metrics', 'up{ns="${n:raw}"}')], 'n')[0].kind).toBe('works');
    expect(variableUses([panel('a', 'metrics', 'up{ns="$nn"}')], 'n')[0].kind).toBe('unused');
  });

  it('warns about a plain variable a logs query reads as text', () => {
    const logs = (expr: string) => variableUses([panel('a', 'logs', expr)], 'n')[0];
    expect(logs('kube_namespace:$n').kind).toBe('partial');
    expect(logs('kube_namespace:$n').reason).toContain('${n:lucene}');
    expect(logs('{"query":{"term":{"ns":"$n"}}}').kind).toBe('partial');
  });

  it('leaves a logs query that reads the variable properly alone', () => {
    const logs = (expr: string) => variableUses([panel('a', 'logs', expr)], 'n')[0].kind;
    expect(logs('{namespace=~"$n"}')).toBe('works');
    expect(logs('{namespace=~"a|${n}"}')).toBe('works');
    expect(logs("{namespace=~'$n'}")).toBe('works');
    expect(logs('kube_namespace:${n:lucene}')).toBe('works');
    expect(logs('{"query":{"terms":{"ns":${n:json}}}}')).toBe('works');
  });

  it('does not warn about the same plain variable on a metrics panel', () => {
    expect(variableUses([panel('a', 'metrics', 'rate(up[$n])')], 'n')[0].kind).toBe('works');
  });

  it('finds a variable in a builder query', () => {
    expect(variableUses([panel('a', 'nudgebee', '', { where: { _and: [{ _binary: { s: { _eq: '$n' } } }] } })], 'n')[0].kind).toBe('works');
  });

  it('skips panels with no query and names it cannot read', () => {
    expect(variableUses([{ title: 't', datasource: 'metrics', targets: [] } as any], 'n')).toEqual([]);
    expect(variableUses([panel('a', 'metrics', 'up')], '(')).toEqual([]);
  });
});

describe('a variable read from the query engine', () => {
  const variable = (table?: string, column?: string) => ({ name: 'status', type: 'nudgebee' as const, table, column });

  it('needs a table and a text column of it', () => {
    expect(variableErrors([variable('events_v2', 'status')])).toEqual([]);
    expect(variableErrors([variable('events_v2', '')])).toHaveLength(1);
    expect(variableErrors([variable('no_such_table', 'status')])).toHaveLength(1);
    expect(variableErrors([variable('events_v2', 'starts_at')])).toHaveLength(1);
  });

  it('offers only tables of the nudgebee datasource', () => {
    expect(variableTables().every((t) => t.datasource === 'nudgebee')).toBe(true);
    expect(variableColumns('no_such_table')).toEqual([]);
  });

  it('counts a changed column as a change to the variables', () => {
    expect(sameVariables([variable('events_v2', 'status')], [variable('events_v2', 'title')])).toBe(false);
  });
});

describe('variableReach', () => {
  const panel = (expr: string) => ({ title: expr, datasource: 'metrics', targets: [{ ref_id: 'A', expr }] } as any);

  it('counts the panels a dropdown changes, half-working ones included', () => {
    expect(variableReach([panel('a{n=~"$n"}'), panel('b{n="$n"}'), panel('c')], 'n')).toBe('Applies to 2 of 3 panels');
    expect(variableReach([panel('c')], 'n')).toBe('Applies to 0 of 1 panel');
  });
});

describe('regexValue with a backslash', () => {
  // What a PromQL double-quoted string hands the regex engine.
  const unquote = (text: string) => text.replace(/\\(.)/g, '$1');

  it.each(['C:\\temp', 'trailing\\', 'a.b', 'a"b'])('keeps %s matching only itself through the string', (value) => {
    expect(new RegExp(`^${unquote(regexValue(value))}$`).test(value)).toBe(true);
  });

  it('cannot escape the closing quote', () => {
    expect(regexValue('a\\')).toBe('a\\\\\\\\');
  });
});

describe('variableValues on a variable that is no longer multi-select', () => {
  it('substitutes only the pick the dropdown shows', () => {
    const values = variableValues([{ ...scaleSet, multi: false }], { scale_set: ['a', 'b'] });
    expect(renderTemplate('$scale_set', values)).toBe('a');
  });
});

describe('sameVariables', () => {
  it('reads an explicit multi: false as no change', () => {
    expect(sameVariables([{ ...scaleSet, multi: false }], [scaleSet])).toBe(true);
  });
});

describe('readVariables', () => {
  it('accepts nothing, and a well-formed list', () => {
    expect(readVariables(undefined)).toEqual({ variables: [], errors: [] });
    expect(readVariables([scaleSet])).toEqual({ variables: [scaleSet], errors: [] });
  });

  it.each([
    ['not a list', 'x'],
    ['a null entry', [null]],
    ['values as text', [{ name: 'a', type: 'custom', values: 'dev,prod' }]],
    ['values as numbers', [{ name: 'a', type: 'custom', values: [1, 2] }]],
    ['a number for a name', [{ name: 1 }]],
    ['multi as text', [{ ...scaleSet, multi: 'yes' }]],
    ['an unknown type', [{ ...scaleSet, type: 'magic' }]],
    ['a repeated name', [scaleSet, scaleSet]],
  ])('refuses %s', (_name, raw) => {
    const read = readVariables(raw);
    expect(read.variables).toEqual([]);
    expect(read.errors.length).toBeGreaterThan(0);
  });
});
