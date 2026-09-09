import AskAiCard, { expandParagraphBreaks } from '@components/k8s/investigate/cards/AskAiCard';

// buildResolveData resolves the event's workload identity from (in order):
// service_key, the noisy_neighbours evidence, and finally the
// pod-name-minus-hash heuristic. The noisy_neighbours evidence now comes from
// the api-server Go playbook (action_noisy_neighbours.go), whose neighbour
// entries carry no `kind` — only the legacy Robusta enricher included it.
describe('AskAiCard.buildResolveData', () => {
  const makeCard = (event) => {
    const card = new AskAiCard();
    card.event = event;
    return card;
  };

  const noisyNeighboursEvidence = (neighbours) => ({
    type: 'json',
    data: JSON.stringify({ name: 'noisy_neighbours', data: { neighbours } }),
  });

  it('falls back to the Deployment heuristic when a matching neighbour has no kind (Go playbook shape)', () => {
    const card = makeCard({
      id: 'evt-1',
      subject_type: 'pod',
      subject_name: 'llm-server-7f66d5f675-cbc4x',
      subject_namespace: 'nudgebee',
      service_key: '',
      evidences: [noisyNeighboursEvidence([{ pod_name: 'llm-server-7f66d5f675-cbc4x', namespace: 'nudgebee', memory_used: 123 }])],
    });

    const data = card.buildResolveData();

    expect(data.cloud_resourse.meta.controller).toBe('llm-server');
    expect(data.cloud_resourse.meta.controllerKind).toBe('Deployment');
  });

  it('does not throw when a pod event has no service_key at all', () => {
    const card = makeCard({
      id: 'evt-2',
      subject_type: 'pod',
      subject_name: 'api-server-abc12-x9y8z',
      subject_namespace: 'default',
      evidences: [],
    });

    expect(() => card.buildResolveData()).not.toThrow();
    expect(card.buildResolveData().cloud_resourse.meta.controller).toBe('api-server');
  });

  it('still resolves workload from legacy neighbours that carry kind', () => {
    const card = makeCard({
      id: 'evt-3',
      subject_type: 'pod',
      subject_name: 'worker-abc',
      subject_namespace: 'jobs',
      service_key: '',
      evidences: [noisyNeighboursEvidence([{ pod_name: 'worker-abc', namespace: 'jobs', kind: [{ name: 'worker', kind: 'StatefulSet' }] }])],
    });

    const data = card.buildResolveData();

    expect(data.cloud_resourse.meta.controller).toBe('worker');
    expect(data.cloud_resourse.meta.controllerKind).toBe('StatefulSet');
  });
});

// The AI summary arrives with single newlines between paragraphs, so a blank line
// is inserted to stop markdown collapsing them into one paragraph. That rewrite
// used to hit table rows too, which ends the table and leaves every following row
// rendering as literal '| Attribute | Value |' text in the Investigation Analysis
// card.
describe('AskAiCard expandParagraphBreaks', () => {
  it('keeps a markdown table intact', () => {
    const markdown = [
      '### Event Details',
      '',
      '| Attribute | Value |',
      '| :--- | :--- |',
      '| **Event ID** | `ad16241e` |',
      '| **Finding Type** | `issue` |',
    ].join('\n');

    expect(expandParagraphBreaks(markdown)).toContain('| :--- | :--- |\n| **Event ID** | `ad16241e` |\n| **Finding Type** | `issue` |');
  });

  it('still separates consecutive prose lines into paragraphs', () => {
    expect(expandParagraphBreaks('First line.\nSecond line.')).toBe('First line.\n\nSecond line.');
  });

  it('leaves fenced code blocks untouched', () => {
    const markdown = 'Before.\n```\nline one\nline two\n```';

    expect(expandParagraphBreaks(markdown)).toContain('```\nline one\nline two\n```');
  });

  it('does not add a break where one already exists', () => {
    expect(expandParagraphBreaks('One.\n\nTwo.')).toBe('One.\n\nTwo.');
  });
});
