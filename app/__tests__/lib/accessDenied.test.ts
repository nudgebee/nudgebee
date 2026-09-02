import { snackbar } from '@shared/snackbarService';
import { isAccessDenied, notifyAccessDenied, reportAccessDeniedForOperation, OPERATION_ACCESS_SECTIONS } from '@lib/accessDenied';

describe('isAccessDenied', () => {
  it('detects the gateway role-gate (FORBIDDEN)', () => {
    expect(isAccessDenied({ data: { errors: [{ extensions: { code: 'FORBIDDEN' } }] } })).toBe(true);
  });

  it('detects a role-less tenant (NO_TENANT_ROLE)', () => {
    expect(isAccessDenied({ data: { errors: [{ extensions: { code: 'NO_TENANT_ROLE' } }] } })).toBe(true);
  });

  it('detects an upstream query-engine 403', () => {
    expect(isAccessDenied({ data: { errors: [{ extensions: { upstream: { status: 403 } } }] } })).toBe(true);
  });

  it('accepts an unwrapped body and a thrown-error shape', () => {
    expect(isAccessDenied({ errors: [{ extensions: { code: 'FORBIDDEN' } }] })).toBe(true);
    expect(isAccessDenied({ response: { data: { errors: [{ extensions: { upstream: { status: 403 } } }] } } })).toBe(true);
  });

  it('is false for success, non-403 errors, and junk', () => {
    expect(isAccessDenied({ data: { data: { insights_list: { rows: [] } } } })).toBe(false);
    expect(isAccessDenied({ data: { errors: [{ extensions: { upstream: { status: 500 } } }] } })).toBe(false);
    expect(isAccessDenied({ data: { errors: [{ message: 'boom' }] } })).toBe(false);
    expect(isAccessDenied(null)).toBe(false);
    expect(isAccessDenied('nope')).toBe(false);
  });
});

describe('notifyAccessDenied batching', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });
  afterEach(() => {
    jest.clearAllTimers();
    jest.useRealTimers();
    jest.restoreAllMocks();
  });

  it('consolidates sections queued in the same burst into one toast', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    notifyAccessDenied('BatchOne');
    notifyAccessDenied('BatchTwo');
    expect(spy).not.toHaveBeenCalled(); // batched, not yet flushed

    jest.advanceTimersByTime(400);
    expect(spy).toHaveBeenCalledTimes(1);
    const message = spy.mock.calls[0][0] as string;
    expect(message).toContain('BatchOne');
    expect(message).toContain('BatchTwo');
  });

  it('suppresses a repeat of the same section within the window', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    notifyAccessDenied('RepeatSection');
    jest.advanceTimersByTime(400);
    expect(spy).toHaveBeenCalledTimes(1);

    notifyAccessDenied('RepeatSection');
    jest.advanceTimersByTime(400);
    expect(spy).toHaveBeenCalledTimes(1); // suppressed within 8s
  });

  it('uses a singular phrasing for a lone section', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    notifyAccessDenied('SoloSection');
    jest.advanceTimersByTime(400);
    expect(spy.mock.calls[0][0]).toBe("You don't have permission to view SoloSection.");
  });
});

describe('reportAccessDeniedForOperation', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });
  afterEach(() => {
    jest.clearAllTimers();
    jest.useRealTimers();
    jest.restoreAllMocks();
  });

  it('toasts the mapped section on a denied response', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    // k8s_nodes_list -> "Nodes" (label unique to this test to avoid suppression bleed)
    expect(OPERATION_ACCESS_SECTIONS.k8s_nodes_list).toBe('Nodes');
    reportAccessDeniedForOperation('k8s_nodes_list', { data: { errors: [{ extensions: { upstream: { status: 403 } } }] } });
    jest.advanceTimersByTime(400);
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0][0]).toContain('Nodes');
  });

  it('ignores unmapped operations', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    reportAccessDeniedForOperation('SomeUnmappedOperation', { data: { errors: [{ extensions: { code: 'FORBIDDEN' } }] } });
    jest.advanceTimersByTime(400);
    expect(spy).not.toHaveBeenCalled();
  });

  it('ignores a successful response for a mapped operation', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    reportAccessDeniedForOperation('k8s_pods_list', { data: { data: { k8s_pods: [] } } });
    jest.advanceTimersByTime(400);
    expect(spy).not.toHaveBeenCalled();
  });

  it('matches split-query operations forwarded as `${base}_${alias}`', () => {
    const spy = jest.spyOn(snackbar, 'error').mockImplementation(() => {});
    // splitAndParallelQuery forwards K8sOptimizeSummaryInfographics per-field.
    reportAccessDeniedForOperation('K8sOptimizeSummaryInfographics_workload_rightsize', {
      data: { errors: [{ extensions: { code: 'FORBIDDEN' } }] },
    });
    jest.advanceTimersByTime(400);
    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy.mock.calls[0][0]).toContain('Optimize Summary');
  });
});
