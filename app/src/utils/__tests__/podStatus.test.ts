import { podDisplayStatus } from '../podStatus';

describe('podDisplayStatus', () => {
  it('shows the container reason over the phase — a CrashLoopBackOff pod has phase Running', () => {
    expect(podDisplayStatus({ status: 'Running', container_status: 'CrashLoopBackOff' })).toBe('CrashLoopBackOff');
  });

  it('shows the container reason for a pod stuck pulling an image, whose phase is Pending', () => {
    expect(podDisplayStatus({ status: 'Pending', container_status: 'ImagePullBackOff' })).toBe('ImagePullBackOff');
  });

  it('falls back to the phase for a healthy pod, where there is no container reason', () => {
    expect(podDisplayStatus({ status: 'Running', container_status: null })).toBe('Running');
  });

  it('falls back to the phase when the agent is too old to report container statuses', () => {
    expect(podDisplayStatus({ status: 'Running' })).toBe('Running');
  });

  it('renders a dash rather than blank when neither is known', () => {
    expect(podDisplayStatus({})).toBe('-');
  });

  it('keeps showing the phase for a pod whose sidecar completed while it runs', () => {
    // container_status is null here by design: a container that exited 0 is not a
    // problem, so a completed sidecar must not make a running pod read "Completed".
    expect(podDisplayStatus({ status: 'Running', container_status: null })).toBe('Running');
  });
});
