import React from 'react';
import { render, screen } from '@testing-library/react';
import Currency, { formatCurrency } from '@shared/format/Currency';

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='tooltip' data-title={title}>
      {children}
    </div>
  ),
}));

// ─── formatCurrency (pure function) ──────────────────────────────────────────

describe('formatCurrency', () => {
  it('returns "-" for empty string', () => {
    expect(formatCurrency('')).toBe('-');
  });

  it('returns "-" for NaN', () => {
    expect(formatCurrency('abc')).toBe('-');
  });

  it('returns "-" for Infinity', () => {
    expect(formatCurrency(Infinity)).toBe('-');
  });

  it('formats an integer', () => {
    expect(formatCurrency(1000)).toBe('$1,000');
  });

  it('formats a decimal (max 2 fraction digits)', () => {
    expect(formatCurrency(9.999)).toBe('$10');
    expect(formatCurrency(1.5)).toBe('$1.5');
    expect(formatCurrency(1.555)).toBe('$1.56');
  });

  it('formats a numeric string', () => {
    expect(formatCurrency('250')).toBe('$250');
  });

  it('formats large numbers with commas', () => {
    expect(formatCurrency(1000000)).toBe('$1,000,000');
  });
});

// ─── Currency component ───────────────────────────────────────────────────────

describe('Currency', () => {
  describe('empty / null / zero values', () => {
    it('renders "-" when value is null', () => {
      render(<Currency value={null} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is undefined', () => {
      render(<Currency value={undefined} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is NaN', () => {
      render(<Currency value={NaN} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is empty string', () => {
      render(<Currency value='' />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is 0', () => {
      render(<Currency value={0} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders custom defaultVal when value is null', () => {
      render(<Currency value={null} defaultVal='N/A' />);
      expect(screen.getByText('N/A')).toBeInTheDocument();
    });
  });

  describe('normal value rendering', () => {
    it('renders the "$" prefix by default', () => {
      render(<Currency value={500} withTooltip={false} />);
      expect(screen.getByText('$')).toBeInTheDocument();
    });

    it('renders the formatted number', () => {
      render(<Currency value={1234} withTooltip={false} precison={0} />);
      expect(screen.getByText('1,234')).toBeInTheDocument();
    });

    it('renders a custom prefix', () => {
      render(<Currency value={500} prefix='€' withTooltip={false} />);
      expect(screen.getByText('€')).toBeInTheDocument();
    });

    it('renders suffix when provided', () => {
      render(<Currency value={500} suffix='/mo' withTooltip={false} />);
      expect(screen.getByText('/mo')).toBeInTheDocument();
    });

    it('respects custom precision', () => {
      render(<Currency value={9.5} precison={2} withTooltip={false} />);
      expect(screen.getByText('9.50')).toBeInTheDocument();
    });
  });

  describe('small values (< 1)', () => {
    it('shows "<" prefix when value < 1 and precision is 0', () => {
      render(<Currency value={0.5} precison={0} withTooltip={false} />);
      expect(screen.getByText('<')).toBeInTheDocument();
    });

    it('displays "1" as the value when value < 1 and precision is 0', () => {
      render(<Currency value={0.5} precison={0} withTooltip={false} />);
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    // The withTooltip=false branch always shows '<' for value < 1 regardless
    // of precision; the precison===0 guard only applies in the withTooltip=true branch.
    it('still shows "<" with precision > 0 when withTooltip=false', () => {
      render(<Currency value={0.5} precison={2} withTooltip={false} />);
      expect(screen.getByText('<')).toBeInTheDocument();
      expect(screen.getByText('0.50')).toBeInTheDocument();
    });

    it('does not show "<" when value >= 1', () => {
      render(<Currency value={1.5} precison={0} withTooltip={false} />);
      expect(screen.queryByText('<')).not.toBeInTheDocument();
    });
  });

  describe('negative-zero edge case', () => {
    it('does not render "-0" for values that round to zero', () => {
      render(<Currency value={-0.001} precison={0} withTooltip={false} />);
      const texts = screen.queryAllByText((content) => content.includes('-0'));
      expect(texts).toHaveLength(0);
    });
  });

  describe('withTooltip prop', () => {
    it('wraps output in a Tooltip by default', () => {
      render(<Currency value={100} />);
      expect(screen.getByTestId('tooltip')).toBeInTheDocument();
    });

    it('renders without Tooltip when withTooltip=false', () => {
      render(<Currency value={100} withTooltip={false} />);
      expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
    });

    it('tooltip title shows the exact value to 2 decimal places', () => {
      render(<Currency value={123.4} />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '123.40');
    });

    it('tooltip title shows "0.00" for near-zero negative values', () => {
      // values in (-0.005, 0) round to 0 — show "0.00" instead of a tiny negative
      render(<Currency value={-0.001} />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '0.00');
    });

    it('tooltip title is empty when value is null and defaultVal is "-"', () => {
      render(<Currency value={null} defaultVal='-' />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '');
    });

    it('tooltip title shows defaultVal when value is null and defaultVal is not "-"', () => {
      render(<Currency value={null} defaultVal='N/A' />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', 'N/A');
    });
  });

  describe('isSavingPotential', () => {
    it('shows "~ $0" when isSavingPotential=true and value is negative', () => {
      render(<Currency value={-50} isSavingPotential={true} withTooltip={true} />);
      // '~ ', '$', and '0' are inline text nodes inside one Typography (<p>) element
      expect(screen.getByText((_, el) => el?.tagName === 'P' && el?.textContent === '~ $0')).toBeInTheDocument();
    });

    it('does not show saving potential UI when value is positive', () => {
      render(<Currency value={50} isSavingPotential={true} withTooltip={true} />);
      expect(screen.queryByText('~')).not.toBeInTheDocument();
    });

    it('uses custom recommendationLabel in saving potential tooltip', () => {
      render(<Currency value={-30} isSavingPotential={true} recommendationLabel='This change' />);
      const tooltip = screen.getByTestId('tooltip');
      expect(tooltip.getAttribute('data-title')).toContain('This change');
    });

    it('includes suffix in saving potential tooltip description', () => {
      render(<Currency value={-20} isSavingPotential={true} suffix='/mo' />);
      const tooltip = screen.getByTestId('tooltip');
      expect(tooltip.getAttribute('data-title')).toContain('/mo');
    });
  });

  describe('negative values (normal path)', () => {
    it('renders a negative value with prefix', () => {
      render(<Currency value={-500} precison={0} withTooltip={false} />);
      expect(screen.getByText('-500')).toBeInTheDocument();
      expect(screen.getByText('$')).toBeInTheDocument();
    });

    it('tooltip title shows the exact negative value to 2 decimal places', () => {
      render(<Currency value={-123.4} />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '-123.40');
    });
  });

  describe('percent prop', () => {
    it('renders percent value when provided (withTooltip=false)', () => {
      render(<Currency value={200} percent='10%' withTooltip={false} />);
      expect(screen.getByText('(10%)')).toBeInTheDocument();
    });

    it('does not render percent when not provided', () => {
      render(<Currency value={200} withTooltip={false} />);
      expect(screen.queryByText(/\(/)).not.toBeInTheDocument();
    });
  });
});
