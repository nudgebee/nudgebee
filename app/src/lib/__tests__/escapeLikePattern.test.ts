import { escapeLikePattern } from '@lib/HttpService';

// A user-typed search term must be matched literally by SQL LIKE/ILIKE. `%`, `_` and the
// escape char `\` are special, so they are escaped before being wrapped with the caller's
// own `%…%`. Regression guard for #40558 (typing `%` returned every row).
describe('escapeLikePattern', () => {
  it('escapes the ILIKE wildcards % and _', () => {
    expect(escapeLikePattern('%')).toBe('\\%');
    expect(escapeLikePattern('_')).toBe('\\_');
    expect(escapeLikePattern('a%b_c')).toBe('a\\%b\\_c');
  });

  it('escapes the escape character itself', () => {
    expect(escapeLikePattern('a\\b')).toBe('a\\\\b');
  });

  it('leaves ordinary search text untouched', () => {
    expect(escapeLikePattern('orders')).toBe('orders');
    expect(escapeLikePattern('i-004f2d')).toBe('i-004f2d');
    expect(escapeLikePattern('')).toBe('');
  });
});
