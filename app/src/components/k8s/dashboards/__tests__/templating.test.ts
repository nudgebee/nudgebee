import { referencedVariables, renderTemplate } from '../templating';

describe('renderTemplate', () => {
  it('substitutes bare and braced tokens', () => {
    expect(renderTemplate('up{ns="$namespace"}', { namespace: 'payments' })).toBe('up{ns="payments"}');
    expect(renderTemplate('up{ns="${namespace}"}', { namespace: 'payments' })).toBe('up{ns="payments"}');
  });

  /**
   * The regression this module exists for. The legacy renderer looped
   * `String.replaceAll` over map keys, so a shorter variable whose name is a
   * prefix of a longer one would eat the longer one's text depending purely on
   * object insertion order.
   */
  it('never lets a shorter variable eat a longer one, in either declaration order', () => {
    const query = 'sum(rate(x{pod="$podName", ns="$namespaceName"}[5m]))';
    const expected = 'sum(rate(x{pod="checkout-0", ns="payments"}[5m]))';

    const shortFirst = { pod: 'WRONG', podName: 'checkout-0', namespace: 'WRONG', namespaceName: 'payments' };
    const longFirst = { namespaceName: 'payments', namespace: 'WRONG', podName: 'checkout-0', pod: 'WRONG' };

    expect(renderTemplate(query, shortFirst)).toBe(expected);
    expect(renderTemplate(query, longFirst)).toBe(expected);
  });

  it('leaves unknown variables verbatim rather than blanking them', () => {
    // A blanked token would silently produce a valid-looking query over the
    // wrong series; leaving it visible surfaces the typo.
    expect(renderTemplate('up{ns="$typo"}', { namespace: 'payments' })).toBe('up{ns="$typo"}');
  });

  it('returns the input untouched when there is nothing to substitute', () => {
    expect(renderTemplate('up{ns="payments"}', { namespace: 'x' })).toBe('up{ns="payments"}');
    expect(renderTemplate('', { namespace: 'x' })).toBe('');
  });

  it('substitutes every occurrence, not just the first', () => {
    expect(renderTemplate('$a + $a', { a: '1' })).toBe('1 + 1');
  });
});

describe('referencedVariables', () => {
  it('collects bare and braced names without duplicates', () => {
    expect(referencedVariables('$a + ${b} + $a').sort()).toEqual(['a', 'b']);
  });

  it('returns an empty list for a query with no variables', () => {
    expect(referencedVariables('up{ns="payments"}')).toEqual([]);
    expect(referencedVariables('')).toEqual([]);
  });
});

describe('renderTemplate with a dropdown', () => {
  const picked = (values: string[], all = false, allValue?: string) => ({ n: { values, all, ...(allValue ? { allValue } : {}) } });

  it('writes a plain token as a PromQL regex', () => {
    expect(renderTemplate('x=~"$n"', picked(['a.b', 'c']))).toBe('x=~"a\\\\.b|c"');
    expect(renderTemplate('x=~"$n"', picked(['a', 'b'], true))).toBe('x=~".*"');
  });

  it.each([
    ['regex', '(a\\.b|c)'],
    ['pipe', 'a.b|c'],
    ['csv', 'a.b,c'],
    ['raw', 'a.b,c'],
    ['singlequote', "'a.b','c'"],
    ['doublequote', '"a.b","c"'],
    ['sqlstring', "'a.b','c'"],
    ['json', '["a.b","c"]'],
    ['lucene', '("a.b" OR "c")'],
  ])('writes ${n:%s}', (format, expected) => {
    expect(renderTemplate(`\${n:${format}}`, picked(['a.b', 'c']))).toBe(expected);
  });

  it('doubles a quote for sqlstring and escapes it for singlequote', () => {
    expect(renderTemplate('${n:sqlstring}', picked(["o'x"]))).toBe("'o''x'");
    expect(renderTemplate('${n:singlequote}', picked(["o'x"]))).toBe("'o\\'x'");
  });

  it('leaves an unknown format verbatim, so the typo shows', () => {
    expect(renderTemplate('${n:nope}', picked(['a']))).toBe('${n:nope}');
  });

  it('spells out every option for All in a list format, and uses the wildcard in a pattern one', () => {
    expect(renderTemplate('${n:csv}', picked(['a', 'b'], true))).toBe('a,b');
    expect(renderTemplate('${n:regex}', picked(['a', 'b'], true))).toBe('.*');
  });

  it('sends a custom All value as written, whatever the format', () => {
    expect(renderTemplate('x=~"$n"', picked(['a'], true, '.+'))).toBe('x=~".+"');
    expect(renderTemplate('${n:csv}', picked(['a'], true, 'a\\.b'))).toBe('a\\.b');
  });

  it('counts a formatted token as a reference', () => {
    expect(referencedVariables('${n:csv} $m ${o}')).toEqual(['n', 'm', 'o']);
  });
});

describe('renderTemplate on a logs panel', () => {
  it('writes All so a Loki selector keeps a non-empty matcher', () => {
    expect(renderTemplate('{app=~"$n"}', { n: { values: ['a'], all: true } }, 'logql')).toBe('{app=~".+"}');
    expect(renderTemplate('{app=~"$n"}', { n: { values: ['a.b'], all: false } }, 'logql')).toBe('{app=~"a\\\\.b"}');
  });
});
