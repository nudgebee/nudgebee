import React from 'react';
import { render, screen, act } from '@testing-library/react';
import apiWorkflow from '@api1/workflow';
import RunAutomationMenu from '@components/workflow/components/RunAutomationMenu';

jest.mock('next/router', () => ({ useRouter: () => ({ push: jest.fn() }) }));
jest.mock('@shared/snackbarService', () => ({ snackbar: { success: jest.fn(), error: jest.fn() } }));
jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    listWorkflows: jest.fn().mockResolvedValue({ data: { workflow_list: { workflows: [] } } }),
    listExecutionsForEvent: jest.fn().mockResolvedValue({ data: { workflow_list_executions_for_event: { executions: [] } } }),
    triggerWorkflow: jest.fn(),
  },
}));

const listWorkflows = apiWorkflow.listWorkflows as jest.Mock;
const built = { id: 'wf-1', name: 'elasticsearch-anomaly-rca', status: 'ACTIVE', definition: { triggers: [{ type: 'manual' }] } };

const renderMenu = (props: Partial<React.ComponentProps<typeof RunAutomationMenu>> = {}) => (
  <RunAutomationMenu accountId='account-1' eventId='event-1' canView canRun {...props} />
);

// #40444: the dropdown loads its list once per account, so an automation the AI window has just
// built was missing until the page was reloaded.
describe('RunAutomationMenu — after an AI build on the event page', () => {
  beforeEach(() => listWorkflows.mockClear());

  it('refetches the automation list when refreshKey changes, and not before', async () => {
    const { rerender } = render(renderMenu({ refreshKey: 0 }));
    await act(async () => {});
    expect(listWorkflows).toHaveBeenCalledTimes(1);

    rerender(renderMenu({ refreshKey: 0 }));
    await act(async () => {});
    expect(listWorkflows).toHaveBeenCalledTimes(1);

    rerender(renderMenu({ refreshKey: 1 }));
    await act(async () => {});
    expect(listWorkflows).toHaveBeenCalledTimes(2);
  });

  it('opens the run dialog for the automation a run request names, then reports it handled', async () => {
    const onRunRequestHandled = jest.fn();
    render(renderMenu({ runRequest: { workflow: built, key: 1 }, onRunRequestHandled }));
    await act(async () => {});
    expect(screen.getByText('Trigger Automation: elasticsearch-anomaly-rca')).toBeInTheDocument();
    expect(onRunRequestHandled).toHaveBeenCalledTimes(1);
  });

  it('ignores a run request from a user who may not run automations', async () => {
    render(renderMenu({ canRun: false, runRequest: { workflow: built, key: 1 } }));
    await act(async () => {});
    expect(screen.queryByText('Trigger Automation: elasticsearch-anomaly-rca')).not.toBeInTheDocument();
  });
});
