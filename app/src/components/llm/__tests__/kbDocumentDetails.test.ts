import { capabilityRows, countClassified, markVsClassifier, scopeLabel, scopeTone, surfaceLabel } from '@components/llm/kbDocumentDetails';

describe('scope and surface wording', () => {
  it('maps the three catalog scopes to a tone and a label, and anything else to unclassified', () => {
    expect([scopeTone('narrow'), scopeLabel('narrow')]).toEqual(['success', 'Narrow']);
    expect([scopeTone('pass_through'), scopeLabel('pass_through')]).toEqual(['warning', 'Pass-through']);
    expect([scopeTone('unclassified'), scopeLabel('unclassified')]).toEqual(['neutral', 'Unclassified']);
    expect([scopeTone(undefined), scopeLabel('whatever')]).toEqual(['neutral', 'Unclassified']);
  });

  it('names the two surfaces and dashes an unknown one', () => {
    expect(surfaceLabel('agent_tool')).toBe('Agent tool');
    expect(surfaceLabel('workflow_task')).toBe('Workflow task');
    expect(surfaceLabel(undefined)).toBe('—');
  });
});

describe('capabilityRows', () => {
  const details = [
    { name: 'kubectl_execute', surface: 'agent_tool', scope: 'pass_through', description: 'Runs kubectl' },
    { name: 'promql_query', surface: 'agent_tool', scope: 'narrow', description: 'Writes PromQL' },
  ];

  it('looks each referenced capability up in the catalog, in the order the page lists them', () => {
    const rows = capabilityRows(['promql_query', 'kubectl_execute'], details);
    expect(rows.map((r) => r.name)).toEqual(['promql_query', 'kubectl_execute']);
    expect(rows[0]).toMatchObject({ surface: 'Agent tool', scope: 'Narrow', tone: 'success', description: 'Writes PromQL', inCatalog: true });
    expect(rows[1]).toMatchObject({ scope: 'Pass-through', tone: 'warning', inCatalog: true });
  });

  it('keeps a capability the catalog does not know, marked as such, instead of dropping it', () => {
    const [row] = capabilityRows(['mystery'], details);
    expect(row).toMatchObject({ name: 'mystery', surface: '—', scope: 'Unclassified', tone: 'neutral', description: '', inCatalog: false });
  });

  it('copes with no catalog details at all', () => {
    expect(capabilityRows(['a'], undefined)).toHaveLength(1);
    expect(capabilityRows(undefined, details)).toEqual([]);
  });
});

describe('markVsClassifier', () => {
  it("says the user's mark wins when it differs from the classifier", () => {
    expect(markVsClassifier('fact', 'sop')).toBe('The classifier says SOP; your mark (Fact) wins.');
    expect(markVsClassifier('sop', 'knowledge')).toBe('The classifier says Knowledge; your mark (SOP) wins.');
  });

  it('says nothing when there is no mark, no classifier label, or they agree', () => {
    expect(markVsClassifier('', 'sop')).toBeNull();
    expect(markVsClassifier('fact', null)).toBeNull();
    expect(markVsClassifier('sop', 'sop')).toBeNull();
    expect(markVsClassifier('fact', 'knowledge')).toBeNull();
  });
});

describe('countClassified', () => {
  it('counts the loaded documents by what the classifier decided', () => {
    expect(countClassified([{ kind: 'sop' }, { kind: 'sop' }, { kind: 'knowledge' }, {}, { kind: 'embedded' }])).toEqual({
      sop: 2,
      knowledge: 1,
      notClassified: 2,
    });
  });
});
