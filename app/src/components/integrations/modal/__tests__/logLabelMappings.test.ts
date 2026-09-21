/**
 * Round-trip contract for the log_label_mappings blob.
 *
 * The wire shape is shared with the backend resolver, and config values are upserted
 * per name — never wiped — so both halves of the round trip carry real consequences:
 * a dropped row silently reverts a mapping, and omitting a cleared value leaves the
 * old mapping in force.
 */
import { parseLogLabelMappings, serializeLogLabelMappings } from '../LogLabelMappingCards';
import { indexForAccount } from '../useLogFieldOptions';

describe('parseLogLabelMappings', () => {
  it('turns the stored blob into per-account cards', () => {
    const cards = parseLogLabelMappings(
      JSON.stringify([{ accountId: 'acc-1', mappings: { pod: 'kubernetes.pod_name.keyword', namespace: 'kubernetes.namespace_name' } }])
    );

    expect(cards).toHaveLength(1);
    expect(cards[0].accountId).toBe('acc-1');
    // Sorted, so the row order does not shuffle between reloads.
    expect(cards[0].rows).toEqual([
      { canonical: 'namespace', field: 'kubernetes.namespace_name' },
      { canonical: 'pod', field: 'kubernetes.pod_name.keyword' },
    ]);
  });

  it('keeps accounts separate', () => {
    const cards = parseLogLabelMappings(
      JSON.stringify([
        { accountId: 'acc-1', mappings: { pod: 'a_pod' } },
        { accountId: 'acc-2', mappings: { pod: 'b_pod' } },
      ])
    );

    expect(cards.map((c) => c.accountId)).toEqual(['acc-1', 'acc-2']);
    expect(cards[1].rows).toEqual([{ canonical: 'pod', field: 'b_pod' }]);
  });

  it('degrades to a blank card rather than throwing on unusable input', () => {
    // A corrupt value must not make the integration uneditable.
    for (const input of ['not json{{{', '', '[]', 'null', undefined]) {
      const cards = parseLogLabelMappings(input as never);
      expect(cards).toHaveLength(1);
      expect(cards[0].accountId).toBe('');
      expect(cards[0].rows).toEqual([{ canonical: '', field: '' }]);
    }
  });

  it('gives an account with no mappings one blank row to type into', () => {
    const cards = parseLogLabelMappings(JSON.stringify([{ accountId: 'acc-1', mappings: {} }]));
    expect(cards[0].rows).toEqual([{ canonical: '', field: '' }]);
  });
});

describe('serializeLogLabelMappings', () => {
  it('round-trips through parse unchanged', () => {
    const stored = [{ accountId: 'acc-1', mappings: { namespace: 'kubernetes.namespace_name', pod: 'kubernetes.pod_name.keyword' } }];
    expect(serializeLogLabelMappings(parseLogLabelMappings(JSON.stringify(stored)))).toEqual(stored);
  });

  it('drops half-typed rows', () => {
    // A concept with no field would rename the column to "" and break every query
    // filtering on it; a field with no concept has nothing to attach to.
    const out = serializeLogLabelMappings([
      {
        accountId: 'acc-1',
        rows: [
          { canonical: 'pod', field: 'kubernetes.pod_name' },
          { canonical: 'namespace', field: '' },
          { canonical: '', field: 'orphan_field' },
        ],
      },
    ]);

    expect(out).toEqual([{ accountId: 'acc-1', mappings: { pod: 'kubernetes.pod_name' } }]);
  });

  it('trims surrounding whitespace', () => {
    const out = serializeLogLabelMappings([{ accountId: ' acc-1 ', rows: [{ canonical: ' pod ', field: ' kubernetes.pod_name ' }] }]);
    expect(out).toEqual([{ accountId: 'acc-1', mappings: { pod: 'kubernetes.pod_name' } }]);
  });

  it('drops a card with no account', () => {
    const out = serializeLogLabelMappings([{ accountId: '', rows: [{ canonical: 'pod', field: 'kubernetes.pod_name' }] }]);
    expect(out).toEqual([]);
  });

  it('returns an empty array when every row is cleared', () => {
    // The caller relies on this to emit `[]` for a previously-saved mapping. Config
    // values are upserted per name, so omitting the key instead would leave the old
    // mapping live even though the operator deleted every row.
    expect(serializeLogLabelMappings([{ accountId: 'acc-1', rows: [{ canonical: '', field: '' }] }])).toEqual([]);
    expect(serializeLogLabelMappings([])).toEqual([]);
  });
});

describe('indexForAccount', () => {
  const rules = [
    { accountId: 'acc-1', log_index: 'logs-prod-*' },
    { accountId: 'acc-2', log_index: '' },
  ];

  it("uses the account's own per-account index when it has one", () => {
    // One ES endpoint serves several accounts, each with its own index — reading the
    // top-level one would offer fields that do not exist in the index this account's
    // queries run against.
    expect(indexForAccount(rules, 'acc-1', 'logs-default-*')).toBe('logs-prod-*');
  });

  it('falls back to the top-level index when the account has no override', () => {
    expect(indexForAccount(rules, 'acc-2', 'logs-default-*')).toBe('logs-default-*');
    expect(indexForAccount(rules, 'acc-3', 'logs-default-*')).toBe('logs-default-*');
  });

  it('returns empty when nothing is configured, letting the provider decide', () => {
    expect(indexForAccount(rules, 'acc-2', '')).toBe('');
    expect(indexForAccount([], 'acc-1', undefined as never)).toBe('');
  });

  it('returns empty without an account', () => {
    expect(indexForAccount(rules, '', 'logs-default-*')).toBe('');
  });

  it('trims, so a whitespace-only index is treated as unset', () => {
    expect(indexForAccount([{ accountId: 'acc-1', log_index: '   ' }], 'acc-1', 'logs-default-*')).toBe('logs-default-*');
  });
});
