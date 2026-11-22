import { formatShape, summariseClassifier } from '@components/llm/kbDocumentClassification';

describe('summariseClassifier', () => {
  it('reads a document the classifier labelled', () => {
    expect(summariseClassifier({ kind: 'sop', shape: 'deterministic_procedure', capabilities: ['kubectl_execute', 'logs'] })).toEqual({
      classified: true,
      kind: 'sop',
      kindLabel: 'SOP',
      shapeLabel: 'Deterministic Procedure',
      capabilities: ['kubectl_execute', 'logs'],
    });
  });

  it('labels a knowledge document without a shape or capabilities', () => {
    expect(summariseClassifier({ kind: 'knowledge' })).toEqual({
      classified: true,
      kind: 'knowledge',
      kindLabel: 'Knowledge',
      shapeLabel: '',
      capabilities: [],
    });
  });

  it('reads a document with no labels as unclassified, not as knowledge', () => {
    expect(summariseClassifier({})).toEqual({ classified: false });
    expect(summariseClassifier({ kind: null, shape: null, capabilities: [] })).toEqual({ classified: false });
  });

  it('does not accept a kind the classifier never writes', () => {
    expect(summariseClassifier({ kind: 'embedded', shape: 'decision_tree', capabilities: ['x'] })).toEqual({ classified: false });
  });

  it('drops non-string capabilities rather than rendering them', () => {
    const summary = summariseClassifier({ kind: 'sop', capabilities: ['logs', '', 7 as unknown as string] });
    expect(summary).toMatchObject({ capabilities: ['logs'] });
  });
});

describe('formatShape', () => {
  it('title-cases a snake_case shape and tolerates a non-string', () => {
    expect(formatShape('narrative_reference')).toBe('Narrative Reference');
    expect(formatShape(undefined)).toBe('');
  });
});
