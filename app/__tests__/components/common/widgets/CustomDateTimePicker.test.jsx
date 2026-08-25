import { render, screen, fireEvent } from '@testing-library/react';
import CustomDateTimePicker from '@shared/widgets/CustomDateTimePicker';

jest.mock('@mui/x-date-pickers/LocalizationProvider', () => ({
  LocalizationProvider: ({ children }) => <>{children}</>,
}));

jest.mock('@mui/x-date-pickers/AdapterDayjs', () => ({
  AdapterDayjs: class {},
}));

// Calls renderInput so the real custom <input> from the component renders in tests.
// This lets us verify preventDirectInput, onBlur, id, and min/max forwarding.
jest.mock('@mui/x-date-pickers/DateTimePicker', () => ({
  DateTimePicker: ({ value, onChange, disabled, minDate, maxDateTime, renderInput }) => {
    const rendered = renderInput?.({
      inputRef: { current: null },
      inputProps: {
        'data-testid': 'picker-input',
        value: value ?? '',
        onChange: (e) => onChange?.(e.target.value),
        onBlur: undefined,
        onKeyDown: undefined,
        ...(disabled && { disabled: true }),
      },
      InputProps: {},
    });
    return (
      <div
        data-testid='date-time-picker'
        data-min-date={minDate !== undefined ? 'set' : undefined}
        data-max-date-time={maxDateTime !== undefined ? 'set' : undefined}
      >
        {rendered}
      </div>
    );
  },
}));

jest.mock('@utils/colors');

