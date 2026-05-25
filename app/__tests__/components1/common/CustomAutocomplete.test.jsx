import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import CustomAutocomplete from '@components1/common/CustomAutocomplete';

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
      primary: '#3B82F6',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
      tertiary: '#9CA3AF',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
    },
  },
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => <div title={String(title)}>{children}</div>,
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@assets', () => ({
  MenuArrowDownIcon: 'menu-arrow-down.svg',
}));

jest.mock('@data/themes/inputField', () => ({
  inputSx: {},
  inputCustomSx: {},
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: (s) => s,
  toKebabCase: (s) => s?.toLowerCase().replace(/\s+/g, '-') || '',
}));

const stringOptions = ['Apple', 'Banana', 'Cherry'];
const objectOptions = [
  { label: 'Option One', value: 'one' },
  { label: 'Option Two', value: 'two' },
  { label: 'Option Three', value: 'three' },
];

describe('CustomAutocomplete', () => {
  it('renders with label', () => {
    render(<CustomAutocomplete label='Select Fruit' options={stringOptions} onSelect={jest.fn()} />);
    expect(screen.getByLabelText('Select Fruit')).toBeInTheDocument();
  });

  it('renders in disabled state when disabled prop is true', () => {
    render(<CustomAutocomplete label='Select Item' options={objectOptions} value={null} disabled onSelect={jest.fn()} />);
    const input = screen.getByRole('combobox');
    expect(input).toBeDisabled();
  });

  it('renders the selected value', () => {
    render(<CustomAutocomplete label='Select Item' options={objectOptions} value={objectOptions[0]} onSelect={jest.fn()} />);
    expect(screen.getByDisplayValue('Option One')).toBeInTheDocument();
  });

  it('shows loading indicator when isOptionsLoading is true', () => {
    render(<CustomAutocomplete label='Loading' options={[]} value={null} isOptionsLoading onSelect={jest.fn()} />);
    // CircularProgress is rendered for loading state
    expect(document.querySelector('[role="progressbar"]')).toBeInTheDocument();
  });

  it('opens dropdown and shows options when clicked', async () => {
    render(<CustomAutocomplete label='Select Fruit' options={stringOptions} value={null} onSelect={jest.fn()} />);
    const input = screen.getByRole('combobox');
    fireEvent.mouseDown(input);
    await waitFor(() => {
      expect(screen.getByText('Apple')).toBeInTheDocument();
    });
  });

  it('shows noOptionsText when options is empty', async () => {
    render(<CustomAutocomplete label='No Options' options={[]} value={null} noOptionsText='Nothing here' onSelect={jest.fn()} />);
    const input = screen.getByRole('combobox');
    fireEvent.mouseDown(input);
    await waitFor(() => {
      expect(screen.getByText('Nothing here')).toBeInTheDocument();
    });
  });

  it('renders with isRequired flag and marks input as required', () => {
    render(<CustomAutocomplete label='Required Field' options={stringOptions} value={null} isRequired onSelect={jest.fn()} />);
    const input = screen.getByRole('combobox');
    expect(input).toBeRequired();
  });
});
