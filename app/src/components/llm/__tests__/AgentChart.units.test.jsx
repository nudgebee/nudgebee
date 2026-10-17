import { FORMATTERS, parseChartSpec } from '../common/AgentChart';

describe('AgentChart metric units', () => {
  it('keeps the unit formats metric charts carry', () => {
    const spec = { type: 'line', labels: ['10:00'], series: [{ key: 'a', data: [1] }] };
    ['bytes', 'bytes_per_second', 'seconds'].forEach((format) => {
      expect(parseChartSpec({ ...spec, format }).format).toBe(format);
    });
    expect(parseChartSpec({ ...spec, format: 'furlongs' }).format).toBe('number');
  });

  it('scales bytes to binary units', () => {
    expect(FORMATTERS.bytes(512)).toBe('512 B');
    expect(FORMATTERS.bytes(152043520)).toBe('145 MiB');
    expect(FORMATTERS.bytes(1.5 * 1024 ** 3)).toBe('1.5 GiB');
    expect(FORMATTERS.bytes_per_second(2048)).toBe('2 KiB/s');
  });

  it('picks a readable duration unit', () => {
    expect(FORMATTERS.seconds(0.25)).toBe('250 ms');
    expect(FORMATTERS.seconds(0)).toBe('0 s');
    expect(FORMATTERS.seconds(45)).toBe('45 s');
    expect(FORMATTERS.seconds(600)).toBe('10 min');
    expect(FORMATTERS.seconds(5400)).toBe('90 min');
    expect(FORMATTERS.seconds(9000)).toBe('2.5 h');
  });
});
