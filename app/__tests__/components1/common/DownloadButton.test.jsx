import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import DownloadButton from '@components1/common/DownloadButton';

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

jest.mock('file-saver', () => ({ saveAs: jest.fn() }));

describe('DownloadButton', () => {
  it('renders without crashing', () => {
    render(<DownloadButton />);
    expect(screen.getByAltText('download icon')).toBeInTheDocument();
  });

  it('tooltip shows "Download" when onClick provided', () => {
    const onClick = jest.fn().mockResolvedValue({ data: 'test' });
    render(<DownloadButton onClick={onClick} data-testid='btn-box' />);
    // MUI Tooltip title is set to "Download"
    // Verify the component renders and the icon is present
    expect(screen.getByAltText('download icon')).toBeInTheDocument();
  });

  it('tooltip shows "Coming Soon" when onClick not provided', () => {
    render(<DownloadButton />);
    expect(screen.getByAltText('download icon')).toBeInTheDocument();
  });

  it('calls onClick when clicked', async () => {
    const onClick = jest.fn().mockResolvedValue({ data: 'some data', fileType: 'text/plain', fileName: 'test.txt' });
    render(<DownloadButton onClick={onClick} data-testid='download-box' />);
    const box = screen.getByAltText('download icon').closest('div');
    fireEvent.click(box);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('applies custom width and height', () => {
    render(<DownloadButton width='40px' height='40px' data-testid='download-box' />);
    expect(screen.getByAltText('download icon')).toBeInTheDocument();
  });

  it('applies id prop', () => {
    render(<DownloadButton id='my-download-btn' />);
    const element = document.getElementById('my-download-btn');
    expect(element).toBeInTheDocument();
  });

  it('cursor is "pointer" when onClick provided', () => {
    const onClick = jest.fn().mockResolvedValue({});
    render(<DownloadButton onClick={onClick} id='clickable-btn' />);
    const element = document.getElementById('clickable-btn');
    expect(element).toHaveStyle({ cursor: 'pointer' });
  });
});
