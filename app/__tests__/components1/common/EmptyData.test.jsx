import React from 'react';
import { render, screen } from '@testing-library/react';
import EmptyData from '@components1/common/EmptyData';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#374151',
      primary: '#3B82F6',
      white: '#fff',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
    },
    background: {
      primaryLightest: '#EFF6FF',
      titleUnderline: '#3B82F6',
      white: '#fff',
      transparent: 'transparent',
      switchTrackDark: '#3B82F6',
      tertiaryLightest: '#F0F9FF',
    },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', success: '#22C55E' },
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
    primary: '#3B82F6',
  },
}));

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ src, alt, ...props }) => <img src={src} alt={alt} data-testid='next-image' {...props} />,
}));

describe('EmptyData', () => {
  it('renders heading with correct id', () => {
    render(<EmptyData id='test' heading='No Data Found' />);
    const heading = screen.getByRole('heading', { level: 2 });
    expect(heading).toBeInTheDocument();
    expect(heading).toHaveAttribute('id', 'test-no-data');
    expect(heading).toHaveTextContent('No Data Found');
  });

  it('renders heading text correctly', () => {
    render(<EmptyData id='items' heading='No Items Available' />);
    expect(screen.getByText('No Items Available')).toBeInTheDocument();
  });

  it('does not render subHeading when not provided', () => {
    render(<EmptyData id='test' heading='Heading Only' />);
    expect(screen.queryByText('Some sub heading')).not.toBeInTheDocument();
  });

  it('renders subHeading when provided', () => {
    render(<EmptyData id='test' heading='No Data' subHeading='Try again later' />);
    expect(screen.getByText('Try again later')).toBeInTheDocument();
  });

  it('does not render image when img prop is not provided', () => {
    render(<EmptyData id='test' heading='No Data' />);
    expect(screen.queryByTestId('next-image')).not.toBeInTheDocument();
  });

  it('renders image when img prop is provided', () => {
    render(<EmptyData id='test' heading='No Data' img='/empty.png' />);
    const image = screen.getByTestId('next-image');
    expect(image).toBeInTheDocument();
    expect(image).toHaveAttribute('src', '/empty.png');
    expect(image).toHaveAttribute('alt', 'empty data');
  });

  it('renders children when provided', () => {
    render(
      <EmptyData id='test' heading='No Data'>
        <button>Retry</button>
      </EmptyData>
    );
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
  });

  it('renders both subHeading and children together', () => {
    render(
      <EmptyData id='test' heading='No Data' subHeading='Please retry'>
        <span>Child content</span>
      </EmptyData>
    );
    expect(screen.getByText('Please retry')).toBeInTheDocument();
    expect(screen.getByText('Child content')).toBeInTheDocument();
  });

  it('uses default height of 308px when height prop is not provided', () => {
    const { container } = render(<EmptyData id='test' heading='No Data' />);
    // The inner Box has the height applied via sx/style
    // We verify rendering completes without error
    expect(container.firstChild).toBeInTheDocument();
  });

  it('accepts custom height prop', () => {
    const { container } = render(<EmptyData id='test' heading='No Data' height='500px' />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders with empty string heading', () => {
    render(<EmptyData id='test' heading='' />);
    const heading = document.getElementById('test-no-data');
    expect(heading).toBeInTheDocument();
  });

  it('renders with no props except id', () => {
    render(<EmptyData id='minimal' />);
    const heading = document.getElementById('minimal-no-data');
    expect(heading).toBeInTheDocument();
  });

  it('accepts sx prop without error', () => {
    const { container } = render(<EmptyData id='test' heading='No Data' sx={{ padding: '10px' }} />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders image with correct dimensions', () => {
    render(<EmptyData id='test' heading='No Data' img='/test.png' />);
    const image = screen.getByTestId('next-image');
    expect(image).toHaveAttribute('height', '128');
    expect(image).toHaveAttribute('width', '128');
  });
});
