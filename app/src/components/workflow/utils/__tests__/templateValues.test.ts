import { applyTemplateValues, buildTemplateFields, initialTemplateValues, missingRequired } from '../templateValues';

const rightSizing = {
  id: 'tpl-1',
  name: 'K8s Right-Sizing with Approval',
  definition: {
    version: 'v1',
    inputs: [
      { id: 'namespace', type: 'string', required: true },
      { id: 'workload_kind', type: 'string', default: 'Deployment' },
      { id: 'approval_channel', type: 'string', required: true },
      { id: 'account_id', type: 'string' },
    ],
    triggers: [{ type: 'manual' }, { type: 'webhook', params: { filter: '{{ true }}' } }],
    tasks: [],
  },
  template_variables: [
    { id: 'ns', input_ref: 'namespace', display_name: 'Namespace', required: true, group: 'Workload' },
    { id: 'kind', input_ref: 'workload_kind', display_name: 'Workload Kind', options: ['Deployment', 'StatefulSet'] },
    { id: 'slack_channel', input_ref: 'approval_channel', display_name: 'Approval Channel', help_text: 'Slack channel', required: true },
    { id: 'k8s_account', input_ref: 'account_id', display_name: 'Kubernetes Account', type: 'account', required: false },
    { id: 'dangling', input_ref: 'not_an_input', display_name: 'Ignored' },
  ],
};

describe('buildTemplateFields', () => {
  it('maps each variable to its input and adds one field per webhook trigger', () => {
    const fields = buildTemplateFields(rightSizing);
    expect(fields.map((f) => [f.key, f.kind, f.required])).toEqual([
      ['namespace', 'text', true],
      ['workload_kind', 'select', false],
      ['approval_channel', 'text', true],
      ['account_id', 'account', false],
      ['trigger:1', 'webhook', true],
    ]);
    expect(fields[2]).toMatchObject({ label: 'Approval Channel', helpText: 'Slack channel' });
  });

  it('returns nothing for a template with no variables and no webhook', () => {
    expect(buildTemplateFields({ definition: { inputs: [], triggers: [{ type: 'manual' }], tasks: [] } })).toEqual([]);
  });
});

describe('initialTemplateValues', () => {
  it('prefers event context, then the input default, then the page account', () => {
    const fields = buildTemplateFields(rightSizing);
    const values = initialTemplateValues(rightSizing, fields, { accountId: 'acct-1', eventContext: { namespace: 'shop' } });
    expect(values).toEqual({ namespace: 'shop', workload_kind: 'Deployment', approval_channel: '', account_id: 'acct-1', 'trigger:1': '' });
  });
});

describe('missingRequired', () => {
  it('lists required fields that are blank after trimming', () => {
    const fields = buildTemplateFields(rightSizing);
    expect(missingRequired(fields, { namespace: ' ', approval_channel: 'C123', 'trigger:1': '' })).toEqual(['namespace', 'trigger:1']);
  });
});

describe('applyTemplateValues', () => {
  it('writes values as input defaults and the webhook integration onto its trigger', () => {
    const out = applyTemplateValues(rightSizing, { namespace: ' shop ', approval_channel: 'C123', account_id: '', 'trigger:1': 'gh-hook' });
    expect(out.definition.inputs).toEqual([
      { id: 'namespace', type: 'string', required: true, default: 'shop' },
      { id: 'workload_kind', type: 'string', default: 'Deployment' },
      { id: 'approval_channel', type: 'string', required: true, default: 'C123' },
      { id: 'account_id', type: 'string' },
    ]);
    expect(out.definition.triggers[1]).toEqual({ type: 'webhook', params: { filter: '{{ true }}', integration_name: 'gh-hook' } });
    expect(rightSizing.definition.inputs[0]).not.toHaveProperty('default');
  });
});

describe('templates with malformed inputs', () => {
  const broken = {
    definition: { inputs: [null, { type: 'string' }, { id: 'namespace' }], triggers: [] },
    template_variables: [{ id: 'ns', input_ref: 'namespace', display_name: 'Namespace' }],
  };

  it('skips null and id-less inputs without crashing or writing to them', () => {
    expect(buildTemplateFields(broken).map((f) => f.key)).toEqual(['namespace']);
    expect(initialTemplateValues(broken, buildTemplateFields(broken), {})).toEqual({ namespace: '' });
    expect(applyTemplateValues(broken, { namespace: 'prod' }).definition.inputs).toEqual([
      null,
      { type: 'string' },
      { id: 'namespace', default: 'prod' },
    ]);
  });
});
