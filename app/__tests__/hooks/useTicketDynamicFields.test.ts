import { renderHook, waitFor } from '@testing-library/react';
import { useTicketDynamicFields } from '@components1/workflow/hooks/data-fetchers/useTicketDynamicFields';
import apiTickets from '@api1/tickets';

jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: {
    listTicketConfigurations: jest.fn(),
    getTicketMeta: jest.fn(),
    getTicketFieldValues: jest.fn(),
  },
}));

const mockApi = apiTickets as unknown as {
  listTicketConfigurations: jest.Mock;
  getTicketMeta: jest.Mock;
  getTicketFieldValues: jest.Mock;
};

// Build the create-meta envelope the hook unwraps via res.data.tickets_get_create_meta.data.
const meta = (template: any) => ({ data: { tickets_get_create_meta: { data: [template] } } });

const renderForTool = (tool: string, fields: Record<string, any>, opts?: { projectKey?: string; ticketType?: string }) => {
  mockApi.listTicketConfigurations.mockResolvedValue({
    data: [{ id: 'int-1', tool, projects: [{ key: 'PROJ', name: 'Proj' }] }],
  });
  mockApi.getTicketMeta.mockResolvedValue(meta({ name: 'Task', fields }));
  mockApi.getTicketFieldValues.mockResolvedValue({ data: { tickets_get_field_values: { data: [] } } });
  return renderHook(() =>
    useTicketDynamicFields({
      isTicketCreateTask: true,
      integrationId: 'int-1',
      projectKey: opts?.projectKey ?? 'PROJ',
      ticketType: opts?.ticketType ?? 'Task',
    })
  );
};

// Mirrors the ActionDetailsSidebar Platform Fields filter — the de-duplication contract.
const platformKeys = (dynamicFields: Record<string, any>) =>
  Object.entries(dynamicFields)
    .filter(([, m]: [string, any]) => !m.group)
    .map(([k]) => k);

beforeEach(() => jest.clearAllMocks());

describe('useTicketDynamicFields severity ownership + de-duplication', () => {
  it('sources Severity from the group=severity field (Jira priority) and never duplicates it as a Platform Field', async () => {
    const { result } = renderForTool('jira', {
      priority: { key: 'priority', name: 'Priority', type: 'select', group: 'severity', allowedValues: [{ id: '1', name: 'High', value: '1' }] },
      customfield_10: { key: 'customfield_10', name: 'Sprint', type: 'select', allowedValues: [{ id: 's1', name: 'S1' }] },
    });

    await waitFor(() => expect(result.current.ticketFieldOptions.priority).toBeDefined());
    expect(result.current.ticketFieldOptions.priority).toEqual([{ label: 'High', value: '1' }]);

    // Invariant: a basic-group field is excluded from Platform Fields; only the custom field remains.
    expect(platformKeys(result.current.ticketDynamicFields)).toEqual(['customfield_10']);
    // Severity source exposed so the UI can label it "Priority" for Jira.
    expect(result.current.ticketSeverityField).toEqual({ key: 'priority', name: 'Priority' });
  });

  it('reports no severity source for tools without one (GitHub) so the UI can hide Severity', async () => {
    const { result } = renderForTool('github', {
      assignee: { key: 'assignee', name: 'Assignee', type: 'select', allowedValues: [{ id: 'u', name: 'u', value: 'u' }] },
      labels: { key: 'labels', name: 'Labels', type: 'array', allowedValues: [{ id: 'bug', name: 'bug', value: 'bug' }] },
    });
    await waitFor(() => expect(Object.keys(result.current.ticketDynamicFields).length).toBeGreaterThan(0));
    expect(result.current.ticketSeverityField).toBeNull();
    expect(result.current.ticketFieldOptions.priority).toBeUndefined();
  });

  it('maps urgency->Severity via the group tag (no key alias) and passes the API token through as value', async () => {
    const { result } = renderForTool('pagerduty', {
      urgency: {
        key: 'urgency',
        name: 'Urgency',
        type: 'select',
        group: 'severity',
        allowedValues: [
          { id: 'high', name: 'High', value: 'high' },
          { id: 'low', name: 'Low', value: 'low' },
        ],
      },
      service: { key: 'service', name: 'Service', type: 'select', required: true, allowedValues: [{ id: 'svc', name: 'Svc', value: 'svc' }] },
    });

    await waitFor(() => expect(result.current.ticketFieldOptions.priority).toBeDefined());
    // Severity options come from urgency; value is the API token ("high"), not the id.
    expect(result.current.ticketFieldOptions.priority).toEqual([
      { label: 'High', value: 'high' },
      { label: 'Low', value: 'low' },
    ]);
    expect(platformKeys(result.current.ticketDynamicFields)).toEqual(['service']);
    // PagerDuty labels the field "Urgency", not "Severity".
    expect(result.current.ticketSeverityField).toEqual({ key: 'urgency', name: 'Urgency' });
  });

  it('passes the explicit option value through instead of preferring id (ZenDuty urgency tokens)', async () => {
    const { result } = renderForTool('zenduty', {
      urgency: {
        key: 'urgency',
        name: 'Urgency',
        type: 'select',
        group: 'severity',
        allowedValues: [{ id: '0', name: 'Low', value: 'low' }],
      },
    });
    await waitFor(() => expect(result.current.ticketFieldOptions.priority).toBeDefined());
    expect(result.current.ticketFieldOptions.priority).toEqual([{ label: 'Low', value: 'low' }]);
  });
});

describe('useTicketDynamicFields assignee seeding', () => {
  it('renders seeded assignee options without an autocomplete round-trip', async () => {
    const { result } = renderForTool('jira', {
      assignee: {
        key: 'assignee',
        name: 'Assignee',
        type: 'select',
        autoCompleteUrl: 'https://x/y?username=',
        allowedValues: [{ id: 'acc-1', name: 'Ada', value: 'acc-1' }],
      },
    });
    await waitFor(() => expect(result.current.ticketFieldOptions.assignee).toBeDefined());
    expect(result.current.ticketFieldOptions.assignee).toEqual([{ label: 'Ada', value: 'acc-1' }]);
    expect(mockApi.getTicketFieldValues).not.toHaveBeenCalled();
  });

  it('seeds an unseeded assignee via one empty-query autocomplete fetch', async () => {
    renderForTool('jira', {
      assignee: { key: 'assignee', name: 'Assignee', type: 'select', autoCompleteUrl: 'https://x/y?username=' },
    });
    await waitFor(() => expect(mockApi.getTicketFieldValues).toHaveBeenCalledTimes(1));
    expect(mockApi.getTicketFieldValues).toHaveBeenCalledWith('int-1', 'assignee', 'https://x/y?username=', '');
  });
});

describe('useTicketDynamicFields ServiceNow', () => {
  it('fetches create-meta for ServiceNow (no more short-circuit)', async () => {
    const { result } = renderForTool(
      'servicenow',
      {
        urgency: { key: 'urgency', name: 'Urgency', type: 'select', group: 'severity', allowedValues: [{ id: '1', name: 'High', value: 'High' }] },
      },
      { projectKey: 'incident', ticketType: 'incident' }
    );
    await waitFor(() => expect(mockApi.getTicketMeta).toHaveBeenCalled());
    expect(mockApi.getTicketMeta).toHaveBeenCalledWith('int-1', 'incident');
    await waitFor(() => expect(result.current.ticketFieldOptions.priority).toEqual([{ label: 'High', value: 'High' }]));
  });
});
