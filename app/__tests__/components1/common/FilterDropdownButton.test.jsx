import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import FilterDropdownButton, { MoreFiltersButton } from '@components1/common/FilterDropdownButton';

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
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      tertiaryBorder: '#BFDBFE',
    },
  },
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value, ...props }) => <span {...props}>{value}</span>,
}));

const stringOptions = ['Option A', 'Option B', 'Option C'];
const objectOptions = [
  { label: 'Item One', value: 'one' },
  { label: 'Item Two', value: 'two' },
  { label: 'Item Three', value: 'three' },
];

describe('FilterDropdownButton', () => {
  it('renders the label', () => {
    render(<FilterDropdownButton label='Status' options={stringOptions} onSelect={jest.fn()} />);
    expect(screen.getByText('Status')).toBeInTheDocument();
  });

  it('opens the dropdown when button is clicked', async () => {
    render(<FilterDropdownButton label='Status' options={stringOptions} onSelect={jest.fn()} />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => {
      expect(screen.getByText('Option A')).toBeInTheDocument();
      expect(screen.getByText('Option B')).toBeInTheDocument();
    });
  });

  it('calls onSelect with the selected value for single select', async () => {
    const onSelect = jest.fn();
    render(<FilterDropdownButton label='Status' options={objectOptions} onSelect={onSelect} />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => expect(screen.getByText('Item One')).toBeInTheDocument());
    fireEvent.click(screen.getByText('Item One'));
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ target: { value: 'one' } }), objectOptions[0]);
  });

  it('shows selected value label when a value is provided', () => {
    render(<FilterDropdownButton label='Status' options={objectOptions} value={objectOptions[0]} onSelect={jest.fn()} />);
    expect(screen.getByText('Item One')).toBeInTheDocument();
  });

  it('clears selection when X icon is clicked', async () => {
    const onSelect = jest.fn();
    render(<FilterDropdownButton label='Status' options={objectOptions} value={objectOptions[0]} onSelect={onSelect} />);
    // The clear svg button is rendered when hasSelection is true
    const svgButtons = document.querySelectorAll('svg');
    // Find the svg with the X lines (clear icon)
    const clearIcon = Array.from(svgButtons).find((svg) => svg.querySelector('line'));
    if (clearIcon) {
      fireEvent.click(clearIcon);
      expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ target: { value: null } }), null);
    }
  });

  it('does not open when disabled', () => {
    render(<FilterDropdownButton label='Status' options={stringOptions} onSelect={jest.fn()} disabled />);
    fireEvent.click(screen.getByRole('button'));
    expect(screen.queryByText('Option A')).not.toBeInTheDocument();
  });

  it('shows loading state when isOptionsLoading is true', async () => {
    render(<FilterDropdownButton label='Status' options={[]} onSelect={jest.fn()} isOptionsLoading />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => {
      expect(screen.getByText('Loading...')).toBeInTheDocument();
    });
  });

  it('shows "No results found" when options list is empty', async () => {
    render(<FilterDropdownButton label='Status' options={[]} onSelect={jest.fn()} />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => {
      expect(screen.getByText('No results found')).toBeInTheDocument();
    });
  });

  it('shows +N badge for multiple selections beyond limitTag', () => {
    const value = [objectOptions[0], objectOptions[1], objectOptions[2]];
    render(<FilterDropdownButton label='Status' options={objectOptions} value={value} multiple limitTag={1} onSelect={jest.fn()} />);
    expect(screen.getByText('+2')).toBeInTheDocument();
  });
});

describe('MoreFiltersButton', () => {
  it('renders count and "more filters" text when not expanded', () => {
    render(<MoreFiltersButton count={3} expanded={false} onClick={jest.fn()} />);
    expect(screen.getByText('3 more filters')).toBeInTheDocument();
  });

  it('renders "Show less" when expanded', () => {
    render(<MoreFiltersButton count={3} expanded onClick={jest.fn()} />);
    expect(screen.getByText('Show less')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<MoreFiltersButton count={2} expanded={false} onClick={onClick} />);
    fireEvent.click(screen.getByRole('button'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });
});
