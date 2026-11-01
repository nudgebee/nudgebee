import { buildRelationshipsCsv } from '../kgExport';
import { imagePixelRatio } from '@components/k8s/details/ServiceMapDownloadImage';

jest.mock('html-to-image', () => ({ toPng: jest.fn(), getFontEmbedCSS: jest.fn() }));

const node = (id, name, extra = {}) => ({
  id,
  data: { name, type: 'Workload', accountName: 'prod-cluster', namespace: 'default', location: '', ...extra.data },
  ...extra.node,
});

const edge = (id, source, target, relationshipLabel, extra = {}) => ({ id, source, target, data: { relationshipLabel }, ...extra });

const parse = (csv) => csv.trim().split('\r\n');

describe('buildRelationshipsCsv', () => {
  it('writes a header and one row per edge', () => {
    const lines = parse(buildRelationshipsCsv([node('a', 'checkout'), node('b', 'payments')], [edge('e1', 'a', 'b', 'Calls')]));
    expect(lines[0]).toBe(
      '"Source Name","Source Kind","Source Account","Source Namespace","Source Location","Relationship","Target Name","Target Kind","Target Account","Target Namespace","Target Location","Source ID","Target ID"'
    );
    expect(lines[1]).toBe('"checkout","Workload","prod-cluster","default","","Calls","payments","Workload","prod-cluster","default","","a","b"');
    expect(lines).toHaveLength(2);
  });

  it('lists nodes with no edge as their own row', () => {
    const lines = parse(buildRelationshipsCsv([node('a', 'checkout'), node('b', 'payments'), node('c', 'orphan')], [edge('e1', 'a', 'b', 'Calls')]));
    expect(lines).toHaveLength(3);
    expect(lines[2]).toBe('"orphan","Workload","prod-cluster","default","","","","","","","","c",""');
  });

  it('skips nodes and edges hidden by focus mode', () => {
    const nodes = [node('a', 'checkout'), node('b', 'payments'), node('c', 'hidden-node', { node: { hidden: true } })];
    const edges = [edge('e1', 'a', 'b', 'Calls'), edge('e2', 'a', 'c', 'Calls'), edge('e3', 'b', 'a', 'Depends On', { hidden: true })];
    const lines = parse(buildRelationshipsCsv(nodes, edges));
    expect(lines).toHaveLength(2);
    expect(lines.join('\n')).not.toContain('hidden-node');
    expect(lines.join('\n')).not.toContain('Depends On');
  });

  it('escapes quotes, keeps commas and newlines inside the cell, and neutralises formulas', () => {
    const csv = buildRelationshipsCsv([node('a', 'say "hi", there'), node('b', '=HYPERLINK("x")'), node('c', 'multi\nline')], []);
    expect(csv).toContain('"say ""hi"", there"');
    expect(csv).toContain('"\'=HYPERLINK(""x"")"');
    expect(csv).toContain('"multi\nline"');
  });

  it('falls back to the node id when the name is missing', () => {
    expect(parse(buildRelationshipsCsv([node('node-123', '')], []))[1]).toMatch(/^"node-123"/);
  });

  it('returns only the header for an empty graph', () => {
    expect(parse(buildRelationshipsCsv([], []))).toHaveLength(1);
  });
});

describe('imagePixelRatio', () => {
  it('uses 2x for a normal-sized graph', () => {
    expect(imagePixelRatio(1600, 900)).toBe(2);
  });

  it('shrinks so the canvas stays under the browser area and side limits', () => {
    const wide = imagePixelRatio(40000, 1200);
    expect(40000 * wide).toBeLessThanOrEqual(16000);
    const big = imagePixelRatio(8000, 6000);
    expect(8000 * big * 6000 * big).toBeLessThanOrEqual(16_000_000);
  });
});
