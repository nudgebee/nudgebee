import fs from 'fs';
import path from 'path';

/**
 * Guards the GraphQL field selections that feed the per-label operator menu.
 *
 * `operatorAppliesToType` fails OPEN when a descriptor carries no
 * `applicable_data_types` — that fallback exists so an older backend degrades to the
 * previous behaviour instead of showing an empty dropdown. The cost of that leniency is
 * that FORGETTING to select the field looks identical to talking to an old backend: the
 * type narrowing silently stops working, every operator is offered for every label, and
 * the failure only surfaces later as a backend rejection of a query the UI itself offered.
 *
 * That is exactly how this shipped broken once. Unit tests on `getOperatorsForLabel` could
 * not catch it because they build descriptors inline; only the wire path was wrong. So
 * assert on the query text itself.
 */
describe('supported_operator_descriptors GraphQL selections', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'index.ts'), 'utf8');

  // Every `supported_operator_descriptors { ... }` selection block in the file.
  const selectionBlocks = [...source.matchAll(/supported_operator_descriptors\s*\{([^}]*)\}/g)].map((m) => m[1]);

  it('finds every descriptor selection in the file', () => {
    // Sanity check on the regex itself: if the queries are restructured such that this
    // matches nothing, the assertions below would vacuously pass.
    expect(selectionBlocks.length).toBeGreaterThan(0);
  });

  it.each(selectionBlocks.map((block, i) => [i, block]))('selection %i requests applicable_data_types', (_i, block) => {
    expect(block).toContain('applicable_data_types');
  });

  it('requests applicable_data_types everywhere kinds is requested', () => {
    // kinds and applicable_data_types are the two fields the operator menu filters on:
    // kinds picks chip-vs-line, applicable_data_types narrows by the label's type. A
    // selection carrying one but not the other yields an unnarrowed menu.
    const withKinds = selectionBlocks.filter((b) => b.includes('kinds'));
    const withTypes = selectionBlocks.filter((b) => b.includes('applicable_data_types'));
    expect(withTypes.length).toBe(withKinds.length);
  });
});
