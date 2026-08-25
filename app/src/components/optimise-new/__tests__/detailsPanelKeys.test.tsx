import '@testing-library/jest-dom';
import fs from 'fs';
import path from 'path';

// Guards a React invariant that review keeps missing: the two keyed sections that
// sit side by side in DetailsPanel's root must not share a `key`. Siblings with
// the same key make React strand the old subtree on a key change instead of
// replacing it, so the section visibly accumulates one copy per recommendation
// the user opens. That regressed once, when OwnershipSection was added next to
// BlastRadiusSection and both were keyed `rec?.id`.
//
// Asserted against the source, not a render: the duplicate is invisible until the
// persistent drawer swaps recommendations, which a mounted-in-isolation test can't
// reproduce.
describe('DetailsPanel sibling keys', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'DetailsPanel.tsx'), 'utf8');

  // Tolerates other props before `key` and line wrapping, so reordering or
  // reformatting the JSX doesn't fail the test for a reason that isn't the bug.
  const keyOf = (component: string) => source.match(new RegExp(`<${component}(?:\\s+[^>]*?)?\\s+key=\\{([^}]*\\}?[^}]*)\\}`))?.[1];

  it('keys OwnershipSection and BlastRadiusSection distinctly', () => {
    const ownership = keyOf('OwnershipSection');
    const blastRadius = keyOf('BlastRadiusSection');

    expect(ownership).toBeDefined();
    expect(blastRadius).toBeDefined();
    expect(ownership).not.toBe(blastRadius);
  });

  it('still keys both on the recommendation id, so their state resets per recommendation', () => {
    expect(keyOf('OwnershipSection')).toContain('rec?.id');
    expect(keyOf('BlastRadiusSection')).toContain('rec?.id');
  });
});
