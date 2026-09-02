import { describeCron, nextCronRuns, validateCron } from '../cron';

describe('validateCron', () => {
  it.each([
    '0 9 * * 1-5',
    '*/15 * * * *',
    '0 0,12 1 */2 *',
    '0 9 * * MON-FRI',
    '0 9 ? * *',
    '@daily',
    '@every 1h',
    '@every 1h30m',
    'CRON_TZ=Asia/Kolkata 0 9 * * *',
    'TZ=UTC 0 9 * * *',
  ])('accepts %s', (expr) => {
    expect(validateCron(expr).valid).toBe(true);
  });

  it.each([
    ['', 'empty'],
    ['not a cron', 'garbage'],
    ['99 * * * *', 'minute out of range'],
    ['0 9 * *', 'four fields'],
    ['0 9 * * * *', 'six fields — the seconds form robfig/cron rejects'],
    ['* * * * 7', 'day-of-week 7, allowed by cron-parser but not by robfig/cron'],
    ['@reboot', 'unsupported descriptor'],
    ['@every 1 hour', 'not a Go duration'],
    ['CRON_TZ=Asia/Kolkata', 'timezone prefix with no schedule'],
  ])('rejects %s (%s)', (expr) => {
    const result = validateCron(expr);
    expect(result.valid).toBe(false);
    expect(result.error).toBeTruthy();
  });
});

describe('describeCron', () => {
  it('describes a standard expression', () => {
    expect(describeCron('0 9 * * 1-5')).toContain('Monday through Friday');
  });

  it('describes descriptors without going through cronstrue', () => {
    expect(describeCron('@daily')).toBe('Every day at midnight');
    expect(describeCron('@every 15m')).toBe('Every 15m');
  });

  it('returns an empty string for an invalid expression', () => {
    expect(describeCron('not a cron')).toBe('');
  });
});

describe('nextCronRuns', () => {
  it('returns the requested number of UTC run times in ascending order', () => {
    const runs = nextCronRuns('0 9 * * *', 3);
    expect(runs).toHaveLength(3);
    runs.forEach((run) => expect(run.getUTCHours()).toBe(9));
    expect(runs[0].getTime()).toBeLessThan(runs[1].getTime());
    expect(runs[1].getTime()).toBeLessThan(runs[2].getTime());
  });

  it('returns nothing for @every, whose next run depends on registration time', () => {
    expect(nextCronRuns('@every 1h')).toEqual([]);
  });

  it('returns nothing for an invalid expression', () => {
    expect(nextCronRuns('99 * * * *')).toEqual([]);
  });
});
