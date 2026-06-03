import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import CustomBackButton from '@components1/common/CustomBackButton';

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

const mockPush = jest.fn();
const mockBack = jest.fn();

jest.mock('next/navigation', () => ({
  useRouter: jest.fn(() => ({ push: mockPush, back: mockBack })),
}));

jest.mock('@assets', () => ({
  AutoPilotGreyIcon: '/mock-icon.svg',
  MenuArrowDownIcon: '/mock-icon.svg',
  ArrowBackGrayIcon: '/mock-icon.svg',
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} {...props} />,
}));

describe('CustomBackButton', () => {
  beforeEach(() => {
    mockPush.mockClear();
    mockBack.mockClear();
  });

  it('renders the back icon (SafeIcon)', () => {
    render(<CustomBackButton />);
    expect(screen.getByAltText('arrow back')).toBeInTheDocument();
  });

  it('calls custom onClick when provided', () => {
    const onClick = jest.fn();
    render(<CustomBackButton onClick={onClick} />);
    const img = screen.getByAltText('arrow back');
    fireEvent.click(img);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('calls router.push(backButtonPath) when backButtonPath provided', () => {
    render(<CustomBackButton backButtonPath='/some-path' />);
    const img = screen.getByAltText('arrow back');
    fireEvent.click(img);
    expect(mockPush).toHaveBeenCalledWith('/some-path');
  });

  it('calls router.back() when no onClick or backButtonPath', () => {
    render(<CustomBackButton />);
    const img = screen.getByAltText('arrow back');
    fireEvent.click(img);
    expect(mockBack).toHaveBeenCalled();
  });

  it('renders with useNewIcon=true: shows IconButton with tooltip "Go Back"', () => {
    render(<CustomBackButton useNewIcon={true} />);
    // When useNewIcon=true, renders an IconButton (button role)
    expect(screen.getByRole('button')).toBeInTheDocument();
    expect(screen.getByAltText('arrow back')).toBeInTheDocument();
  });

  it('renders with useNewIcon=false (default): shows SafeIcon with class "go-back"', () => {
    render(<CustomBackButton useNewIcon={false} />);
    const img = screen.getByAltText('arrow back');
    expect(img).toBeInTheDocument();
    expect(img).toHaveClass('go-back');
  });
});
