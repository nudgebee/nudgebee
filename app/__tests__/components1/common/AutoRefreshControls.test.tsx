import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import AutoRefreshControls from '@components1/common/AutoRefreshControls';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      switchTrackDark: '#3B82F6',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      primaryHover: '#2563EB',
      primaryDisabled: '#93C5FD',
      primaryDisabledText: '#fff',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
      secondaryHover: '#F9FAFB',
      secondaryHoverBorder: '#9CA3AF',
      secondaryDisabled: '#F3F4F6',
      secondaryDisabledText: '#9CA3AF',
      secondaryDisabledBorder: '#E5E7EB',
      tertiary: '#EFF6FF',
      tertiaryBorder: '#BFDBFE',
      tertiaryText: '#3B82F6',
      tertiaryHover: '#DBEAFE',
      tertiaryDisabled: '#F9FAFB',
      tertiaryDisabledText: '#93C5FD',
      tertiaryDisabledBorder: '#DBEAFE',
    },
  },
}));

jest.mock('@components1/common/CustomDropdown', () => ({
  __esModule: true,
  default: ({ onChange, value, label }: { onChange: (e: React.ChangeEvent<HTMLSelectElement>) => void; value: string; label: string }) => (
    <select data-testid='refresh-dropdown' value={value} onChange={onChange} aria-label={label}>
      <option value='0'>Off</option>
      <option value='5'>Live</option>
      <option value='10'>10s</option>
    </select>
  ),
}));

describe('AutoRefreshControls', () => {
  beforeEach(() => {
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('renders without crashing', () => {
    const callBack = jest.fn();
    render(<AutoRefreshControls callBack={callBack} />);
    expect(screen.getByTestId('refresh-dropdown')).toBeInTheDocument();
  });

  it('renders the dropdown with label "Refresh"', () => {
    const callBack = jest.fn();
    render(<AutoRefreshControls callBack={callBack} />);
    expect(screen.getByLabelText('Refresh')).toBeInTheDocument();
  });

  it('default interval is "5"', () => {
    const callBack = jest.fn();
    render(<AutoRefreshControls callBack={callBack} />);
    const dropdown = screen.getByTestId('refresh-dropdown');
    expect(dropdown).toHaveValue('5');
  });

  it('calls callBack after interval when interval > 0 (use jest fake timers)', () => {
    const callBack = jest.fn();
    render(<AutoRefreshControls callBack={callBack} />);
    // Default interval is 5 seconds
    act(() => {
      jest.advanceTimersByTime(5000);
    });
    expect(callBack).toHaveBeenCalledWith(5);
  });

  it('does not call callBack when interval is "0" (Off)', () => {
    const callBack = jest.fn();
    render(<AutoRefreshControls callBack={callBack} />);
    const dropdown = screen.getByTestId('refresh-dropdown');
    fireEvent.change(dropdown, { target: { value: '0' } });
    act(() => {
      jest.advanceTimersByTime(10000);
    });
    expect(callBack).not.toHaveBeenCalled();
  });

  it('clears interval on unmount', () => {
    const callBack = jest.fn();
    const clearIntervalSpy = jest.spyOn(window, 'clearInterval');
    const { unmount } = render(<AutoRefreshControls callBack={callBack} />);
    unmount();
    expect(clearIntervalSpy).toHaveBeenCalled();
    clearIntervalSpy.mockRestore();
  });
});
