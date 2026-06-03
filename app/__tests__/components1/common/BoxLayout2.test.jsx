import React from 'react';
import { render, screen } from '@testing-library/react';
import BoxLayout2 from '@components1/common/BoxLayout2';

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
      primaryLight: '#DBEAFE',
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

jest.mock('@components1/common/TextWithBorder', () => ({
  __esModule: true,
  default: ({ value }) => <div data-testid='text-with-border'>{value}</div>,
}));

jest.mock('@components1/common/FilterDropdownButton', () => ({
  __esModule: true,
  default: ({ label }) => <div data-testid={`filter-btn-${label}`}>{label}</div>,
  MoreFiltersButton: ({ count, onClick }) => (
    <button data-testid='more-filters-btn' onClick={onClick}>
      +{count} more
    </button>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, id }) => (
    <button id={id} onClick={onClick} data-testid={`custom-btn-${text}`}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/CopyButton', () => ({
  __esModule: true,
  default: ({ onClick }) => (
    <button onClick={onClick} data-testid='copy-button'>
      Copy
    </button>
  ),
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

jest.mock('@components1/common/CustomSearch', () => ({
  __esModule: true,
  default: ({ label }) => <input aria-label={label} data-testid='custom-search' />,
}));

jest.mock('@components1/common/CustomButtonsGroup', () => ({
  __esModule: true,
  default: ({ options }) => <div data-testid='buttons-group'>{options?.length} buttons</div>,
}));

jest.mock('@components1/common/CustomSwitch', () => ({
  __esModule: true,
  default: ({ onChange, checked, id }) => <input type='checkbox' id={id} checked={checked} onChange={onChange} data-testid='custom-switch' />,
}));

jest.mock('@components1/common/CustomTableFilters', () => ({
  __esModule: true,
  default: () => <div data-testid='custom-table-filters' />,
}));

jest.mock('@components1/CustomIconButton', () => ({
  __esModule: true,
  default: ({ children, onClick }) => (
    <button onClick={onClick} data-testid='icon-button'>
      {children}
    </button>
  ),
}));

jest.mock('@components1/common/CustomDivider', () => ({
  __esModule: true,
  default: () => <hr />,
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
  AlphaIcon: 'alpha-icon.svg',
}));

describe('BoxLayout2', () => {
  it('renders without crashing', () => {
    const { container } = render(<BoxLayout2 id='test-layout' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders children content', () => {
    render(
      <BoxLayout2 id='test-layout'>
        <div data-testid='child-content'>Hello World</div>
      </BoxLayout2>
    );
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders heading when provided', () => {
    render(<BoxLayout2 id='test-layout' heading='My Heading' />);
    expect(screen.getByTestId('text-with-border')).toHaveTextContent('My Heading');
  });

  it('renders copy button when copyingOption is enabled', () => {
    render(<BoxLayout2 id='test-layout' copyingOption={{ enabled: true, onClick: jest.fn() }} />);
    expect(screen.getByTestId('copy-button')).toBeInTheDocument();
  });

  it('does not render copy button when copyingOption is disabled', () => {
    render(<BoxLayout2 id='test-layout' copyingOption={{ enabled: false, onClick: null }} />);
    expect(screen.queryByTestId('copy-button')).not.toBeInTheDocument();
  });

  it('renders share button when sharingOptions.sharing.enabled is true', () => {
    render(
      <BoxLayout2
        id='test-layout'
        sharingOptions={{
          sharing: { enabled: true, onClick: jest.fn() },
          download: { enabled: false, onClick: jest.fn() },
        }}
      />
    );
    expect(screen.getByTestId('share-button')).toBeInTheDocument();
  });

  it('renders download button when sharingOptions.download.enabled is true', () => {
    render(
      <BoxLayout2
        id='test-layout'
        sharingOptions={{
          sharing: { enabled: false, onClick: null },
          download: { enabled: true, onClick: jest.fn() },
        }}
      />
    );
    expect(screen.getByTestId('download-button')).toBeInTheDocument();
  });

  it('renders modal button when modalButton.enabled is true', () => {
    render(<BoxLayout2 id='test-layout' modalButton={{ enabled: true, text: 'Open Modal', onClick: jest.fn(), id: 'modal-btn' }} />);
    expect(screen.getByTestId('custom-btn-Open Modal')).toBeInTheDocument();
  });

  it('renders search toggle button when searchOption.enabled is true', () => {
    render(
      <BoxLayout2
        id='test-layout'
        searchOption={{
          enabled: true,
          placeholder: 'Search...',
          value: '',
          onChange: jest.fn(),
          onClear: jest.fn(),
          onEnter: jest.fn(),
        }}
      />
    );
    // The search toggle icon button should be present
    expect(screen.getByRole('button', { name: '' })).toBeInTheDocument();
  });

  it('renders the "More filters" button when more than 3 filters are defined', () => {
    const filterOptions = [
      { type: 'dropdown', label: 'Filter 1', options: [], value: null, onSelect: jest.fn() },
      { type: 'dropdown', label: 'Filter 2', options: [], value: null, onSelect: jest.fn() },
      { type: 'dropdown', label: 'Filter 3', options: [], value: null, onSelect: jest.fn() },
      { type: 'dropdown', label: 'Filter 4', options: [], value: null, onSelect: jest.fn() },
    ];
    render(<BoxLayout2 id='test-layout' filterOptions={filterOptions} />);
    expect(screen.getByTestId('more-filters-btn')).toBeInTheDocument();
  });
});
