import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomTableFilters from '@components1/common/CustomTableFilters';

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

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

jest.mock('@components1/common/CustomSearch', () => ({
  __esModule: true,
  default: ({ label, onChange, value }) => (
    <input aria-label={label || 'search'} value={value || ''} onChange={(e) => onChange?.(e.target.value)} data-testid='custom-search' />
  ),
}));

jest.mock('@components1/common/CustomButtonsGroup', () => ({
  __esModule: true,
  default: ({ options }) => (
    <div data-testid='buttons-group'>
      {options?.map((opt) => (
        <button key={opt.value}>{opt.label}</button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/CustomSwitch', () => ({
  __esModule: true,
  default: ({ onChange, checked, id }) => <input type='checkbox' id={id} checked={!!checked} onChange={onChange} data-testid='custom-switch' />,
}));

jest.mock('@components1/common/CustomDivider', () => ({
  __esModule: true,
  default: () => <hr />,
}));

jest.mock('@components1/common/ShareButton', () => ({
  __esModule: true,
  default: ({ onClick }) => (
    <button onClick={onClick} data-testid='share-button'>
      Share
    </button>
  ),
}));

jest.mock('@components1/common/DownloadButton', () => ({
  __esModule: true,
  default: ({ onClick }) => (
    <button onClick={onClick} data-testid='download-button'>
      Download
    </button>
  ),
}));

jest.mock('@components1/common/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: () => <div data-testid='date-time-picker' />,
}));

jest.mock('@data/themes/inputField', () => ({
  inputSx: {},
  inputCustomSx: {},
}));

jest.mock('@data/constants', () => ({
  TIME_PICK_SHORTCUTS: [],
}));

jest.mock('@assets', () => ({
  FilterIcon: 'filter-icon.svg',
}));

jest.mock('@components1/common/format/Text', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

const defaultProps = {
  filterOptions: [],
  showBorder: false,
  sharingOptions: {
    sharing: { enabled: false, onClick: null },
    download: { enabled: false, onClick: null },
  },
  dateTimeRange: {
    enabled: false,
    onChange: jest.fn(),
    passedSelectedDateTime: { startTime: Date.now() - 3600000, endTime: Date.now() },
  },
  handleSelectDates: jest.fn(),
  onClearAll: jest.fn(),
  expandedAccordions: {},
  setExpandedAccordions: jest.fn(),
};

describe('CustomTableFilters', () => {
  it('renders without crashing', () => {
    const { container } = render(<CustomTableFilters {...defaultProps} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders "Filters" label with filter icon', () => {
    render(<CustomTableFilters {...defaultProps} />);
    expect(screen.getByText('Filters')).toBeInTheDocument();
    expect(screen.getByAltText('filter')).toBeInTheDocument();
  });

  it('renders "Clear All" button and calls onClearAll when clicked', () => {
    const onClearAll = jest.fn();
    render(<CustomTableFilters {...defaultProps} onClearAll={onClearAll} />);
    fireEvent.click(screen.getByText('Clear All'));
    expect(onClearAll).toHaveBeenCalledTimes(1);
  });

  it('renders accordion for each filter option', () => {
    const filterOptions = [
      {
        type: 'dropdown',
        label: 'Environment',
        options: [{ label: 'Production', value: 'prod' }],
        value: null,
        onSelect: jest.fn(),
      },
    ];
    render(<CustomTableFilters {...defaultProps} filterOptions={filterOptions} expandedAccordions={{ 0: true }} />);
    expect(screen.getByText('Environment')).toBeInTheDocument();
  });

  it('renders share button when sharingOptions.sharing.enabled is true', () => {
    render(
      <CustomTableFilters
        {...defaultProps}
        sharingOptions={{
          sharing: { enabled: true, onClick: jest.fn() },
          download: { enabled: false, onClick: null },
        }}
      />
    );
    expect(screen.getByTestId('share-button')).toBeInTheDocument();
  });

  it('renders download button when sharingOptions.download.enabled is true', () => {
    render(
      <CustomTableFilters
        {...defaultProps}
        sharingOptions={{
          sharing: { enabled: false, onClick: null },
          download: { enabled: true, onClick: jest.fn() },
        }}
      />
    );
    expect(screen.getByTestId('download-button')).toBeInTheDocument();
  });

  it('renders date time picker when dateTimeRange.enabled is true', () => {
    render(
      <CustomTableFilters
        {...defaultProps}
        dateTimeRange={{
          enabled: true,
          onChange: jest.fn(),
          passedSelectedDateTime: { startTime: Date.now() - 3600000, endTime: Date.now() },
        }}
      />
    );
    expect(screen.getByTestId('date-time-picker')).toBeInTheDocument();
  });

  it('toggles accordion expanded state when clicked', async () => {
    const setExpandedAccordions = jest.fn();
    const filterOptions = [
      {
        type: 'search',
        label: 'Name',
        options: [],
        value: '',
        onSelect: jest.fn(),
      },
    ];
    render(
      <CustomTableFilters
        {...defaultProps}
        filterOptions={filterOptions}
        expandedAccordions={{ 0: false }}
        setExpandedAccordions={setExpandedAccordions}
      />
    );
    // Click the accordion header
    fireEvent.click(screen.getByText('Name'));
    expect(setExpandedAccordions).toHaveBeenCalled();
  });
});
