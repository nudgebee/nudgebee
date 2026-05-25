import React from 'react';
import { render, screen } from '@testing-library/react';
import AnchorComponent from '@components1/common/AnchorComponent';

// Mock next/router
jest.mock('next/router', () => ({
  useRouter: () => ({
    asPath: '/',
    push: jest.fn(),
    replace: jest.fn(),
  }),
}));

// Mock next/link
jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ children, href }) => <a href={href}>{children}</a>,
}));

// Mock heavy sub-components
jest.mock('@components1/CustomIconButton', () => ({
  __esModule: true,
  default: ({ children, onClick }) => <button onClick={onClick}>{children}</button>,
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <img alt={alt} />,
}));

jest.mock('@components1/common/CustomPill', () => ({
  __esModule: true,
  default: ({ value }) => <span>{value}</span>,
}));

jest.mock('@components1/common/CustomTabs', () => ({
  __esModule: true,
  default: ({ value: _value, onChange, options: _options }) => (
    <div data-testid='custom-tabs'>
      <button onClick={() => onChange(1)}>Tab</button>
    </div>
  ),
}));

jest.mock('@assets', () => ({
  BetaIcon: '/beta.svg',
  MenuArrowDownIcon: '/arrow.svg',
}));

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
      disabled: '#9CA3AF',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
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
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

describe('AnchorComponent', () => {
  beforeEach(() => {
    // Ensure window.location.hash is empty for most tests
    delete window.location;
    window.location = { hash: '', pathname: '/', search: '', href: 'http://localhost/' };
  });

  test('renders without crashing with empty filterOptions', () => {
    const { container } = render(<AnchorComponent filterOptions={[]} />);
    expect(container).toBeTruthy();
  });

  test('renders filter option buttons for each option in filterOptions', () => {
    const filterOptions = [
      { name: 'Overview', value: 0, id: 'overview', fragment: 'overview' },
      { name: 'Details', value: 1, id: 'details', fragment: 'details' },
      { name: 'Settings', value: 2, id: 'settings', fragment: 'settings' },
    ];

    render(<AnchorComponent filterOptions={filterOptions} />);

    expect(screen.getByText('Overview')).toBeInTheDocument();
    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.getByText('Settings')).toBeInTheDocument();
  });

  test('renders button title when buttonTitle prop provided', () => {
    render(<AnchorComponent filterOptions={[]} buttonTitle='Add New' handleButtonAction={jest.fn()} />);

    expect(screen.getByText('Add New')).toBeInTheDocument();
  });

  test('renders buttonComponent when provided', () => {
    const ButtonComp = <button data-testid='custom-btn'>Custom Action</button>;

    render(<AnchorComponent filterOptions={[]} buttonComponent={ButtonComp} />);

    expect(screen.getByTestId('custom-btn')).toBeInTheDocument();
    expect(screen.getByText('Custom Action')).toBeInTheDocument();
  });

  test('calls onChangeFilter on mount with default values', () => {
    const onChangeFilter = jest.fn();
    const filterOptions = [{ name: 'Tab1', value: 0, id: 'tab1', fragment: 'tab1' }];

    render(<AnchorComponent filterOptions={filterOptions} onChangeFilter={onChangeFilter} />);

    expect(onChangeFilter).toHaveBeenCalledWith(0, 0, { tab: 0, subtab: 0 });
  });

  test('renders secondary tab bar (CustomTabs) when active tab has tabOptions', () => {
    const filterOptions = [
      {
        name: 'Tab1',
        value: 0,
        id: 'tab1',
        fragment: 'tab1',
        tabOptions: [
          { name: 'SubTab A', value: 0, fragment: 'sub-a' },
          { name: 'SubTab B', value: 1, fragment: 'sub-b' },
        ],
      },
    ];

    render(<AnchorComponent filterOptions={filterOptions} />);

    expect(screen.getByTestId('custom-tabs')).toBeInTheDocument();
  });

  test('renders options scroll bar when active tab has options array', () => {
    const filterOptions = [
      {
        name: 'Tab1',
        value: 0,
        id: 'tab1',
        fragment: 'tab1',
        options: [
          { name: 'Section A', id: 'section-a' },
          { name: 'Section B', id: 'section-b' },
        ],
      },
    ];

    render(<AnchorComponent filterOptions={filterOptions} />);

    expect(screen.getByText('Section A')).toBeInTheDocument();
    expect(screen.getByText('Section B')).toBeInTheDocument();
  });

  test('does not show popover when no tab is hovered', () => {
    const filterOptions = [{ name: 'Overview', value: 0, id: 'overview', fragment: 'overview' }];

    render(<AnchorComponent filterOptions={filterOptions} />);

    // Popover with id 'mouse-over-popover' should not be visible
    expect(screen.queryByRole('presentation')).not.toBeInTheDocument();
  });
});
