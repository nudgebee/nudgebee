import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import AutoRefreshControls from '@shared/widgets/AutoRefreshControls';

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ onSelect, value, label }: { onSelect: (e: React.ChangeEvent<HTMLSelectElement>) => void; value: string; label: string }) => (
    <select data-testid='refresh-dropdown' value={value} onChange={onSelect} aria-label={label}>
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
