// Regression tests for the egressfilter chip predicate. The Go backend
// evolves its mode strings over time; each new value must be added to the
// predicate in the same PR that ships it, or the chip silently returns
// null for every new event with no CI signal.
//   - PR #33187: renamed "audit" → "detect"
//   - PR #33358: added "redact" mode
//   - PR #31514: metadata.egressfilter[] is now polymorphic (secrets + PII
//                sibling detectors, discriminated by `detector` field). The
//                secrets chip filters to `detector === 'secrets'` (or missing,
//                for legacy pre-detector rows) so PII entries never poison
//                the "N secrets" count or rule_ids tooltip.
//
// Precedence for tone / verb: enforce > redact > detect.

import { __egressfilterItemForTest as egressfilterItem, __piiScrubItemForTest as piiScrubItem } from '@components/llm/common/ResponseMetaRail';

// Reach into the returned React element to pull the Chip's count prop.
// Structure is <Tooltip><Box><Chip count={N}>label</Chip></Box></Tooltip>.
// Keep this local to the test file so the source stays free of test seams.
const chipCount = (item) => item.node.props.children.props.children.props.count;

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

  // --- Polymorphic array guardrail (PR #31514) ---
  //
  // metadata.egressfilter[] holds events from every outbound-inspection
  // detector (secrets + PII scrubber). The secrets chip must only count
  // secrets — otherwise a turn with 3 secret hits + 2 PII hits would
  // render "5 secrets blocked", double-counting PII.

  it('returns null for a PII-only turn (secrets chip is not responsible for PII)', () => {
    const item = egressfilterItem([
      {
        detector: 'pii',
        audit_id: 'scrub-abc',
        hit_count: 3,
        categories: ['EMAIL', 'PERSON'],
        reversible: true,
      },
    ]);
    expect(item).toBeNull();
  });

  it('mixed secrets + PII turn: chip renders secrets-only count (PII does NOT bleed in)', () => {
    const item = egressfilterItem([
      { detector: 'secrets', mode: 'detect', hit_count: 2, rule_ids: ['aws-access-key-id'], audit_id: 'egress-1' },
      // PII entry MUST be excluded from the "N secrets" tally, even
      // though it has a hit_count field.
      { detector: 'pii', hit_count: 3, categories: ['EMAIL'], audit_id: 'scrub-1', reversible: true },
    ]);
    expect(item).not.toBeNull();
    expect(chipCount(item)).toBe(2);
  });

  it('legacy pre-detector rows are treated as secrets (backward compat)', () => {
    // Rows written before the Detector field landed lack the key. Absent
    // must be treated as "secrets" or the chip silently disappears for
    // historical events.
    const item = egressfilterItem([{ mode: 'enforce', hit_count: 4, rule_ids: ['openai-api-key'], audit_id: 'egress-legacy' }]);
    expect(item).not.toBeNull();
    expect(chipCount(item)).toBe(4);
  });

  it('mixed legacy + tagged secrets rows both count', () => {
    const item = egressfilterItem([
      { mode: 'detect', hit_count: 1, rule_ids: ['a'], audit_id: 'egress-legacy' }, // no detector
      { detector: 'secrets', mode: 'detect', hit_count: 2, rule_ids: ['b'], audit_id: 'egress-new' },
    ]);
    expect(item).not.toBeNull();
    expect(chipCount(item)).toBe(3);
  });

  it('unrecognised detector (defensive) is dropped from the secrets chip', () => {
    // Forward-compat: a future detector we do not know about (e.g.
    // "prompt_injection") must not accidentally get counted as secrets.
    const item = egressfilterItem([
      { detector: 'prompt_injection', hit_count: 99 },
      { detector: 'secrets', mode: 'detect', hit_count: 1, rule_ids: ['a'] },
    ]);
    expect(item).not.toBeNull();
    expect(chipCount(item)).toBe(1);
  });
});

// ---------------------------------------------------------------------------
// PII chip (sibling of the secrets chip; reads detector==='pii' entries from
// the same metadata.egressfilter[] array).
// ---------------------------------------------------------------------------

