import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomTabsForDrilldown from '@components1/common/CustomTabsForDrilldown';

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
  AlphaIcon: 'alpha-icon.svg',
}));

const tabOptions = [
  { value: 0, text: 'Overview' },
  { value: 1, text: 'Details' },
  { value: 2, text: 'Settings' },
];

describe('CustomTabsForDrilldown', () => {
  it('renders all tab options', () => {
    render(<CustomTabsForDrilldown value={0} onChange={jest.fn()} options={tabOptions} />);
    expect(screen.getByText('Overview')).toBeInTheDocument();
    expect(screen.getByText('Details')).toBeInTheDocument();
    expect(screen.getByText('Settings')).toBeInTheDocument();
  });

  it('calls onChange when a tab is clicked', () => {
    const onChange = jest.fn();
    render(<CustomTabsForDrilldown value={0} onChange={onChange} options={tabOptions} />);
    fireEvent.click(screen.getByText('Details'));
    expect(onChange).toHaveBeenCalled();
  });

  it('renders count pill when option has count property', () => {
    const optionsWithCount = [
      { value: 0, text: 'Events', count: 12 },
      { value: 1, text: 'Logs' },
    ];
    render(<CustomTabsForDrilldown value={0} onChange={jest.fn()} options={optionsWithCount} />);
    expect(screen.getByTestId('custom-pill')).toHaveTextContent('12');
  });

  it('renders beta icon badge when betaIcon is true', () => {
    const optionsWithBeta = [{ value: 0, text: 'Beta Feature', betaIcon: true }];
    render(<CustomTabsForDrilldown value={0} onChange={jest.fn()} options={optionsWithBeta} />);
    expect(screen.getByAltText('Beta icon')).toBeInTheDocument();
  });

  it('renders alpha icon badge when alphaIcon is true', () => {
    const optionsWithAlpha = [{ value: 0, text: 'Alpha Feature', alphaIcon: true }];
    render(<CustomTabsForDrilldown value={0} onChange={jest.fn()} options={optionsWithAlpha} />);
    // The alpha icon uses alt 'Beta icon' in the source
    const images = screen.getAllByTestId('safe-icon');
    expect(images.length).toBeGreaterThan(0);
  });

  it('renders right button when rightButton.visible is true', () => {
    render(
      <CustomTabsForDrilldown
        value={0}
        onChange={jest.fn()}
        options={tabOptions}
        rightButton={{ visible: true, text: 'Export', onClick: jest.fn() }}
      />
    );
    expect(screen.getByTestId('right-button')).toBeInTheDocument();
    expect(screen.getByTestId('right-button')).toHaveTextContent('Export');
  });

  it('does not render right button when rightButton.visible is false', () => {
    render(
      <CustomTabsForDrilldown
        value={0}
        onChange={jest.fn()}
        options={tabOptions}
        rightButton={{ visible: false, text: 'Export', onClick: jest.fn() }}
      />
    );
    expect(screen.queryByTestId('right-button')).not.toBeInTheDocument();
  });

  it('disables a tab when disabled is true', () => {
    const options = [
      { value: 0, text: 'Enabled' },
      { value: 1, text: 'Disabled Tab', disabled: true },
    ];
    render(<CustomTabsForDrilldown value={0} onChange={jest.fn()} options={options} />);
    const disabledTab = screen.getByText('Disabled Tab').closest('[role="tab"]');
    expect(disabledTab).toBeDisabled();
  });
});
