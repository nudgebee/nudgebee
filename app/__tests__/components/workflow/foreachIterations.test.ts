import { summarizeForeachIterations } from '@components/workflow/utils/foreachIterations';

describe('summarizeForeachIterations', () => {
  it('returns null when children is not an array (no execution data)', () => {
    expect(summarizeForeachIterations(undefined)).toBeNull();
    expect(summarizeForeachIterations(null)).toBeNull();
    expect(summarizeForeachIterations('not-an-array')).toBeNull();
  });

  it('returns zeros for an empty children array (items list was empty)', () => {
    expect(summarizeForeachIterations([])).toEqual({ total: 0, completed: 0, failed: 0, running: 0 });
  });

  it('counts completed, failed and running iterations', () => {
    const children = [
      { id: 'Iteration 0', type: 'iteration', status: 'COMPLETED' },
      { id: 'Iteration 1', type: 'iteration', status: 'COMPLETED' },
      { id: 'Iteration 2', type: 'iteration', status: 'FAILED' },
      { id: 'Iteration 3', type: 'iteration', status: 'STARTED' },
      { id: 'Iteration 4', type: 'iteration', status: 'SCHEDULED' },
    ];
    expect(summarizeForeachIterations(children)).toEqual({ total: 5, completed: 2, failed: 1, running: 2 });
  });

  it('ignores non-iteration entries mixed into children', () => {
    const children = [
      { id: 'Iteration 0', type: 'iteration', status: 'COMPLETED' },
      { id: 'stray-task', type: 'core.print', status: 'COMPLETED' },
    ];
    expect(summarizeForeachIterations(children)).toEqual({ total: 1, completed: 1, failed: 0, running: 0 });
  });

  it('is case-insensitive on status and defaults unknown statuses to completed', () => {
    const children = [
      { id: 'Iteration 0', type: 'iteration', status: 'failed' },
      { id: 'Iteration 1', type: 'iteration', status: 'running' },
      { id: 'Iteration 2', type: 'iteration' },
    ];
    expect(summarizeForeachIterations(children)).toEqual({ total: 3, completed: 1, failed: 1, running: 1 });
  });
});
