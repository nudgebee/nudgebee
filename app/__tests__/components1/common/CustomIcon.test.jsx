import React from 'react';
import { render, screen } from '@testing-library/react';
import CustomIcon from '@components1/common/CustomIcon';

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

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} {...props} />,
}));

describe('CustomIcon', () => {
  it('renders without crashing', () => {
    const { container } = render(<CustomIcon />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders SafeIcon when icon is a string/object (SVG path)', () => {
    render(<CustomIcon icon='/mock-icon.svg' />);
    expect(screen.getByAltText('icon')).toBeInTheDocument();
  });

  it('renders React element icon when icon is a JSX element', () => {
    render(<CustomIcon icon={<span data-testid='jsx-icon'>Icon</span>} />);
    expect(screen.getByTestId('jsx-icon')).toBeInTheDocument();
  });

  it('renders using React.createElement when icon is a function component', () => {
    const FuncIcon = ({ width, height }) => <svg data-testid='func-icon' width={width} height={height} />;
    render(<CustomIcon icon={FuncIcon} />);
    expect(screen.getByTestId('func-icon')).toBeInTheDocument();
  });

  it('renders nothing in icon container when no icon provided', () => {
    const { container } = render(<CustomIcon />);
    // When no icon is provided, no img or svg should be rendered
    expect(container.querySelector('img')).not.toBeInTheDocument();
    expect(container.querySelector('svg')).not.toBeInTheDocument();
  });
});
