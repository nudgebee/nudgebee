import { render, screen, fireEvent, act } from '@testing-library/react';
import dayjs from 'dayjs';
import GatewayUsage from '@components/llm/gateway-usage/GatewayUsage';

// The shell half of the Overview drill-down: applying a picked day to the shared
// filters, and — the part that doesn't come for free — moving the date picker to
// match. The picker snapshots its value on mount and only re-reads it when
// remounted, so a window changed from outside it would otherwise keep displaying
// the old range while the data below refreshed.

// Holder must be `mock`-prefixed to be reachable from a hoisted jest.mock factory.
const mockPicker = { mounts: 0, value: null as { startTime: number; endTime: number } | null };

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => {
  const React = require('react');
  return {
    __esModule: true,
    default: ({ passedSelectedDateTime }: any) => {
      mockPicker.value = passedSelectedDateTime;
      React.useEffect(() => {
        mockPicker.mounts += 1;
      }, []);
      return <div data-testid='picker' />;
    },
  };
});

jest.mock('@components/llm/gateway-usage/useGatewayData', () => ({
  useGatewayData: () => ({ loading: false, error: null, metrics: null }),
}));

jest.mock('@components/llm/gateway-usage/views/OverviewView', () => ({
  __esModule: true,
  default: ({ onSelectDay }: any) => (
    <button data-testid='drill-day' onClick={() => onSelectDay?.('2026-07-14')}>
      drill
    </button>
  ),
}));

jest.mock('@shared/navigation/Tabs', () => ({ __esModule: true, default: () => <div /> }));
jest.mock('@ui/ToggleGroup', () => ({ ToggleGroup: () => <div /> }));

describe('GatewayUsage: applying an Overview drill-down', () => {
  beforeEach(() => {
    mockPicker.mounts = 0;
    mockPicker.value = null;
  });

  it('narrows the window to the clicked day', () => {
    render(<GatewayUsage />);
    act(() => {
      fireEvent.click(screen.getByTestId('drill-day'));
    });
    expect(dayjs(mockPicker.value!.startTime).format('YYYY-MM-DD')).toBe('2026-07-14');
    expect(dayjs(mockPicker.value!.endTime).format('YYYY-MM-DD')).toBe('2026-07-14');
  });

  it('remounts the date picker so it displays the new window', () => {
    render(<GatewayUsage />);
    const before = mockPicker.mounts;
    act(() => {
      fireEvent.click(screen.getByTestId('drill-day'));
    });
    expect(mockPicker.mounts).toBe(before + 1);
  });

  it('does not remount the picker on an unrelated re-render', () => {
    const { rerender } = render(<GatewayUsage />);
    const before = mockPicker.mounts;
    rerender(<GatewayUsage />);
    // Only a drill-in re-keys it — the user driving the picker must not fight a remount.
    expect(mockPicker.mounts).toBe(before);
  });
});