describe('CustomDateTimePicker', () => {
  describe('rendering', () => {
    it('renders the picker', () => {
      render(<CustomDateTimePicker onChange={jest.fn()} />);
      expect(screen.getByTestId('date-time-picker')).toBeInTheDocument();
    });

    it('shows label when provided', () => {
      render(<CustomDateTimePicker label='Start Date' onChange={jest.fn()} />);
      expect(screen.getByText('Start Date')).toBeInTheDocument();
    });

    it('does not show label element when label is not provided', () => {
      const { container } = render(<CustomDateTimePicker onChange={jest.fn()} />);
      expect(container.querySelector('label')).not.toBeInTheDocument();
    });

    it('shows required asterisk when required=true', () => {
      render(<CustomDateTimePicker label='Date' required={true} onChange={jest.fn()} />);
      expect(screen.getByText('*')).toBeInTheDocument();
    });

    it('does not show asterisk when required=false', () => {
      render(<CustomDateTimePicker label='Date' required={false} onChange={jest.fn()} />);
      expect(screen.queryByText('*')).not.toBeInTheDocument();
    });

    it('renders as disabled when disabled=true', () => {
      render(<CustomDateTimePicker disabled={true} onChange={jest.fn()} />);
      expect(screen.getByTestId('picker-input')).toBeDisabled();
    });

    it('applies id to the input element', () => {
      render(<CustomDateTimePicker id='start-date' onChange={jest.fn()} />);
      expect(screen.getByTestId('picker-input')).toHaveAttribute('id', 'start-date');
    });
  });

  describe('value handling', () => {
    it('reflects the value prop in the input', () => {
      render(<CustomDateTimePicker value='2024-06-01T10:00' onChange={jest.fn()} />);
      expect(screen.getByTestId('picker-input')).toHaveValue('2024-06-01T10:00');
    });

    it('shows empty string when value is undefined', () => {
      render(<CustomDateTimePicker onChange={jest.fn()} />);
      expect(screen.getByTestId('picker-input')).toHaveValue('');
    });
  });

  describe('onChange', () => {
    it('calls onChange with the new value when date is changed', () => {
      const onChange = jest.fn();
      render(<CustomDateTimePicker value='' onChange={onChange} />);
      fireEvent.change(screen.getByTestId('picker-input'), { target: { value: '2024-01-15' } });
      expect(onChange).toHaveBeenCalledWith('2024-01-15');
    });

    it('does not crash when onChange is not provided', () => {
      render(<CustomDateTimePicker value='' />);
      expect(() => fireEvent.change(screen.getByTestId('picker-input'), { target: { value: '2024-01-15' } })).not.toThrow();
    });

    it('calls onChange each time the value changes', () => {
      const onChange = jest.fn();
      render(<CustomDateTimePicker value='' onChange={onChange} />);
      const input = screen.getByTestId('picker-input');
      fireEvent.change(input, { target: { value: '2024-01-01' } });
      fireEvent.change(input, { target: { value: '2024-06-15' } });
      expect(onChange).toHaveBeenCalledTimes(2);
      expect(onChange).toHaveBeenNthCalledWith(1, '2024-01-01');
      expect(onChange).toHaveBeenNthCalledWith(2, '2024-06-15');
    });
  });

  describe('onBlur', () => {
    it('calls onBlur prop when input loses focus', () => {
      const onBlur = jest.fn();
      render(<CustomDateTimePicker onBlur={onBlur} onChange={jest.fn()} />);
      fireEvent.blur(screen.getByTestId('picker-input'));
      expect(onBlur).toHaveBeenCalledTimes(1);
    });

    it('does not crash when onBlur is not provided', () => {
      render(<CustomDateTimePicker onChange={jest.fn()} />);
      expect(() => fireEvent.blur(screen.getByTestId('picker-input'))).not.toThrow();
    });
  });

  describe('preventDirectInput', () => {
    it('prevents keyboard input when preventDirectInput=true', () => {
      render(<CustomDateTimePicker preventDirectInput={true} onChange={jest.fn()} />);
      const wasNotPrevented = fireEvent.keyDown(screen.getByTestId('picker-input'), {
        key: 'a',
        cancelable: true,
      });
      expect(wasNotPrevented).toBe(false);
    });

    it('allows keyboard input when preventDirectInput=false (default)', () => {
      render(<CustomDateTimePicker onChange={jest.fn()} />);
      const wasNotPrevented = fireEvent.keyDown(screen.getByTestId('picker-input'), {
        key: 'a',
        cancelable: true,
      });
      expect(wasNotPrevented).toBe(true);
    });

    it('prevents paste when preventDirectInput=true', () => {
      render(<CustomDateTimePicker preventDirectInput={true} onChange={jest.fn()} />);
      const wasNotPrevented = fireEvent.paste(screen.getByTestId('picker-input'), {
        cancelable: true,
      });
      expect(wasNotPrevented).toBe(false);
    });

    it('prevents drop when preventDirectInput=true', () => {
      render(<CustomDateTimePicker preventDirectInput={true} onChange={jest.fn()} />);
      const wasNotPrevented = fireEvent.drop(screen.getByTestId('picker-input'), {
        cancelable: true,
      });
      expect(wasNotPrevented).toBe(false);
    });
  });

  describe('min/max date constraints', () => {
    it('forwards minDate to DateTimePicker', () => {
      render(<CustomDateTimePicker minDate={new Date('2024-01-01')} onChange={jest.fn()} />);
      expect(screen.getByTestId('date-time-picker')).toHaveAttribute('data-min-date', 'set');
    });

    it('forwards maxDateTime to DateTimePicker', () => {
      render(<CustomDateTimePicker maxDateTime={new Date('2024-12-31')} onChange={jest.fn()} />);
      expect(screen.getByTestId('date-time-picker')).toHaveAttribute('data-max-date-time', 'set');
    });

    it('does not set data-min-date when minDate is not provided', () => {
      render(<CustomDateTimePicker onChange={jest.fn()} />);
      expect(screen.getByTestId('date-time-picker')).not.toHaveAttribute('data-min-date');
    });

    it('does not set data-max-date-time when maxDateTime is not provided', () => {
      render(<CustomDateTimePicker onChange={jest.fn()} />);
      expect(screen.getByTestId('date-time-picker')).not.toHaveAttribute('data-max-date-time');
    });
  });

  describe('error and out-of-range state', () => {
    it('shows error message when error string is provided', () => {
      render(<CustomDateTimePicker error='Invalid date' onChange={jest.fn()} />);
      expect(screen.getByText('Invalid date')).toBeInTheDocument();
    });

    it('error message has role=alert', () => {
      render(<CustomDateTimePicker error='Date is before minimum allowed' onChange={jest.fn()} />);
      expect(screen.getByRole('alert')).toHaveTextContent('Date is before minimum allowed');
    });

    it('shows out-of-range error message via role=alert', () => {
      render(<CustomDateTimePicker error='Date is after maximum allowed' onChange={jest.fn()} />);
      expect(screen.getByRole('alert')).toHaveTextContent('Date is after maximum allowed');
    });

    it('shows helperText as error when error=true', () => {
      render(<CustomDateTimePicker error={true} helperText='Date is out of range' onChange={jest.fn()} />);
      expect(screen.getByRole('alert')).toHaveTextContent('Date is out of range');
    });

    it('shows helperText without role=alert when there is no error', () => {
      render(<CustomDateTimePicker helperText='Select a date' error='' onChange={jest.fn()} />);
      expect(screen.getByText('Select a date')).toBeInTheDocument();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('does not show helperText when error is set and errorMessage takes over', () => {
      render(<CustomDateTimePicker error='Bad date' helperText='Pick wisely' onChange={jest.fn()} />);
      // error message is shown, helperText is suppressed when error is truthy
      expect(screen.getByRole('alert')).toHaveTextContent('Bad date');
      expect(screen.queryByText('Pick wisely')).not.toBeInTheDocument();
    });

    it('shows no alert when error is empty string (default)', () => {
      render(<CustomDateTimePicker error='' onChange={jest.fn()} />);
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
  });
});
