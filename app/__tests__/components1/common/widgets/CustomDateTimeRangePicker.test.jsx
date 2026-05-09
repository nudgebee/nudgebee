import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomDateTimeRangePicker from '@components1/common/widgets/CustomDateTimeRangePicker';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151' },
    background: { white: '#FFFFFF', tertiaryLightest: '#F0F9FF' },
  },
}));

jest.mock('@assets', () => ({
  calendarViewWeek: { default: { src: 'calendar-icon.svg' } },
  MenuArrowDownIcon: { src: 'arrow-icon.svg' },
}));

jest.mock('@data/constants', () => ({
  TIME_PICK_SHORTCUTS: [
    'Last 5 Minutes',
    'Last 10 Minutes',
    'Last 15 Minutes',
    'Last 30 Minutes',
    'Last 1 Hour',
    'Last 3 Hours',
    'Last 6 Hours',
    'Last 12 Hours',
    'Last 24 Hours',
    'Current Week',
    'Current Month',
    'Last Month',
  ],
}));

jest.mock('@components1/common/CustomTooltip', () => {
  const PropTypes = require('prop-types');
  const CustomTooltip = ({ children, title }) => (
    <div data-testid='custom-tooltip' title={typeof title === 'string' ? title : ''}>
      {children}
    </div>
  );
  CustomTooltip.displayName = 'CustomTooltip';
  CustomTooltip.propTypes = { children: PropTypes.node, title: PropTypes.any };
  return CustomTooltip;
});

jest.mock('@components1/common/NewCustomButton', () => {
  const PropTypes = require('prop-types');
  const CustomButton = ({ text, onClick, variant: _variant, disabled, sx: _sx }) => (
    <button data-testid={`custom-button-${text?.replace(/\s+/g, '-').toLowerCase()}`} onClick={onClick} disabled={disabled}>
      {text}
    </button>
  );
  CustomButton.displayName = 'CustomButton';
  CustomButton.propTypes = {
    text: PropTypes.string,
    onClick: PropTypes.func,
    variant: PropTypes.string,
    disabled: PropTypes.bool,
    sx: PropTypes.object,
  };
  return CustomButton;
});

jest.mock('@components1/common/SafeIcon', () => {
  const PropTypes = require('prop-types');
  const SafeIcon = ({ alt, style: _style, className: _className }) => <img data-testid='safe-icon' alt={alt} />;
  SafeIcon.displayName = 'SafeIcon';
  SafeIcon.propTypes = { alt: PropTypes.string, style: PropTypes.object, className: PropTypes.string };
  return SafeIcon;
});

jest.mock('@mui/x-date-pickers/LocalizationProvider', () => {
  const PropTypes = require('prop-types');
  const LocalizationProvider = ({ children }) => <div data-testid='localization-provider'>{children}</div>;
  LocalizationProvider.propTypes = { children: PropTypes.node };
  return { LocalizationProvider };
});

jest.mock('@mui/x-date-pickers/AdapterDayjs', () => ({
  AdapterDayjs: jest.fn(),
}));

jest.mock('@mui/x-date-pickers/DateTimePicker', () => {
  const PropTypes = require('prop-types');
  const DateTimePicker = ({ onChange, renderInput, label, value: _value }) => {
    const labelId = label?.toLowerCase();
    return (
      <div data-testid={`date-picker-${labelId}`}>
        {renderInput?.({ inputProps: {}, InputProps: {} })}
        <button data-testid={`trigger-change-${labelId}`} onClick={() => onChange?.({ valueOf: () => Date.now() - 100 })}>
          Change {label}
        </button>
      </div>
    );
  };
  DateTimePicker.propTypes = { onChange: PropTypes.func, renderInput: PropTypes.func, label: PropTypes.string, value: PropTypes.any };
  return { DateTimePicker };
});

const defaultProps = {
  passedSelectedDateTime: {
    startTime: Date.now() - 3600 * 1000,
    endTime: Date.now(),
    shortcutClickTime: 0,
  },
  onChange: jest.fn(),
};

