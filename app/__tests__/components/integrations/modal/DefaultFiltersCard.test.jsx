import { hasCardMissingAccount, parseDefaultFilters, serializeDefaultFilters } from '@components/integrations/modal/DefaultFiltersCard';

// These helpers back BOTH the log (`default_filters`) and trace
// (`default_trace_filters`) cards, and their output is the JSON the Go side
// unmarshals into []accountDefaultFilters — so a shape change here silently
// disables an operator's standing filter rather than failing anywhere visible.

describe('parseDefaultFilters', () => {
  it('reads the stored shape into editable cards, dropping op', () => {
    const raw = JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'service_name', op: '_eq', value: 'checkout' }] }]);
    expect(parseDefaultFilters(raw)).toEqual([{ accountId: 'acc-1', filters: [{ key: 'service_name', value: 'checkout' }] }]);
  });

  it('accepts an already-parsed array as well as a JSON string', () => {
    const parsed = [{ accountId: 'acc-1', filters: [{ key: 'k', value: 'v' }] }];
    expect(parseDefaultFilters(parsed)).toEqual(parsed);
  });

  it('normalises an object accountId, as the account dropdown emits', () => {
    const raw = JSON.stringify([{ accountId: { value: 'acc-9', label: 'prod' }, filters: [{ key: 'k', value: 'v' }] }]);
    expect(parseDefaultFilters(raw)[0].accountId).toBe('acc-9');
  });

  it('coerces a non-string value so the Input stays controlled', () => {
    const raw = JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'http_status_code', value: 500 }] }]);
    expect(parseDefaultFilters(raw)[0].filters[0].value).toBe('500');
  });

  // A corrupt or empty config must leave the integration editable, not throw.
  it.each([
    ['not json', 'oops'],
    ['empty array', '[]'],
    ['null', null],
    ['non-array', '{"a":1}'],
  ])('degrades %s to one blank card', (_name, raw) => {
    expect(parseDefaultFilters(raw)).toEqual([{ accountId: '', filters: [{ key: '', value: '' }] }]);
  });
});

describe('serializeDefaultFilters', () => {
  it('writes op:_eq and trims both sides', () => {
    const cards = [{ accountId: 'acc-1', filters: [{ key: '  service_name ', value: ' checkout  ' }] }];
    expect(serializeDefaultFilters(cards)).toEqual([{ accountId: 'acc-1', filters: [{ key: 'service_name', op: '_eq', value: 'checkout' }] }]);
  });

  it('drops half-filled rows and cards with no account', () => {
    const cards = [
      {
        accountId: 'acc-1',
        filters: [
          { key: 'a', value: '1' },
          { key: 'b', value: '' },
          { key: '', value: '2' },
        ],
      },
      { accountId: '', filters: [{ key: 'c', value: '3' }] },
    ];
    expect(serializeDefaultFilters(cards)).toEqual([{ accountId: 'acc-1', filters: [{ key: 'a', op: '_eq', value: '1' }] }]);
  });

  // Clearing every row must serialize to [] rather than nothing: config values are
  // upserted per name, so omitting the key would leave the old filter applied and
  // make "remove this filter" a no-op.
  it('yields an empty array when every row is cleared', () => {
    expect(serializeDefaultFilters([{ accountId: 'acc-1', filters: [{ key: '', value: '' }] }])).toEqual([]);
    expect(JSON.stringify(serializeDefaultFilters([]))).toBe('[]');
  });
});

describe('hasCardMissingAccount', () => {
  it('flags a card with filters typed but no account selected', () => {
    expect(hasCardMissingAccount([{ accountId: '', filters: [{ key: 'a', value: '' }] }])).toBe(true);
  });

  it('ignores an untouched blank card', () => {
    expect(hasCardMissingAccount([{ accountId: '', filters: [{ key: '', value: '' }] }])).toBe(false);
  });

  it('passes a fully filled card', () => {
    expect(hasCardMissingAccount([{ accountId: 'acc-1', filters: [{ key: 'a', value: '1' }] }])).toBe(false);
  });
});
