import { executionAutomationLabel, nonPersistedAutomationLabel } from '@components/workflow/execution-dashboard/constants';

// The backend resolves workflow_name from the `workflows` table keyed on the
// nb_workflow_id search attribute. Dry-run and inline runs have no row there, so
// the name never arrives and the column used to print the synthetic id.
describe('executionAutomationLabel', () => {
  it('prefers the resolved name', () => {
    expect(executionAutomationLabel('Restart unhealthy pods', 'd44166cb-9676-466d-9607-936401874fdc')).toBe('Restart unhealthy pods');
  });

  it('labels runs that were never persisted as automations', () => {
    expect(executionAutomationLabel(undefined, 'dry-run-98091125-f8fb-4681-ad83-b86f7be4f370')).toBe('Dry run');
    expect(executionAutomationLabel(undefined, 'inline-group-1')).toBe('Inline');
  });

  it('falls back to the id when a real automation has no resolved name', () => {
    expect(executionAutomationLabel(undefined, 'd44166cb-9676-466d-9607-936401874fdc')).toBe('d44166cb-9676-466d-9607-936401874fdc');
  });
});

describe('nonPersistedAutomationLabel', () => {
  // Drives whether the cell links to the builder — there is no builder page for
  // an id that has no `workflows` row.
  it('only matches the synthetic prefixes', () => {
    expect(nonPersistedAutomationLabel('dry-run-98091125-f8fb-4681-ad83-b86f7be4f370')).toBe('Dry run');
    expect(nonPersistedAutomationLabel('d44166cb-9676-466d-9607-936401874fdc')).toBeUndefined();
    expect(nonPersistedAutomationLabel(undefined)).toBeUndefined();
  });
});

// A visibility record written before the nb_workflow_id search attribute existed
// decodes to "" rather than being absent (the Go field has no omitempty), so the
// label must not treat it as a linkable automation.
describe('empty workflow id', () => {
  it('is not a non-persisted run and produces no label', () => {
    expect(nonPersistedAutomationLabel('')).toBeUndefined();
    expect(executionAutomationLabel(undefined, '')).toBe('');
  });
});