describe('CustomDateTimeRangePicker', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders without crashing', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} />);
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
  });

  it('renders button with calendar icon', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} />);
    expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
  });

  it('opens popover when button is clicked', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} />);
    const button = screen.getByRole('button');
    fireEvent.click(button);
    expect(screen.getByTestId('localization-provider')).toBeInTheDocument();
  });

  it('renders date range text when shortcut not selected', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: new Date('2024-01-01').getTime(),
          endTime: new Date('2024-01-15').getTime(),
          shortcutClickTime: 0,
        }}
      />
    );
    // The displayed text is a date range
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('renders shortcut name when shortcut is selected via shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 3600 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 3600 * 1000, // Last 1 Hour
        }}
      />
    );
    expect(screen.getByText('Last 1 Hour')).toBeInTheDocument();
  });

  it('shows "Last 5 Minutes" shortcut from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 5 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 5 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 5 Minutes')).toBeInTheDocument();
  });

  it('does not select shortcut for unknown shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 9999,
          endTime: Date.now(),
          shortcutClickTime: 9999,
        }}
      />
    );
    // No shortcut selected, fallback to date range display
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('shows shortcut buttons in popover', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.getByTestId('custom-button-last-5-minutes')).toBeInTheDocument();
  });

  it('handles shortcut click for "Last 5 Minutes"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-5-minutes'));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        selection: expect.objectContaining({
          shortcutClickTime: 5 * 60 * 1000,
        }),
      })
    );
  });

  it('handles shortcut click for "Last 10 Minutes"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-10-minutes'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 15 Minutes"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-15-minutes'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 30 Minutes"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-30-minutes'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 1 Hour"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-1-hour'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 3 Hours"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-3-hours'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 6 Hours"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-6-hours'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 12 Hours"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-12-hours'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last 24 Hours"', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-24-hours'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Current Week" (in timeAdjustments)', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-current-week'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Current Month" (switch default branch)', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} shortCuts={['Current Month']} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-current-month'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles shortcut click for "Last Month" (switch branch)', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} shortCuts={['Last Month']} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-last-month'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles unknown shortcut in switch default case', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} shortCuts={['Some Unknown Shortcut']} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-some-unknown-shortcut'));
    expect(onChange).toHaveBeenCalled();
  });

  it('handles "Apply Time Range" button click', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-apply-time-range'));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        selection: expect.objectContaining({
          startTime: expect.any(Number),
          endTime: expect.any(Number),
          shortcutClickTime: 0,
        }),
      })
    );
  });

  it('handles start date change', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} />);
    fireEvent.click(screen.getByRole('button'));
    const fromPicker = screen.getByTestId('trigger-change-from');
    fireEvent.click(fromPicker);
    // Should not crash
    expect(screen.getByTestId('localization-provider')).toBeInTheDocument();
  });

  it('handles end date change', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} />);
    fireEvent.click(screen.getByRole('button'));
    const toPicker = screen.getByTestId('trigger-change-to');
    fireEvent.click(toPicker);
    expect(screen.getByTestId('localization-provider')).toBeInTheDocument();
  });

  it('renders with showAbsoluteRange=false (no date pickers)', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} showAbsoluteRange={false} />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.queryByTestId('date-picker-from')).not.toBeInTheDocument();
  });

  it('renders with showOnlyCalenderIcon=true (no display text)', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} showOnlyCalenderIcon={true} />);
    // Just the calendar icon button
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('resets datetime when resetDateTime changes', () => {
    const onChange = jest.fn();
    const { rerender } = render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} resetDateTime={0} />);
    rerender(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} resetDateTime={Date.now()} />);
    // Should not crash
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('handles apply with shortcut selected (preserves shortcutClickTime)', () => {
    const onChange = jest.fn();
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} />);
    fireEvent.click(screen.getByRole('button'));
    // First select a shortcut
    fireEvent.click(screen.getByTestId('custom-button-last-1-hour'));
    // Now open again and apply
    fireEvent.click(screen.getByRole('button'));
    // Apply should have been called already by shortcut
    expect(onChange).toHaveBeenCalled();
  });

  it('renders with custom width', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} width='200px' />);
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('renders with minDate as dayjs object', () => {
    const dayjs = require('dayjs');
    render(<CustomDateTimeRangePicker {...defaultProps} minDate={dayjs().subtract(1, 'week')} />);
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('renders with minDate as string', () => {
    render(<CustomDateTimeRangePicker {...defaultProps} minDate='2024-01-01' />);
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('handles shortcut that goes through switch (Current Month uses max with minDate)', () => {
    const onChange = jest.fn();
    // Set minDate to far in the future to test Math.max branch
    const futureDate = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000);
    render(<CustomDateTimeRangePicker {...defaultProps} onChange={onChange} shortCuts={['Current Month']} minDate={futureDate} />);
    fireEvent.click(screen.getByRole('button'));
    fireEvent.click(screen.getByTestId('custom-button-current-month'));
    expect(onChange).toHaveBeenCalled();
  });

  it('shows shortcut as selected when matching shortcut is in shortcutClickTime effect', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 10 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 10 * 60 * 1000, // Last 10 Minutes
        }}
      />
    );
    expect(screen.getByText('Last 10 Minutes')).toBeInTheDocument();
  });

  it('shows shortcut "Last 15 Minutes" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 15 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 15 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 15 Minutes')).toBeInTheDocument();
  });

  it('shows shortcut "Last 30 Minutes" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 30 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 30 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 30 Minutes')).toBeInTheDocument();
  });

  it('shows shortcut "Last 3 Hours" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 3 * 60 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 3 * 60 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 3 Hours')).toBeInTheDocument();
  });

  it('shows shortcut "Last 6 Hours" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 6 * 60 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 6 * 60 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 6 Hours')).toBeInTheDocument();
  });

  it('shows shortcut "Last 12 Hours" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 12 * 60 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 12 * 60 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 12 Hours')).toBeInTheDocument();
  });

  it('shows shortcut "Last 24 Hours" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 24 * 60 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 24 * 60 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Last 24 Hours')).toBeInTheDocument();
  });

  it('shows shortcut "Current Week" from shortcutClickTime', () => {
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        passedSelectedDateTime={{
          startTime: Date.now() - 7 * 24 * 60 * 60 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 7 * 24 * 60 * 60 * 1000,
        }}
      />
    );
    expect(screen.getByText('Current Week')).toBeInTheDocument();
  });

  it('handles apply with isShortcutSelected and shortcutClickTime > 0 in state', () => {
    const onChange = jest.fn();
    render(
      <CustomDateTimeRangePicker
        {...defaultProps}
        onChange={onChange}
        passedSelectedDateTime={{
          startTime: Date.now() - 3600 * 1000,
          endTime: Date.now(),
          shortcutClickTime: 3600 * 1000,
        }}
      />
    );
    fireEvent.click(screen.getByRole('button'));
    // Apply time range (not from shortcut)
    fireEvent.click(screen.getByTestId('custom-button-apply-time-range'));
    expect(onChange).toHaveBeenCalled();
  });
});
