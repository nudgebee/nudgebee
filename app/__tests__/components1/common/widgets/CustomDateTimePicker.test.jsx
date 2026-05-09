import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomDateTimePicker from '@components1/common/widgets/CustomDateTimePicker';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { tertiary: '#6B7280' },
    border: { secondary: '#D1D5DB' },
  },
}));

jest.mock('@mui/x-date-pickers/LocalizationProvider', () => ({
  LocalizationProvider: ({ children }) => <div data-testid='localization-provider'>{children}</div>,
}));

jest.mock('@mui/x-date-pickers/AdapterDayjs', () => ({
  AdapterDayjs: jest.fn(),
}));

jest.mock('@mui/x-date-pickers/DateTimePicker', () => ({
  DateTimePicker: ({ onChange, renderInput, value: _value }) => (
    <div data-testid='date-time-picker'>
      {renderInput?.({ inputProps: {}, InputProps: {} })}
      <button data-testid='trigger-change' onClick={() => onChange?.({ valueOf: () => 1234567890000 })}>
        Change
      </button>
    </div>
  ),
}));

describe('CustomDateTimePicker', () => {
  it('renders without crashing', () => {
    render(<CustomDateTimePicker label='Start Date' value={null} onChange={jest.fn()} />);
    expect(screen.getByTestId('localization-provider')).toBeInTheDocument();
  });

  it('renders label text', () => {
    render(<CustomDateTimePicker label='Start Date' value={null} onChange={jest.fn()} />);
    expect(screen.getByText('Start Date')).toBeInTheDocument();
  });

  it('calls onChange when value changes', () => {
    const handleChange = jest.fn();
    render(<CustomDateTimePicker label='End Date' value={null} onChange={handleChange} />);
    const trigger = screen.getByTestId('trigger-change');
    trigger.click();
    expect(handleChange).toHaveBeenCalledWith({ valueOf: expect.any(Function) });
  });

  it('renders with custom views and format', () => {
    render(<CustomDateTimePicker label='Custom' value={null} onChange={jest.fn()} views={['day', 'hours']} format='MM/DD/YYYY' />);
    expect(screen.getByTestId('date-time-picker')).toBeInTheDocument();
  });

  it('handles onChange when no onChange prop provided (null onChange)', () => {
    // Render without onChange to cover the if (onChange) branch being false
    render(<CustomDateTimePicker label='No onChange' value={null} />);
    const trigger = screen.getByTestId('trigger-change');
    // Should not throw
    expect(() => trigger.click()).not.toThrow();
  });

  it('renders DateTimePicker with default views', () => {
    render(<CustomDateTimePicker label='Default Views' value={null} onChange={jest.fn()} />);
    expect(screen.getByTestId('date-time-picker')).toBeInTheDocument();
  });
});
