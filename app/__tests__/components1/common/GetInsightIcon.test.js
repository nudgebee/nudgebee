import React from 'react';
import { GetInsightIcon } from '@components1/common/GetInsightIcon';

jest.mock('@assets', () => ({
  CpuRedIcon: '/cpu-red.svg',
  InfoRedIcon: '/info-red.svg',
  MemoryRedIcon: '/memory-red.svg',
  WrenchIcon: '/wrench.svg',
  AutoPilotGreyIcon: '/autopilot.svg',
  MenuArrowDownIcon: '/arrow.svg',
  checklistIcon: '/check.svg',
  checkIconBold: '/check-bold.svg',
  checkFilledIcon: '/check-filled.svg',
  AskNudgebeeSkipIcon: '/skip.svg',
  RunningIcon: '/running.svg',
  timelapseBlackSVG: '/timelapse-black.svg',
  AskNudgebeeErrorIcon: '/error.svg',
  timelapse: '/timelapse.svg',
  AskNudgebeeLayoutV2: '/layout.svg',
}));

describe('GetInsightIcon', () => {
  test('returns InfoRedIcon for Event source', () => {
    const result = GetInsightIcon({ source: 'Event', title: 'Some event' });
    expect(result).toBe('/info-red.svg');
  });

  test('returns WrenchIcon for Recommendation source', () => {
    const result = GetInsightIcon({ source: 'Recommendation', title: 'Some recommendation' });
    expect(result).toBe('/wrench.svg');
  });

  test('returns MemoryRedIcon for Metric source with memory in title', () => {
    const result = GetInsightIcon({ source: 'Metric', title: 'High memory usage' });
    expect(result).toBe('/memory-red.svg');
  });

  test('returns CpuRedIcon for Metric source with cpu in title', () => {
    const result = GetInsightIcon({ source: 'Metric', title: 'CPU throttling detected' });
    expect(result).toBe('/cpu-red.svg');
  });

  test('returns WrenchIcon for Metric source with other title', () => {
    const result = GetInsightIcon({ source: 'Metric', title: 'Network latency spike' });
    expect(result).toBe('/wrench.svg');
  });

  test('returns WrenchIcon for unknown source', () => {
    const result = GetInsightIcon({ source: 'UnknownSource', title: 'Something happened' });
    expect(result).toBe('/wrench.svg');
  });
});