describe('piiScrubItem', () => {
  const piiEvent = (overrides = {}) => ({
    detector: 'pii',
    audit_id: 'scrub-abc123',
    hit_count: 1,
    categories: ['EMAIL'],
    reversible: true,
    ...overrides,
  });

  it('returns null on empty / missing input', () => {
    expect(piiScrubItem(undefined)).toBeNull();
    expect(piiScrubItem(null)).toBeNull();
    expect(piiScrubItem([])).toBeNull();
  });

  it('returns null when the array has no pii entries (secrets-only turn)', () => {
    const item = piiScrubItem([{ detector: 'secrets', mode: 'detect', hit_count: 3, rule_ids: ['a'] }]);
    expect(item).toBeNull();
  });

  it('renders a chip for a single PII event, count from hit_count', () => {
    const item = piiScrubItem([piiEvent({ hit_count: 2, categories: ['EMAIL', 'PERSON'] })]);
    expect(item).not.toBeNull();
    expect(item.key).toBe('pii');
    expect(chipCount(item)).toBe(2);
  });

  it('label singular vs plural on total hits', () => {
    const one = piiScrubItem([piiEvent({ hit_count: 1 })]);
    expect(one.node.props.children.props.children.props.children).toBe('PII scrubbed');
    const many = piiScrubItem([piiEvent({ hit_count: 3 })]);
    expect(many.node.props.children.props.children.props.children).toBe('PII values scrubbed');
  });

  it('sums hit_count across multiple PII events and dedupes categories', () => {
    const item = piiScrubItem([
      piiEvent({ audit_id: 'scrub-a', hit_count: 2, categories: ['EMAIL', 'PERSON'] }),
      piiEvent({ audit_id: 'scrub-b', hit_count: 4, categories: ['PERSON', 'PHONE'] }),
    ]);
    expect(item).not.toBeNull();
    expect(chipCount(item)).toBe(6);
    // Tooltip is on the outer Tooltip element as `title`.
    expect(item.node.props.title).toContain('EMAIL');
    expect(item.node.props.title).toContain('PERSON');
    expect(item.node.props.title).toContain('PHONE');
    // Cross-call scope line only appears when >1 event.
    expect(item.node.props.title).toContain('across 2 calls');
    // Audit ids present.
    expect(item.node.props.title).toContain('scrub-a');
    expect(item.node.props.title).toContain('scrub-b');
  });

  it('IGNORES secrets entries when computing counts (mixed array)', () => {
    // The whole point of the polymorphic-array design: the PII chip must
    // never count secrets hit_count, and vice-versa.
    const item = piiScrubItem([
      { detector: 'secrets', mode: 'enforce', hit_count: 100, rule_ids: ['aws-access-key-id'] },
      piiEvent({ hit_count: 2, categories: ['EMAIL'] }),
    ]);
    expect(item).not.toBeNull();
    expect(chipCount(item)).toBe(2);
  });

  it('missing hit_count on a PII event falls back to min 1', () => {
    // Defensive: a malformed row without hit_count still renders a chip
    // (rather than "0 PII scrubbed") so the audit trail stays visible.
    const item = piiScrubItem([piiEvent({ hit_count: undefined })]);
    expect(chipCount(item)).toBe(1);
  });

  it('missing categories does not crash the tooltip', () => {
    // Defensive: `categories` absent → we skip the "Scrubbed: ..." line
    // and fall back to just the audit-id line (still useful for support).
    const item = piiScrubItem([piiEvent({ categories: undefined })]);
    expect(item).not.toBeNull();
    expect(item.node.props.title).toContain('scrub-abc123');
    expect(item.node.props.title).not.toContain('Scrubbed:');
  });

  it('dedupes duplicate audit_ids in the tooltip (Gemini review)', () => {
    // Two events sharing an audit id (retries, or a badly-behaved caller
    // emitting duplicates) must not render as "scrub-x, scrub-x" — one
    // occurrence in the tooltip is enough.
    const item = piiScrubItem([
      piiEvent({ audit_id: 'scrub-dup', hit_count: 1 }),
      piiEvent({ audit_id: 'scrub-dup', hit_count: 1 }),
      piiEvent({ audit_id: 'scrub-other', hit_count: 1 }),
    ]);
    expect(item).not.toBeNull();
    const title = item.node.props.title;
    // 'scrub-dup' appears exactly once (dedupe worked), 'scrub-other' also once.
    expect(title.match(/scrub-dup/g)?.length).toBe(1);
    expect(title.match(/scrub-other/g)?.length).toBe(1);
  });
});
