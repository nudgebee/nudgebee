// listTaskDefinitions swaps the tenant brand into runbook-server task descriptions, which are
// copied into workflow node state, so it must wait for branding instead of latching the default.

const mockQueryGraphQL = jest.fn();
jest.mock('@lib/HttpService', () => ({
  queryGraphQL: (...args: any[]) => mockQueryGraphQL(...args),
}));

let mockTitle = 'Nudgebee';
let mockResolvedTitle = 'Calsoft';
let mockResolveBranding: () => void = () => {};
jest.mock('@hooks/useTenantBranding', () => ({
  getBrandTitle: () => mockTitle,
  whenBrandingReady: () =>
    new Promise<void>((resolve) => {
      mockResolveBranding = () => {
        mockTitle = mockResolvedTitle;
        resolve();
      };
    }),
}));

import apiWorkflow from '@api1/workflow';

const tasksResponse = () => ({
  data: {
    data: {
      workflow_list_taskdefinitions: {
        tasks: [
          {
            name: 'events.store',
            description: 'Save data to Nudgebee for later troubleshooting and analysis.',
            input_schema: { event: { description: 'Nudgebee event payload' } },
          },
          { name: 'data.filter', description: 'Keep only the items in a list that match a condition.' },
          { name: 'no.description' },
        ],
      },
    },
  },
});

describe('apiWorkflow.listTaskDefinitions branding', () => {
  beforeEach(() => {
    mockTitle = 'Nudgebee';
    mockResolvedTitle = 'Calsoft';
    mockQueryGraphQL.mockReset().mockResolvedValue(tasksResponse());
  });

  it('brands task descriptions only after branding resolves', async () => {
    const pending = apiWorkflow.listTaskDefinitions();
    await new Promise((r) => setTimeout(r, 0));
    mockResolveBranding();

    const tasks = (await pending).data.workflow_list_taskdefinitions.tasks;
    expect(tasks[0].description).toBe('Save data to Calsoft for later troubleshooting and analysis.');
    expect(tasks[1].description).toBe('Keep only the items in a list that match a condition.');
    expect(tasks[2].description).toBeUndefined();
    expect(tasks[0].name).toBe('events.store');
    expect(tasks[0].input_schema.event.description).toBe('Nudgebee event payload');
  });

  it('inserts a brand containing $ literally', async () => {
    mockResolvedTitle = 'Cash$&Co';
    const pending = apiWorkflow.listTaskDefinitions();
    await new Promise((r) => setTimeout(r, 0));
    mockResolveBranding();

    const tasks = (await pending).data.workflow_list_taskdefinitions.tasks;
    expect(tasks[0].description).toBe('Save data to Cash$&Co for later troubleshooting and analysis.');
  });

  it('clears the fallback timer once branding resolves', async () => {
    jest.useFakeTimers();
    try {
      const pending = apiWorkflow.listTaskDefinitions();
      await jest.advanceTimersByTimeAsync(0);
      mockResolveBranding();
      await pending;
      expect(jest.getTimerCount()).toBe(0);
    } finally {
      jest.useRealTimers();
    }
  });

  it('returns the task list with the default brand when branding stalls', async () => {
    jest.useFakeTimers();
    try {
      const pending = apiWorkflow.listTaskDefinitions();
      await jest.advanceTimersByTimeAsync(3000);

      const tasks = (await pending).data.workflow_list_taskdefinitions.tasks;
      expect(tasks[0].description).toBe('Save data to Nudgebee for later troubleshooting and analysis.');
    } finally {
      jest.useRealTimers();
    }
  });
});
