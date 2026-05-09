import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomCheckBox from '@components1/common/CustomCheckbox';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
    },
    background: { primaryLightest: '#EFF6FF', buttonTab: '#EFF6FF', white: '#fff', transparent: 'transparent', switchTrackDark: '#3B82F6' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', success: '#22C55E', buttonTab: '#3B82F6', primaryLight: '#60A5FA' },
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
    primary: '#3B82F6',
  },
}));

describe('CustomCheckBox', () => {
  it('renders checkbox without label when no text, startElement, or endElement', () => {
    render(<CustomCheckBox checked={false} onChange={jest.fn()} />);
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
    expect(screen.queryByRole('label')).not.toBeInTheDocument();
  });

  it('renders checkbox with text label', () => {
    render(<CustomCheckBox text='Accept Terms' checked={false} onChange={jest.fn()} />);
    expect(screen.getByRole('checkbox')).toBeInTheDocument();
    expect(screen.getByText('Accept Terms')).toBeInTheDocument();
  });

  it('checkbox is checked when checked is true', () => {
    render(<CustomCheckBox checked={true} onChange={jest.fn()} />);
    expect(screen.getByRole('checkbox')).toBeChecked();
  });

  it('calls onChange when clicked', () => {
    const onChange = jest.fn();
    render(<CustomCheckBox checked={false} onChange={onChange} />);
    fireEvent.click(screen.getByRole('checkbox'));
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it('is disabled when disabled is true', () => {
    render(<CustomCheckBox checked={false} onChange={jest.fn()} disabled={true} />);
    expect(screen.getByRole('checkbox')).toBeDisabled();
  });

  it('renders with startElement', () => {
    const startEl = <span data-testid='start-el'>Start</span>;
    render(<CustomCheckBox text='Label' checked={false} onChange={jest.fn()} startElement={startEl} />);
    expect(screen.getByTestId('start-el')).toBeInTheDocument();
    expect(screen.getByText('Label')).toBeInTheDocument();
  });

  it('renders with endElement', () => {
    const endEl = <span data-testid='end-el'>End</span>;
    render(<CustomCheckBox text='Label' checked={false} onChange={jest.fn()} endElement={endEl} />);
    expect(screen.getByTestId('end-el')).toBeInTheDocument();
    expect(screen.getByText('Label')).toBeInTheDocument();
  });

  it('renders with indeterminate state', () => {
    render(<CustomCheckBox checked={false} onChange={jest.fn()} indeterminate={true} />);
    // MUI renders indeterminate checkbox as a checkbox role with data-indeterminate attribute
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).toBeInTheDocument();
  });
});
