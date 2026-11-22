// What the classifier decided about one listed document, in the form the row shows.
// Only the classifier's own output: how an agent treats the page also depends on a
// user's Fact/SOP mark and on the knowledge base, which the row shows separately.

export interface ClassifiedDocument {
  kind?: string | null;
  shape?: string | null;
  capabilities?: string[] | null;
}

export type ClassifierSummary =
  | { classified: false }
  | { classified: true; kind: 'sop' | 'knowledge'; kindLabel: string; shapeLabel: string; capabilities: string[] };

export const formatShape = (shape: unknown): string => {
  if (typeof shape !== 'string') return '';
  return shape
    .split('_')
    .filter(Boolean)
    .map((word) => word[0].toUpperCase() + word.slice(1))
    .join(' ');
};

// A kind other than the two the classifier writes is not a label, so it reads as
// unclassified rather than being shown as if the classifier had said it.
export const summariseClassifier = (doc: ClassifiedDocument): ClassifierSummary => {
  const kind = doc.kind;
  if (kind !== 'sop' && kind !== 'knowledge') return { classified: false };
  return {
    classified: true,
    kind,
    kindLabel: kind === 'sop' ? 'SOP' : 'Knowledge',
    shapeLabel: formatShape(doc.shape),
    capabilities: Array.isArray(doc.capabilities) ? doc.capabilities.filter((c) => typeof c === 'string' && c !== '') : [],
  };
};
