import { saveAs } from 'file-saver';
import { downloadFlowImage } from '@components/k8s/details/ServiceMapDownloadImage';

const CSV_HEADER = [
  'Source Name',
  'Source Kind',
  'Source Account',
  'Source Namespace',
  'Source Location',
  'Relationship',
  'Target Name',
  'Target Kind',
  'Target Account',
  'Target Namespace',
  'Target Location',
  // Last, so the readable columns come first: same-named nodes (e.g. two "default"
  // subnets in one region) are otherwise indistinguishable rows.
  'Source ID',
  'Target ID',
];

// Quote every cell (RFC 4180) and neutralise spreadsheet formulas: manual node
// names are user-typed, so a leading = + - @ must not execute when opened in Excel.
const csvCell = (value) => {
  let str = value == null ? '' : String(value);
  if (/^[=+\-@\t\r]/.test(str)) str = `'${str}`;
  return `"${str.replace(/"/g, '""')}"`;
};

const nodeColumns = (node) => {
  const d = node?.data || {};
  return [d.name || node?.id || '', d.type, d.accountName, d.namespace, d.location];
};

/**
 * One row per visible edge (source → relationship → target). Visible nodes with
 * no visible edge get a row with empty relationship/target columns so the CSV
 * still lists every node on screen. Nodes/edges hidden by focus mode are skipped.
 */
export function buildRelationshipsCsv(nodes, edges) {
  const visibleNodes = (nodes || []).filter((n) => !n.hidden);
  const nodeById = new Map(visibleNodes.map((n) => [n.id, n]));
  const connected = new Set();
  const rows = [];

  (edges || []).forEach((e) => {
    if (e.hidden) return;
    const source = nodeById.get(e.source);
    const target = nodeById.get(e.target);
    if (!source || !target) return;
    connected.add(e.source);
    connected.add(e.target);
    rows.push([...nodeColumns(source), e.data?.relationshipLabel || e.data?.relationship_type || '', ...nodeColumns(target), source.id, target.id]);
  });

  visibleNodes.forEach((n) => {
    if (!connected.has(n.id)) rows.push([...nodeColumns(n), '', '', '', '', '', '', n.id, '']);
  });

  return [CSV_HEADER, ...rows].map((row) => row.map(csvCell).join(',')).join('\r\n') + '\r\n';
}

const exportFileName = (ext) => `knowledge-graph-${new Date().toISOString().slice(0, 10)}.${ext}`;

export function downloadRelationshipsCsv(nodes, edges) {
  const blob = new Blob([buildRelationshipsCsv(nodes, edges)], { type: 'text/csv;charset=utf-8' });
  saveAs(blob, exportFileName('csv'));
}

// html-to-image inlines every computed style on every cloned element, so the SVG
// it serialises grows with the node count. Measured on dev (Chrome): 380 nodes
// exports in ~18s; 904 nodes overflows V8's max string length ("Invalid string
// length"). Its includeStyleProperties option would shrink it, but the library
// caches the first call's property list module-wide, leaking into the Service Map
// and dashboard exports — so cap the image instead; CSV has no limit.
export const IMAGE_MAX_NODES = 500;

/**
 * Render the visible graph to a PNG. `viewportEl` is this graph's
 * `.react-flow__viewport`; the caller must have every node rendered into it
 * (i.e. `onlyRenderVisibleElements` off), or off-screen nodes are missing.
 */
export function downloadGraphImage(viewportEl, nodes) {
  return downloadFlowImage(
    viewportEl,
    (nodes || []).filter((n) => !n.hidden),
    exportFileName('png')
  );
}
