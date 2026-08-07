import { summarizeValue, getTableDataFromArrayOfObject } from '@lib/util';

// util.js renders cells through these components; stub them so importing the
// module doesn't pull MUI/markdown into a pure-logic test.
jest.mock('@shared/format/Text', () => () => null);
jest.mock('@shared/viewers/MarkDowns', () => () => null);

describe('summarizeValue', () => {
  it('picks the human-readable key from an object (gh issue author shape)', () => {
    const author = { id: 'MDQ6VXNlcjNjMDMz', is_bot: false, login: 'rohitutekar123', name: 'Rohit Utekar' };
    expect(summarizeValue(author)).toBe('Rohit Utekar');
  });

  it('falls back through the key priority order when earlier keys are absent', () => {
    expect(summarizeValue({ id: 'x1', login: 'rohitutekar123' })).toBe('rohitutekar123');
  });

  it('summarizes an array of objects as a comma list (gh issue labels shape)', () => {
    const labels = [
      { id: 'LA_1', name: 'bug', color: 'd73a4a' },
      { id: 'LA_2', name: 'Ready', color: 'ededed' },
    ];
    expect(summarizeValue(labels)).toBe('bug, Ready');
  });

  it('joins arrays of primitives', () => {
    expect(summarizeValue(['a', 'b'])).toBe('a, b');
  });

  it('drops nullish entries when summarizing arrays instead of emitting stray commas', () => {
    expect(summarizeValue([null, { name: 'x' }])).toBe('x');
    expect(summarizeValue([null, undefined])).toBe('');
  });

  it('stringifies objects with no recognizable identity key', () => {
    expect(summarizeValue({ foo: 1 })).toBe('{"foo":1}');
  });

  it('stringifies primitives, keeping falsy values visible', () => {
    expect(summarizeValue(0)).toBe('0');
    expect(summarizeValue(false)).toBe('false');
  });

  it('returns undefined for null/undefined so the cell shows the default placeholder', () => {
    expect(summarizeValue(null)).toBeUndefined();
    expect(summarizeValue(undefined)).toBeUndefined();
  });
});

describe('getTableDataFromArrayOfObject', () => {
  it('divides the row width evenly across columns and builds one row per item', () => {
    const { headers, tableData } = getTableDataFromArrayOfObject([
      { title: 'Issue A', number: 1 },
      { title: 'Issue B', number: 2 },
    ]);
    // Equal widths summing to 100% — CustomTable defaults widthless headers
    // to 20% each, which over-constrains tables with 6+ columns.
    expect(headers).toEqual([
      { name: 'title', width: '50.00%' },
      { name: 'number', width: '50.00%' },
    ]);
    expect(tableData).toHaveLength(2);
  });

  it('returns empty output for null/primitive input instead of throwing', () => {
    expect(getTableDataFromArrayOfObject(null)).toEqual({ headers: [], tableData: [] });
    expect(getTableDataFromArrayOfObject([null])).toEqual({ headers: [], tableData: [] });
    expect(getTableDataFromArrayOfObject(['a', 'b'])).toEqual({ headers: [], tableData: [] });
  });
});
