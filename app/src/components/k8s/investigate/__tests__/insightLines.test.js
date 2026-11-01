import { uniqueInsightLines } from '../insightLines';

describe('uniqueInsightLines', () => {
  it('keeps one copy of a repeated line, in the order first seen', () => {
    expect(
      uniqueInsightLines(['Back-off restarting failed container app', 'Readiness probe failed', 'Back-off restarting failed container app'])
    ).toEqual(['Back-off restarting failed container app', 'Readiness probe failed']);
  });

  it('treats the "combined from similar events" form as the same line', () => {
    expect(
      uniqueInsightLines([
        'Memory cgroup out of memory: Killed process 435000 (python)',
        '(combined from similar events): Memory cgroup out of memory: Killed process 435000 (python)',
      ])
    ).toEqual(['Memory cgroup out of memory: Killed process 435000 (python)']);
    expect(uniqueInsightLines(['(combined from similar events): only seen in this form'])).toEqual([
      '(combined from similar events): only seen in this form',
    ]);
  });

  it('treats a line Kubernetes cut short as a repeat of the full line, and shows the full one', () => {
    const full = 'Memory cgroup out of memory: Killed process 435000 (python) total-vm:532500kB, oom_score_adj:983';
    const cut = '(combined from similar events): Memory cgroup out of memory: Killed process 435000 (python) total-vm:532500kB, oom_score_adj:9…';
    expect(uniqueInsightLines([full, cut])).toEqual([full]);
    expect(uniqueInsightLines(['Readiness probe failed', cut, full])).toEqual(['Readiness probe failed', full]);
  });

  it('keeps a short line that merely starts another line', () => {
    expect(uniqueInsightLines(['Error', 'Error: ImagePullBackOff'])).toEqual(['Error', 'Error: ImagePullBackOff']);
    expect(uniqueInsightLines(['Waiting...', 'Waiting for volume to attach'])).toEqual(['Waiting...', 'Waiting for volume to attach']);
  });

  it('drops empty entries and tolerates a missing list', () => {
    expect(uniqueInsightLines(['', '  ', null, 'Error: ImagePullBackOff'])).toEqual(['Error: ImagePullBackOff']);
    expect(uniqueInsightLines(undefined)).toEqual([]);
  });
});
