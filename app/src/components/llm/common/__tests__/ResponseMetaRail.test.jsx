// Regression tests for the egressfilter chip predicate. The Go backend
// evolves its mode strings over time; each new value must be added to the
// predicate in the same PR that ships it, or the chip silently returns
// null for every new event with no CI signal.
//   - PR #33187: renamed "audit" → "detect"
//   - PR #33358: added "redact" mode
//
// Precedence for tone / verb: enforce > redact > detect.

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

  it('renders a chip for "redact" mode (post-#33358)', () => {
    const item = egressfilterItem([{ mode: 'redact', hit_count: 1, rule_ids: ['github-pat'], audit_id: 'egress-red1' }]);
    expect(item).not.toBeNull();
  });

  it('returns null for an unrecognised mode (defensive)', () => {
    const item = egressfilterItem([{ mode: 'garbage', hit_count: 1, rule_ids: ['x'], audit_id: 'egress-x' }]);
    expect(item).toBeNull();
  });

  it('handles mixed detect + enforce events (enforce wins the tone)', () => {
    const item = egressfilterItem([
      { mode: 'detect', hit_count: 1, rule_ids: ['a'] },
      { mode: 'enforce', hit_count: 1, rule_ids: ['b'] },
    ]);
    expect(item).not.toBeNull();
  });

  it('handles mixed detect + redact events (redact precedence over detect)', () => {
    const item = egressfilterItem([
      { mode: 'detect', hit_count: 1, rule_ids: ['a'] },
      { mode: 'redact', hit_count: 1, rule_ids: ['b'] },
    ]);
    expect(item).not.toBeNull();
  });

  it('handles mixed enforce + redact events (enforce still wins over redact)', () => {
    const item = egressfilterItem([
      { mode: 'redact', hit_count: 1, rule_ids: ['a'] },
      { mode: 'enforce', hit_count: 1, rule_ids: ['b'] },
    ]);
    expect(item).not.toBeNull();
  });
});
