import React from 'react';
import { render, screen } from '@testing-library/react';
import FilterGroup from '@components1/common/FilterGroup';

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

jest.mock('@components1/common/CustomSearch', () => ({
  __esModule: true,
  default: ({ label, onChange }: any) => <input aria-label={label} onChange={(e) => onChange(e.target.value)} data-testid='custom-search' />,
}));

jest.mock('@components1/common/CustomButtonsGroup', () => ({
  __esModule: true,
  default: ({ options, onClick }: any) => (
    <div data-testid='custom-buttons-group'>
      {options?.map((opt: any) => (
        <button key={opt.value} onClick={() => onClick({ target: { value: opt.value } })}>
          {opt.label || opt.text}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: ({ onChange }: any) => (
    <button data-testid='date-time-picker' onClick={() => onChange({ selection: {} })}>
      Date Picker
    </button>
  ),
}));

jest.mock('@data/themes/inputField', () => ({
  inputSx: {},
  inputCustomSx: {},
}));

describe('FilterGroup', () => {
  it('renders without crashing with default props', () => {
    const { container } = render(<FilterGroup />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('shows "Last 24 hours" text', () => {
    render(<FilterGroup />);
    expect(screen.getByText('Last 24 hours')).toBeInTheDocument();
  });

  it('renders dropdown filter option', () => {
    const filterOptions = [
      {
        id: 'env',
        type: 'dropdown',
        label: 'Environment',
        options: [{ label: 'Production', value: 'prod' }],
        value: null,
        onSelect: jest.fn(),
      },
    ];
    render(<FilterGroup filterOptions={filterOptions} />);
    expect(screen.getByLabelText('Environment')).toBeInTheDocument();
  });

  it('renders search filter option', () => {
    const filterOptions = [
      {
        id: 'search-1',
        type: 'search',
        label: 'Search',
        onSelect: jest.fn(),
      },
    ];
    render(<FilterGroup filterOptions={filterOptions} />);
    expect(screen.getByTestId('custom-search')).toBeInTheDocument();
  });

  it('renders buttons filter option', () => {
    const onSelect = jest.fn();
    const filterOptions = [
      {
        id: 'btn-group',
        type: 'buttons',
        options: [
          { label: 'All', value: 'all' },
          { label: 'Active', value: 'active' },
        ],
        selected: 'all',
        onSelect,
      },
    ];
    render(<FilterGroup filterOptions={filterOptions} />);
    expect(screen.getByTestId('custom-buttons-group')).toBeInTheDocument();
  });

  it('renders custom component type', () => {
    const filterOptions = [
      {
        id: 'custom-1',
        type: 'custom',
        component: <div data-testid='custom-component'>Custom</div>,
      },
    ];
    render(<FilterGroup filterOptions={filterOptions} />);
    expect(screen.getByTestId('custom-component')).toBeInTheDocument();
  });

  it('renders date time picker when dateTimeRange.enabled is true', () => {
    const dateTimeRange = {
      enabled: true,
      onChange: jest.fn(),
      passedSelectedDateTime: {
        startTime: Date.now() - 3600 * 1000,
        endTime: Date.now(),
      },
    };
    render(<FilterGroup dateTimeRange={dateTimeRange} />);
    expect(screen.getByTestId('date-time-picker')).toBeInTheDocument();
  });

  it('does not render date time picker when dateTimeRange.enabled is false', () => {
    const dateTimeRange = {
      enabled: false,
      onChange: jest.fn(),
      passedSelectedDateTime: {
        startTime: Date.now() - 3600 * 1000,
        endTime: Date.now(),
      },
    };
    render(<FilterGroup dateTimeRange={dateTimeRange} />);
    expect(screen.queryByTestId('date-time-picker')).not.toBeInTheDocument();
  });

  it('skips filter options with enabled: false', () => {
    const filterOptions = [
      {
        id: 'hidden',
        type: 'search',
        label: 'Hidden Search',
        enabled: false,
        onSelect: jest.fn(),
      },
    ];
    render(<FilterGroup filterOptions={filterOptions} />);
    expect(screen.queryByTestId('custom-search')).not.toBeInTheDocument();
  });
});
