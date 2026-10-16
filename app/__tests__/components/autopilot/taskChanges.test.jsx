import { parseRecommendationMeta } from '@components/autopilot/taskChanges';
import { formatClockCompact } from '@shared/format/Datetime';

describe('parseRecommendationMeta — replica tasks', () => {
  it('shows the hour a forecast target was forecast for', () => {
    const forecastFor = '2026-10-16T14:00:00Z';
    const [[label, current, target]] = parseRecommendationMeta({ change_to: 6, forecast_for: forecastFor });
    expect(label.text).toBe('Replicas');
    expect(current.text).toBe('current');
    expect(target.text).toBe(`6 (forecast for ${formatClockCompact(forecastFor)})`);
  });

  it('shows a plain target when it was not forecast', () => {
    const [[, , target]] = parseRecommendationMeta({ change_to: 4 });
    expect(target.text).toBe('4');
  });

  it('keeps the volume row unchanged', () => {
    const [[label, , target]] = parseRecommendationMeta({ change_by: '10%' });
    expect(label.text).toBe('Storage size');
    expect(target.text).toBe('+10%');
  });
});
