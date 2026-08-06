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
