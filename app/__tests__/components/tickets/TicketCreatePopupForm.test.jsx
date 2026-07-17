import { buildCreateTicketPayload, extractCreateErrorMessage } from '@components/tickets/TicketCreatePopupForm';

jest.mock('@utils/colors');
jest.mock('@components/tickets/TicketFormSection', () => () => null);
jest.mock('@api1/tickets', () => ({ __esModule: true, default: { createTicket: jest.fn() } }));
jest.mock('@ui/Modal', () => ({ Modal: () => null }));
jest.mock('@ui/Button', () => ({ Button: () => null }));
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn(), success: jest.fn(), warning: jest.fn() } }));

// res is the value TicketCreatePopupForm receives from apiTickets.createTicket:
// { data: <axios response> }, so the GraphQL body sits at res.data.data.
const resWithBody = (body) => ({ data: { data: body } });

describe('extractCreateErrorMessage', () => {
  it('extracts the upstream error from the JSON-encoded gateway error message (issue #34126)', () => {
    const upstreamError =
      'failed to create github ticket: POST https://api.github.com/repos/nudgebee/nudgebee-agent/issues: 403 Repository was archived so is read-only. []';
    const res = resWithBody({
      data: { tickets_create: null },
      errors: [{ message: JSON.stringify({ data: { insert_tickets_one: { id: '', error: upstreamError } } }) }],
    });
    expect(extractCreateErrorMessage(res)).toBe(upstreamError);
  });

  it('returns a plain-string error message as-is', () => {
    const res = resWithBody({
      data: { tickets_create: null },
      errors: [{ message: 'server_gateway_unavailable' }],
    });
    expect(extractCreateErrorMessage(res)).toBe('server_gateway_unavailable');
  });

  it('returns null when the JSON-encoded message has no insert_tickets_one error', () => {
    const res = resWithBody({
      data: { tickets_create: null },
      errors: [{ message: JSON.stringify({ data: { insert_tickets_one: { id: 'abc' } } }) }],
    });
    expect(extractCreateErrorMessage(res)).toBeNull();
  });

  it('returns null when the response has no errors', () => {
    const res = resWithBody({ data: { tickets_create: { data: { insert_tickets_one: { id: 'abc' } } } } });
    expect(extractCreateErrorMessage(res)).toBeNull();
    expect(extractCreateErrorMessage(undefined)).toBeNull();
  });
});

describe('buildCreateTicketPayload', () => {
  const ticketState = ({ fields, formData }) => ({
    isValid: true,
    selectedConfig: { id: 'cfg-1' },
    selectedProject: { key: 'KAN' },
    selectedIssueType: 'Task',
    ticketDetails: { subject: 'subj', description: 'desc' },
    formData,
    selectedIssueTypeTicketMetadata: [{ name: 'Task', fields }],
  });

  it('sources severity from the group-tagged field and strips it from additional_fields', () => {
    const state = ticketState({
      fields: {
        priority: { key: 'priority', name: 'Priority', type: 'select', group: 'severity' },
        customfield_100: { key: 'customfield_100', name: 'Team', type: 'select' },
      },
      formData: { priority: '3', customfield_100: '10023' },
    });
    const { payload, missingRequired } = buildCreateTicketPayload(state, { reference: { id: 'ref-1', type: 'ui' } });
    expect(missingRequired).toBeUndefined();
    expect(payload.severity).toBe('3');
    expect(payload.additional_fields).not.toHaveProperty('priority');
    expect(payload.additional_fields.customfield_100).toBe('10023');
  });

  it('reads urgency-keyed severity fields so ServiceNow/PagerDuty/Zenduty urgency is not dropped', () => {
    const state = ticketState({
      fields: { urgency: { key: 'urgency', name: 'Urgency', type: 'select', group: 'severity' } },
      formData: { urgency: 'high' },
    });
    const { payload } = buildCreateTicketPayload(state, {});
    expect(payload.severity).toBe('high');
    expect(payload.additional_fields).not.toHaveProperty('urgency');
  });

  it('falls back to formData.priority when no field carries the severity group', () => {
    const state = ticketState({
      fields: { labels: { key: 'labels', name: 'Labels', type: 'array' } },
      formData: { priority: '2', labels: ['a'] },
    });
    const { payload } = buildCreateTicketPayload(state, {});
    expect(payload.severity).toBe('2');
    expect(payload.additional_fields).not.toHaveProperty('priority');
    expect(payload.additional_fields.labels).toEqual(['a']);
  });

  it('keeps assignee out of additional_fields but on the payload root', () => {
    const state = ticketState({
      fields: { assignee: { key: 'assignee', name: 'Assignee', type: 'select' } },
      formData: { assignee: '557058:abc' },
    });
    const { payload } = buildCreateTicketPayload(state, {});
    expect(payload.assignee).toBe('557058:abc');
    expect(payload.additional_fields).not.toHaveProperty('assignee');
  });

  it('reports empty required dynamic fields by display name instead of building a payload', () => {
    const state = ticketState({
      fields: {
        customfield_200: { key: 'customfield_200', name: 'nudgebee_teams', type: 'multiselect', required: true },
      },
      formData: { customfield_200: [] },
    });
    const { payload, missingRequired } = buildCreateTicketPayload(state, {});
    expect(payload).toBeUndefined();
    expect(missingRequired).toEqual(['nudgebee_teams']);
  });

  it('does not report a required severity-group field the user has filled', () => {
    const state = ticketState({
      fields: { priority: { key: 'priority', name: 'Priority', type: 'select', group: 'severity', required: true } },
      formData: { priority: '1' },
    });
    const { payload, missingRequired } = buildCreateTicketPayload(state, {});
    expect(missingRequired).toBeUndefined();
    expect(payload.severity).toBe('1');
  });

  it('omits blank optional date fields instead of defaulting them to now', () => {
    const state = ticketState({
      fields: {
        duedate: { key: 'duedate', name: 'Due date', type: 'datepicker' },
        customfield_300: { key: 'customfield_300', name: 'Start', type: 'datetime' },
      },
      formData: {},
    });
    const { payload } = buildCreateTicketPayload(state, {});
    expect(payload.additional_fields).not.toHaveProperty('duedate');
    expect(payload.additional_fields).not.toHaveProperty('customfield_300');
  });

  it('converts datepicker and datetime values to ISO strings', () => {
    const ts = new Date('2026-07-17T10:30:00.000Z').getTime();
    const state = ticketState({
      fields: {
        duedate: { key: 'duedate', name: 'Due date', type: 'datepicker' },
        customfield_300: { key: 'customfield_300', name: 'Start', type: 'datetime' },
      },
      formData: { duedate: ts, customfield_300: ts },
    });
    const { payload } = buildCreateTicketPayload(state, {});
    expect(payload.additional_fields.duedate).toBe('2026-07-17');
    expect(payload.additional_fields.customfield_300).toBe('2026-07-17T10:30:00.000Z');
  });
});
