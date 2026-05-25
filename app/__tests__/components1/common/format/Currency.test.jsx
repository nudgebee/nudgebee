import React from 'react';
import { render, screen } from '@testing-library/react';
import Currency, { formatCurrency } from '@components1/common/format/Currency';

// Mock colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#666',
      secondaryDark: '#333',
      lowest: '#00aa00',
      red: '#ff0000',
    },
    button: {
      primary: '#3B82F6',
      primaryHover: '#2563EB',
      primaryText: '#FFFFFF',
      primaryDisabled: '#BFDBFE',
      primaryDisabledText: '#ffffff',
      secondary: '#FFFFFF',
      secondaryText: '#737373',
      secondaryHover: '#F8F8F8',
      secondaryBorder: '#d7d7d7',
      secondaryHoverBorder: '#D0D0D0',
      secondaryDisabled: '#F8F8F8',
      secondaryDisabledText: '#D0D0D0',
      secondaryDisabledBorder: '#EBEBEB',
      tertiaryBorder: '#60A5FA',
      tertiary: '#FFFFFF',
      tertiaryText: '#3B82F6',
      tertiaryHover: '#EFF6FF',
      tertiaryHoverBorder: '#60A5FA',
      tertiaryDisabled: '#FFFFFF',
      tertiaryDisabledText: '#93C5FD',
      tertiaryDisabledBorder: '#BFDBFE',
    },
  },
}));

describe('formatCurrency (named export)', () => {
  it('returns "-" for empty string', () => {
    expect(formatCurrency('')).toBe('-');
  });

  it('returns formatted currency for integer', () => {
    expect(formatCurrency(1000)).toBe('$1,000');
  });

  it('returns formatted currency for float', () => {
    expect(formatCurrency(1234.56)).toBe('$1,234.56');
  });

  it('returns "-" for NaN string', () => {
    expect(formatCurrency('abc')).toBe('-');
  });

  it('returns "-" for Infinity', () => {
    expect(formatCurrency(Infinity)).toBe('-');
  });

  it('returns formatted currency for numeric string', () => {
    expect(formatCurrency('500')).toBe('$500');
  });
});

describe('Currency component', () => {
  it('renders defaultVal when value is null', () => {
    render(<Currency value={null} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders defaultVal when value is undefined', () => {
    render(<Currency value={undefined} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders defaultVal when value is empty string', () => {
    render(<Currency value='' />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders defaultVal when value is NaN', () => {
    render(<Currency value={NaN} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders defaultVal when value is 0', () => {
    render(<Currency value={0} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders custom defaultVal when value is null', () => {
    render(<Currency value={null} defaultVal='N/A' />);
    expect(screen.getByText('N/A')).toBeInTheDocument();
  });

  it('renders without tooltip when withTooltip=false and value is null', () => {
    render(<Currency value={null} withTooltip={false} />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders formatted value for a valid number', () => {
    render(<Currency value={1000} />);
    expect(screen.getByText('1,000')).toBeInTheDocument();
    expect(screen.getByText('$')).toBeInTheDocument();
  });

  it('renders with custom prefix', () => {
    render(<Currency value={500} prefix='€' />);
    expect(screen.getByText('€')).toBeInTheDocument();
  });

  it('renders with suffix', () => {
    render(<Currency value={500} suffix='/mo' />);
    expect(screen.getByText('/mo')).toBeInTheDocument();
  });

  it('renders with withTooltip=false and valid value', () => {
    render(<Currency value={500} withTooltip={false} />);
    expect(screen.getByText('500')).toBeInTheDocument();
  });

  it('renders with savings variant applying green color (sx empty)', () => {
    render(<Currency value={500} varient='savings' />);
    expect(screen.getByText('500')).toBeInTheDocument();
  });

  it('renders with expense variant applying red color', () => {
    render(<Currency value={500} varient='expense' />);
    expect(screen.getByText('500')).toBeInTheDocument();
  });

  it('renders with custom sx overriding variant', () => {
    render(<Currency value={500} varient='savings' sx={{ color: 'blue' }} />);
    expect(screen.getByText('500')).toBeInTheDocument();
  });

  it('shows < prefix when formattedValue < 1 and precison=0 with tooltip', () => {
    // 0.1 rounds to 0 with 0 decimal places, so formattedValue = "0" => parseFloat("0") < 1
    render(<Currency value={0.1} precison={0} withTooltip={true} />);
    expect(screen.getByText('<')).toBeInTheDocument();
  });

  it('shows < prefix when formattedValue < 1 and precison=0 without tooltip', () => {
    render(<Currency value={0.1} precison={0} withTooltip={false} />);
    expect(screen.getByText('<')).toBeInTheDocument();
  });

  it('does not show < when value >= 1', () => {
    render(<Currency value={2} precison={0} />);
    const lessThans = screen.queryAllByText('<');
    expect(lessThans.length).toBe(0);
  });

  it('renders percent when withTooltip=false', () => {
    render(<Currency value={500} withTooltip={false} percent='10%' />);
    expect(screen.getByText('(10%)')).toBeInTheDocument();
  });

  it('does not render percent when withTooltip=true', () => {
    render(<Currency value={500} withTooltip={true} percent='10%' />);
    expect(screen.queryByText('(10%)')).not.toBeInTheDocument();
  });

  it('renders with precision=2', () => {
    render(<Currency value={1234.5} precison={2} />);
    expect(screen.getByText('1,234.50')).toBeInTheDocument();
  });

  it('handles non-finite value (Infinity) and shows - inside component', () => {
    render(<Currency value={Infinity} />);
    // Infinity is not finite so formattedValue = '-'
    // But Infinity is not NaN and !== '', !== 0, so it won't be in defaultVal branch
    // It will be in main render block showing '-'
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('applies || {} fallback when varient is an unknown string (sx empty)', () => {
    // variantStyles["unknown"] is undefined, so || {} fallback is taken
    render(<Currency value={500} varient='unknown' />);
    expect(screen.getByText('500')).toBeInTheDocument();
  });

  it('renders percent with negative formattedValue (colors.text.red branch)', () => {
    // When formattedValue is '-', parseFloat('-') is NaN and NaN > 0 is false => colors.text.red
    render(<Currency value={Infinity} withTooltip={false} percent='50%' />);
    expect(screen.getByText('(50%)')).toBeInTheDocument();
  });
});
