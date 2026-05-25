import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import CustomDropdown from '@components1/common/CustomDropdown';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    clusterIndicator: '#10B981',
    error: '#EF4444',
    iconColor: '#6B7280',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
      infoGraphic: '#F8FAFC',
      error: '#EF4444',
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
      tertiaryBorder: '#BFDBFE',
    },
  },
}));

jest.mock('@data/themes/inputField', () => ({
  inputSx: {},
  inputCustomSx: {},
}));

jest.mock('@components1/common/ClusterStatusIndicator', () => ({
  __esModule: true,
  default: () => <span data-testid='cluster-indicator' />,
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/CloudIcon', () => ({
  __esModule: true,
  default: () => <span data-testid='cloud-icon' />,
}));

jest.mock('src/utils/common', () => ({
  toKebabCase: (s) => s.toLowerCase().replace(/\s+/g, '-'),
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

jest.mock('@assets', () => ({
  MenuArrowDownIcon: '/arrow.svg',
}));

const sampleOptions = [
  { label: 'Option A', value: 'a' },
  { label: 'Option B', value: 'b' },
  { label: 'Option C', value: 'c' },
];

describe('CustomDropdown', () => {
  test('renders without crashing', () => {
    const { container } = render(<CustomDropdown options={sampleOptions} />);
    expect(container).toBeTruthy();
  });

  test('renders with label prop', () => {
    render(<CustomDropdown options={sampleOptions} label='Select item' />);
    expect(screen.getByLabelText('Select item')).toBeInTheDocument();
  });

  test('shows options in dropdown (open it and check)', async () => {
    render(<CustomDropdown options={sampleOptions} label='My Dropdown' />);
    const input = screen.getByRole('combobox');
    fireEvent.mouseDown(input);
    await waitFor(() => {
      expect(screen.getByText('Option A')).toBeInTheDocument();
    });
  });

  test('calls onChange when option selected', async () => {
    const onChange = jest.fn();
    render(<CustomDropdown options={sampleOptions} label='Select' onChange={onChange} />);
    const input = screen.getByRole('combobox');
    fireEvent.mouseDown(input);
    await waitFor(() => {
      expect(screen.getByText('Option A')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Option A'));
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  test('is disabled when isDisabled=true', () => {
    render(<CustomDropdown options={sampleOptions} isDisabled={true} />);
    const input = screen.getByRole('combobox');
    expect(input).toBeDisabled();
  });

  test('shows loading spinner when isLoading=true', () => {
    render(<CustomDropdown options={sampleOptions} isLoading={true} />);
    // CircularProgress renders with role="progressbar"
    expect(screen.getByRole('progressbar')).toBeInTheDocument();
  });

  test('renders with noOptionsText when no options', async () => {
    render(<CustomDropdown options={[]} label='Empty' noOptionsText='Nothing here' />);
    const input = screen.getByRole('combobox');
    fireEvent.mouseDown(input);
    await waitFor(() => {
      expect(screen.getByText('Nothing here')).toBeInTheDocument();
    });
  });
});
