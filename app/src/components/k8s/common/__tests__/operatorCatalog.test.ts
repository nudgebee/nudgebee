import { getOperatorsForKind, getOperatorsForLabel, operatorAppliesToType, OperatorDescriptor } from '../operatorCatalog';

// Mirrors what the backend sends as capabilities.supported_operator_descriptors for a
// schema-backed provider (Pinot/Hive advertise exactly this operator set), including
// the applicable_data_types matrix from query.OperatorCatalog.
const PINOT_DESCRIPTORS: OperatorDescriptor[] = [
  { token: '_eq', chip_label: '=', kinds: ['chip'], applicable_data_types: ['string', 'number', 'bool', 'timestamp'] },
  { token: '_neq', chip_label: '!=', kinds: ['chip'], applicable_data_types: ['string', 'number', 'bool', 'timestamp'] },
  { token: '_is_null', chip_label: 'is null', kinds: ['chip'], applicable_data_types: ['string', 'number', 'bool', 'timestamp'] },
  { token: '_gt', chip_label: '>', kinds: ['chip'], applicable_data_types: ['string', 'number', 'timestamp'] },
  { token: '_lt', chip_label: '<', kinds: ['chip'], applicable_data_types: ['string', 'number', 'timestamp'] },
  { token: '_contains', chip_label: 'contains', line_label: 'Line contains', kinds: ['chip', 'line'], applicable_data_types: ['string'] },
  { token: '_like', chip_label: 'LIKE', line_label: 'Line matches pattern (LIKE)', kinds: ['chip', 'line'], applicable_data_types: ['string'] },
  { token: '_regex', chip_label: '=~', line_label: 'Line contains regex match', kinds: ['chip', 'line'], applicable_data_types: ['string'] },
  { token: '_nregex', chip_label: '!~', line_label: 'Line does not match regex', kinds: ['chip', 'line'], applicable_data_types: ['string'] },
];

const tokensOf = (options: { value: string }[]) => options.map((o) => o.value);

describe('getOperatorsForLabel — a numeric label must not be offered pattern operators', () => {
  // The reported bug: the builder offered =~ on an INT column and Pinot answered
  // with a raw REGEXP_LIKE type error.
  it('hides regex, contains and LIKE for a number', () => {
    const tokens = tokensOf(getOperatorsForLabel(PINOT_DESCRIPTORS, 'chip', 'number'));
    expect(tokens).not.toContain('_regex');
    expect(tokens).not.toContain('_nregex');
    expect(tokens).not.toContain('_contains');
    expect(tokens).not.toContain('_like');
  });

  it('keeps equality and ordering for a number', () => {
    const tokens = tokensOf(getOperatorsForLabel(PINOT_DESCRIPTORS, 'chip', 'number'));
    expect(tokens).toEqual(expect.arrayContaining(['_eq', '_neq', '_gt', '_lt', '_is_null']));
  });

  it('offers the provider’s full list for a string', () => {
    const tokens = tokensOf(getOperatorsForLabel(PINOT_DESCRIPTORS, 'chip', 'string'));
    expect(tokens).toEqual(tokensOf(getOperatorsForKind(PINOT_DESCRIPTORS, 'chip')));
  });

  it('allows only equality and existence for a bool', () => {
    const tokens = tokensOf(getOperatorsForLabel(PINOT_DESCRIPTORS, 'chip', 'bool'));
    expect(tokens.sort()).toEqual(['_eq', '_is_null', '_neq']);
  });

  it('allows ordering but not patterns for a timestamp', () => {
    const tokens = tokensOf(getOperatorsForLabel(PINOT_DESCRIPTORS, 'chip', 'timestamp'));
    expect(tokens).toEqual(expect.arrayContaining(['_eq', '_gt', '_lt']));
    expect(tokens).not.toContain('_regex');
  });

  it('narrows line operators too, and keeps their line labels', () => {
    const lineOps = getOperatorsForLabel(PINOT_DESCRIPTORS, 'line', 'string');
    expect(tokensOf(lineOps)).toEqual(expect.arrayContaining(['_contains', '_regex']));
    expect(lineOps.find((o) => o.value === '_regex')?.label).toBe('Line contains regex match');
  });
});

// The feature must only ever NARROW a case we positively understand. Every one of
// these has to return the provider's full list, or a provider without type info
// (Loki, Splunk, Loggly, Dynatrace) would silently lose operators it supports.
describe('getOperatorsForLabel — fails open when the type is not known', () => {
  const full = tokensOf(getOperatorsForKind(PINOT_DESCRIPTORS, 'chip'));

  it.each([
    ['no data type passed', undefined],
    ['explicitly unknown', 'unknown'],
    ['empty string', ''],
  ])('returns the full list when %s', (_label, dataType) => {
    expect(tokensOf(getOperatorsForLabel(PINOT_DESCRIPTORS, 'chip', dataType as string | undefined))).toEqual(full);
  });

  it('returns the full list when descriptors predate applicable_data_types', () => {
    const legacy: OperatorDescriptor[] = PINOT_DESCRIPTORS.map((d) => ({
      token: d.token,
      chip_label: d.chip_label,
      line_label: d.line_label,
      kinds: d.kinds,
    }));
    expect(tokensOf(getOperatorsForLabel(legacy, 'chip', 'number'))).toEqual(full);
  });

  it('returns the full list when applicable_data_types is empty', () => {
    const empty: OperatorDescriptor[] = PINOT_DESCRIPTORS.map((d) => ({ ...d, applicable_data_types: [] }));
    expect(tokensOf(getOperatorsForLabel(empty, 'chip', 'number'))).toEqual(full);
  });

  it('returns nothing when there are no descriptors at all', () => {
    expect(getOperatorsForLabel(undefined, 'chip', 'number')).toEqual([]);
    expect(getOperatorsForLabel([], 'chip', 'number')).toEqual([]);
  });
});

describe('operatorAppliesToType', () => {
  const regex = PINOT_DESCRIPTORS.find((d) => d.token === '_regex') as OperatorDescriptor;
  const eq = PINOT_DESCRIPTORS.find((d) => d.token === '_eq') as OperatorDescriptor;

  it('rejects a string-only operator on a number', () => {
    expect(operatorAppliesToType(regex, 'number')).toBe(false);
  });

  it('accepts a string-only operator on a string', () => {
    expect(operatorAppliesToType(regex, 'string')).toBe(true);
  });

  it('accepts a universal operator on every type', () => {
    ['string', 'number', 'bool', 'timestamp'].forEach((t) => {
      expect(operatorAppliesToType(eq, t)).toBe(true);
    });
  });

  it('fails open on unknown', () => {
    expect(operatorAppliesToType(regex, 'unknown')).toBe(true);
  });
});
