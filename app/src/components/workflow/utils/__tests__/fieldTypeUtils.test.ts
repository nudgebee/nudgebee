import { filterTicketOptionsBySubType, getDropdownOptionsForField } from '../fieldTypeUtils';

const TICKET_CONFIGS = [
  { label: 'Jira Prod', value: 'jira-1', tool: 'jira' },
  { label: 'GitHub OSS', value: 'gh-1', tool: 'github' },
  { label: 'GitLab', value: 'gl-1', tool: 'gitlab' },
  { label: 'PagerDuty Oncall', value: 'pd-1', tool: 'pagerduty' },
  { label: 'ZenDuty', value: 'zd-1', tool: 'Zenduty' },
];

const dropdownData = {
  cloudAccounts: [],
  integrations: [],
  notifications: [],
  ticketConfigurations: TICKET_CONFIGS,
  namespaces: [],
  resourceTypes: [],
  workloadKinds: [],
  dbmsOptions: [],
};

describe('filterTicketOptionsBySubType', () => {
  it('returns every option when the field declares no restriction', () => {
    expect(filterTicketOptionsBySubType(TICKET_CONFIGS, {})).toHaveLength(5);
  });

  it('narrows to the declared set, case-insensitively on the integration tool', () => {
    const opts = filterTicketOptionsBySubType(TICKET_CONFIGS, { sub_types: ['pagerduty', 'zenduty'] });
    expect(opts.map((o) => o.value)).toEqual(['pd-1', 'zd-1']);
  });

  it('still honours the single-valued sub_type form', () => {
    const opts = filterTicketOptionsBySubType(TICKET_CONFIGS, { sub_type: 'github' });
    expect(opts.map((o) => o.value)).toEqual(['gh-1']);
  });
});

describe('getDropdownOptionsForField', () => {
  // Acknowledge / Escalate / Resolve only accept incident-management
  // integrations; the dropdown used to offer GitHub, GitLab and Jira, which
  // then failed at execution time (nudgebee-enterprise#34946).
  it('offers only incident platforms for a ticket field declaring sub_types', () => {
    const options = getDropdownOptionsForField('integration_id', { type: 'ticket', sub_types: ['pagerduty', 'zenduty'] }, dropdownData);
    expect(options.map((o) => o.value)).toEqual(['pd-1', 'zd-1']);
  });

  it('offers every ticket integration when the field declares no sub_types', () => {
    const options = getDropdownOptionsForField('integration_id', { type: 'ticket' }, dropdownData);
    expect(options).toHaveLength(5);
  });
});
