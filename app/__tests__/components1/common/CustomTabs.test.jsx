import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomTabs from '@components1/common/CustomTabs';

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

jest.mock('@components1/common/CustomPill', () => ({
  __esModule: true,
  default: ({ value }) => <span data-testid='custom-pill'>{value}</span>,
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick }) => (
    <button onClick={onClick} data-testid='right-button'>
      {text}
    </button>
  ),
}));

jest.mock('@assets', () => ({
  BetaIcon: 'beta-icon.svg',
}));

// CustomTabs.jsx uses options as { tabOptions: [...], fragment: '' }
const buildOptions = (tabArray, fragment = '') => ({
  tabOptions: tabArray,
  fragment,
});

const tabArray = [
  { value: 'overview', text: 'Overview' },
  { value: 'details', text: 'Details' },
  { value: 'settings', text: 'Settings' },
];

describe('CustomTabs (router variant)', () => {
  it('renders all tab options', () => {
    render(<CustomTabs value='overview' onChange={jest.fn()} options={buildOptions(tabArray)} />);
    expect(screen.getByText('Overview')).toBeInTheDocument();
    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.getByText('Settings')).toBeInTheDocument();
  });

  it('calls onChange when a tab is clicked', () => {
    const onChange = jest.fn();
    render(<CustomTabs value='overview' onChange={onChange} options={buildOptions(tabArray)} />);
    fireEvent.click(screen.getByText('Details'));
    expect(onChange).toHaveBeenCalled();
  });

  it('renders with no tabs when tabOptions is empty', () => {
    render(<CustomTabs value='' onChange={jest.fn()} options={{ tabOptions: [] }} />);
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
  });

  it('renders count pill when option has count', () => {
    const optionsWithCount = buildOptions([
      { value: 'issues', text: 'Issues', count: 5 },
      { value: 'events', text: 'Events' },
    ]);
    render(<CustomTabs value='issues' onChange={jest.fn()} options={optionsWithCount} />);
    expect(screen.getByTestId('custom-pill')).toBeInTheDocument();
    expect(screen.getByTestId('custom-pill')).toHaveTextContent('5');
  });

  it('renders beta icon when betaIcon is true', () => {
    const optionsWithBeta = buildOptions([{ value: 'new', text: 'New Feature', betaIcon: true }]);
    render(<CustomTabs value='new' onChange={jest.fn()} options={optionsWithBeta} />);
    expect(screen.getByAltText('Beta icon')).toBeInTheDocument();
  });

  it('disables tab when disabled is true', () => {
    const optionsWithDisabled = buildOptions([
      { value: 'active', text: 'Active' },
      { value: 'locked', text: 'Locked Tab', disabled: true },
    ]);
    render(<CustomTabs value='active' onChange={jest.fn()} options={optionsWithDisabled} />);
    const disabledTab = screen.getByText('Locked Tab').closest('[role="tab"]');
    // MUI Tab renders as <a> with aria-disabled when using Link component
    expect(disabledTab).toHaveAttribute('aria-disabled', 'true');
  });

  it('hides tab when hidden is true', () => {
    const optionsWithHidden = buildOptions([
      { value: 'visible', text: 'Visible Tab' },
      { value: 'hidden', text: 'Hidden Tab', hidden: true },
    ]);
    render(<CustomTabs value='visible' onChange={jest.fn()} options={optionsWithHidden} />);
    expect(screen.queryByText('Hidden Tab')).not.toBeInTheDocument();
  });

  it('renders with default props (no error)', () => {
    const { container } = render(<CustomTabs value='' onChange={jest.fn()} options={{ tabOptions: tabArray }} />);
    expect(container.firstChild).toBeInTheDocument();
  });
});
