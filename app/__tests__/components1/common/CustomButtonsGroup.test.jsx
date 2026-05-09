import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomButtonsGroup from '@components1/common/CustomButtonsGroup';

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

const options = [
  { text: 'Option A', value: 'a' },
  { text: 'Option B', value: 'b' },
  { text: 'Option C', value: 'c', disabled: true },
];

describe('CustomButtonsGroup', () => {
  it('renders all options as buttons', () => {
    render(<CustomButtonsGroup options={options} selected='a' onClick={() => {}} />);
    expect(screen.getByText('Option A')).toBeInTheDocument();
    expect(screen.getByText('Option B')).toBeInTheDocument();
    expect(screen.getByText('Option C')).toBeInTheDocument();
  });

  it('selected button has "selected" class', () => {
    render(<CustomButtonsGroup options={options} selected='b' onClick={() => {}} />);
    const buttonB = screen.getByText('Option B').closest('button');
    expect(buttonB).toHaveClass('selected');
  });

  it('calls onClick with option object when button clicked', () => {
    const onClick = jest.fn();
    render(<CustomButtonsGroup options={options} selected='a' onClick={onClick} />);
    fireEvent.click(screen.getByText('Option B'));
    expect(onClick).toHaveBeenCalledWith({ text: 'Option B', value: 'b' });
  });

  it('disabled option is disabled', () => {
    render(<CustomButtonsGroup options={options} selected='a' onClick={() => {}} />);
    const buttonC = screen.getByText('Option C').closest('button');
    expect(buttonC).toBeDisabled();
  });

  it('renders empty when no options', () => {
    render(<CustomButtonsGroup options={[]} selected='' onClick={() => {}} />);
    const group = screen.getByRole('group');
    expect(group).toBeEmptyDOMElement();
  });

  it('renders with tabType prop', () => {
    render(<CustomButtonsGroup options={options} selected='a' onClick={() => {}} tabType={true} />);
    expect(screen.getByText('Option A')).toBeInTheDocument();
  });
});
