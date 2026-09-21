import TextEnricherDynamicCard from '../TextEnricherDynamicCard';
import ShowingObjectCard from '../ShowingObjectCard';
import ShowingTableCard from '../ShowingTableCard';
import { getTableData, getTableData3 } from '../util';

// The envelopes below are what api-server's BuildAuthoredEvidence emits for each
// evidence type an automation can attach (api-server/services/event/authored_evidence.go).
// They are written out by hand on purpose: this is the seam where a Go change
// and a card's expectations drift apart, which is how authored evidence used to
// save fine and then draw nothing.
const markdownEvidence = {
  type: 'markdown',
  title: 'Rollout blocked',
  data: '## Why\n\nThe new pods never became ready.',
  filename: 'rollout-blocked.md',
  additional_info: {
    actual_action_name: 'workflow_evidence',
    title: 'Rollout blocked',
    severity: 'Critical',
    source_workflow: { workflow_id: 'wf-1', workflow_name: 'Triage', execution_id: 'run-1' },
  },
};

const jsonEvidence = {
  type: 'json',
  title: 'Deployment',
  data: '{"image":"api:1.4.2","replicas":3}',
  filename: 'deployment.json',
  additional_info: { actual_action_name: 'workflow_evidence', title: 'Deployment', severity: 'Info' },
};

const tableEvidence = {
  type: 'table',
  title: 'Restarting pods',
  data: {
    headers: ['Pod', 'Reason'],
    rows: [
      ['api-7f9', 'CrashLoopBackOff'],
      ['web-2b1', 'OOMKilled'],
    ],
    table_name: 'Restarting pods',
    column_renderers: {},
  },
  additional_info: { actual_action_name: 'workflow_evidence', title: 'Restarting pods', severity: 'High' },
};

describe('cards for automation-written evidence', () => {
  it('draws markdown through the text card, keeping the link back to the run', async () => {
    const card = new TextEnricherDynamicCard(markdownEvidence, 0);
    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.text).toBe('Rollout blocked');
    expect(card.sourceWorkflow).toEqual(markdownEvidence.additional_info.source_workflow);
    expect(card.authoredByAutomation).toBe(true);
  });

  it('marks a card written by Run Task, which has no run to link to', async () => {
    const { source_workflow: _stamped, ...noRun } = markdownEvidence.additional_info;
    const card = new TextEnricherDynamicCard({ ...markdownEvidence, additional_info: noRun }, 0);
    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.sourceWorkflow).toBeUndefined();
    expect(card.authoredByAutomation).toBe(true);
  });

  it('draws JSON through the object card as key/value rows', async () => {
    const card = new ShowingObjectCard(jsonEvidence, {}, 0);
    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.authoredByAutomation).toBe(true);
    expect(card.text).toBe('Deployment');
    expect(card.tableData.headers).toEqual(['Key', 'Value']);
    expect(card.tableData.tableData).toHaveLength(2);
  });

  it('draws a table through the table card', async () => {
    const card = new ShowingTableCard(tableEvidence, 0);
    await expect(card.canRenderContent()).resolves.toBe(true);
    expect(card.authoredByAutomation).toBe(true);
    expect(card.text).toBe('Restarting Pods');
    expect(card.tableData.headers).toEqual(['Pod', 'Reason']);
    expect(card.tableData.tableData).toHaveLength(2);
  });
});

describe('values a card cannot render directly', () => {
  // A nested value reaches the page as a React child and throws, which takes
  // down the whole event page rather than just the card.
  it('shows a nested table cell as JSON text', () => {
    const { convertedJson2 } = getTableData({
      data: { headers: ['Pod', 'Detail'], rows: [['api-7f9', { restarts: 5 }]], table_name: 'Restarting pods' },
    });
    expect(convertedJson2[0][1].component.props.value).toBe('{"restarts":5}');
  });

  it('shows a nested JSON value as JSON text', () => {
    const { convertedJson2 } = getTableData3({ limits: { cpu: '500m' } });
    expect(convertedJson2[0][1].component.props.children).toBe('{"cpu":"500m"}');
  });

  it('shows a boolean as text, which React would otherwise render as nothing', () => {
    const json = getTableData3({ enabled: false });
    expect(json.convertedJson2[0][1].component.props.children).toBe('false');

    const table = getTableData({
      data: { headers: ['Unit', 'Enabled'], rows: [['postgresql', false]], table_name: 'Units' },
    });
    expect(table.convertedJson2[0][1].component.props.value).toBe('false');
  });
});
