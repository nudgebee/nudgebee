import { getOperatorDisplayLabel, getOperatorsForKind, normalizeLegacyOperator, OperatorDescriptor } from '@components1/k8s/common/operatorCatalog';

const SAMPLE_DESCRIPTORS: OperatorDescriptor[] = [
  { token: '_eq', chip_label: '=', kinds: ['chip'] },
  { token: '_neq', chip_label: '!=', kinds: ['chip'] },
  { token: '_contains', chip_label: 'contains', line_label: 'Line contains', kinds: ['chip', 'line'] },
  { token: '_regex', chip_label: '=~', line_label: 'Line contains regex match', kinds: ['chip', 'line'] },
  { token: '_line_only', line_label: 'Line only', kinds: ['line'] },
];

describe('operatorCatalog', () => {
  describe('getOperatorsForKind', () => {
    it('filters descriptors by chip kind and uses chip_label', () => {
      expect(getOperatorsForKind(SAMPLE_DESCRIPTORS, 'chip')).toEqual([
        { label: '=', value: '_eq' },
        { label: '!=', value: '_neq' },
        { label: 'contains', value: '_contains' },
        { label: '=~', value: '_regex' },
      ]);
    });

    it('filters descriptors by line kind and uses line_label', () => {
      expect(getOperatorsForKind(SAMPLE_DESCRIPTORS, 'line')).toEqual([
        { label: 'Line contains', value: '_contains' },
        { label: 'Line contains regex match', value: '_regex' },
        { label: 'Line only', value: '_line_only' },
      ]);
    });

    it('returns an empty array when descriptors is undefined', () => {
      expect(getOperatorsForKind(undefined, 'chip')).toEqual([]);
      expect(getOperatorsForKind(undefined, 'line')).toEqual([]);
    });

    it('returns an empty array when descriptors is an empty array', () => {
      expect(getOperatorsForKind([], 'chip')).toEqual([]);
      expect(getOperatorsForKind([], 'line')).toEqual([]);
    });

    it('falls back to the token when the requested kind label is missing', () => {
      const descriptors: OperatorDescriptor[] = [{ token: '_no_labels', kinds: ['chip'] }];
      expect(getOperatorsForKind(descriptors, 'chip')).toEqual([{ label: '_no_labels', value: '_no_labels' }]);
    });
  });

  describe('normalizeLegacyOperator', () => {
    it.each([
      ['=', '_eq'],
      ['!=', '_neq'],
      ['<', '_lt'],
      ['<=', '_lte'],
      ['>', '_gt'],
      ['>=', '_gte'],
      ['=~', '_regex'],
      ['!~', '_nregex'],
      ['CONTAINS', '_contains'],
      ['ICONTAINS', '_icontains'],
      ['NOT ICONTAINS', '_nlike'],
      ['LIKE', '_like'],
      ['ILIKE', '_ilike'],
      ['NOT LIKE', '_nlike'],
      ['NOT ILIKE', '_nlike'],
      ['REGEX', '_regex'],
      ['NOT REGEX', '_nregex'],
      ['REGEXP', '_regex'],
      ['IN', '_in'],
      ['NOT IN', '_not_in'],
      ['EXISTS', '_has_key'],
      ['NOT EXISTS', '_is_null'],
      ['BETWEEN', '_between'],
    ])('maps legacy value %s to backend token %s', (legacy, token) => {
      expect(normalizeLegacyOperator(legacy)).toBe(token);
    });

    it('passes backend tokens through unchanged (idempotent)', () => {
      expect(normalizeLegacyOperator('_eq')).toBe('_eq');
      expect(normalizeLegacyOperator('_contains')).toBe('_contains');
    });

    it('passes unknown values through unchanged', () => {
      expect(normalizeLegacyOperator('HOPEFULLY_UNDEFINED')).toBe('HOPEFULLY_UNDEFINED');
    });
  });

  describe('getOperatorDisplayLabel', () => {
    it('resolves a backend token to its chip_label via descriptor lookup', () => {
      expect(getOperatorDisplayLabel('_contains', SAMPLE_DESCRIPTORS)).toBe('contains');
    });

    it('normalizes a legacy value and resolves the chip_label', () => {
      expect(getOperatorDisplayLabel('CONTAINS', SAMPLE_DESCRIPTORS)).toBe('contains');
    });

    it('falls back to the raw op when no descriptor matches', () => {
      expect(getOperatorDisplayLabel('NEW_OPERATOR', SAMPLE_DESCRIPTORS)).toBe('NEW_OPERATOR');
    });

    it('returns an empty string for falsy input', () => {
      expect(getOperatorDisplayLabel('', SAMPLE_DESCRIPTORS)).toBe('');
    });

    it('returns the raw op when descriptors is undefined', () => {
      expect(getOperatorDisplayLabel('_eq', undefined)).toBe('_eq');
    });

    it('returns the raw op when descriptors is an empty array', () => {
      expect(getOperatorDisplayLabel('_eq', [])).toBe('_eq');
    });
  });
});
