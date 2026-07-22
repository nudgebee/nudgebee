// Regression tests for the egressfilter chip predicate. The Go backend
// renamed the mode string "audit" -> "detect"; the chip must render for the
// canonical "detect" string (and still for the legacy "audit" alias),
// otherwise metadata is persisted but no chip appears in the UI.
import { __egressfilterItemForTest as egressfilterItem } from '@components/llm/common/ResponseMetaRail';

describe('egressfilterItem', () => {
  it('returns null for empty/nullish input', () => {
    expect(egressfilterItem(undefined)).toBeNull();
    expect(egressfilterItem(null)).toBeNull();
    expect(egressfilterItem([])).toBeNull();
  });

  it('renders a chip for the canonical "detect" mode', () => {
    const item = egressfilterItem([{ mode: 'detect', hit_count: 1, rule_ids: ['aws-access-key-id'], audit_id: 'egress-abc' }]);
    expect(item).not.toBeNull();
    expect(item.key).toBe('egressfilter');
  });

  it('renders a chip for the legacy "audit" alias', () => {
    const item = egressfilterItem([{ mode: 'audit', hit_count: 1, rule_ids: ['openai-api-key'], audit_id: 'egress-abc' }]);
    expect(item).not.toBeNull();
  });

  it('renders a chip for "enforce" mode', () => {
    const item = egressfilterItem([{ mode: 'enforce', hit_count: 1, rule_ids: ['aws-access-key-id'], audit_id: 'egress-xyz' }]);
    expect(item).not.toBeNull();
  });

  it('returns null for an unrecognised mode', () => {
    const item = egressfilterItem([{ mode: 'garbage', hit_count: 1, rule_ids: ['x'], audit_id: 'egress-x' }]);
    expect(item).toBeNull();
  });
});
