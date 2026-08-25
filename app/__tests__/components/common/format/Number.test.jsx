import { render, screen } from '@testing-library/react';
import NumberComponent from '@shared/format/Number';

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='tooltip' data-title={String(title ?? '')}>
      {children}
    </div>
  ),
}));

describe('NumberComponent', () => {
  // ─── falsy / invalid values → defaultVal ───────────────────────────────────

  describe('falsy / invalid values', () => {
    it('renders "-" when value is null', () => {
      render(<NumberComponent value={null} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is undefined', () => {
      render(<NumberComponent value={undefined} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is 0', () => {
      render(<NumberComponent value={0} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is empty string', () => {
      render(<NumberComponent value='' />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is NaN', () => {
      render(<NumberComponent value={NaN} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders custom defaultVal for invalid input', () => {
      render(<NumberComponent value={null} defaultVal='N/A' />);
      expect(screen.getByText('N/A')).toBeInTheDocument();
    });
  });

  // ─── valid number formatting ────────────────────────────────────────────────

  describe('valid number formatting', () => {
    it('renders a plain integer', () => {
      render(<NumberComponent value={42} />);
      expect(screen.getByText('42')).toBeInTheDocument();
    });

    it('renders a decimal rounded to 2 places by default', () => {
      render(<NumberComponent value={3.14159} />);
      expect(screen.getByText('3.14')).toBeInTheDocument();
    });

    it('renders a large number with thousands separators', () => {
      render(<NumberComponent value={1000000} />);
      expect(screen.getByText('1,000,000')).toBeInTheDocument();
    });

    it('accepts a numeric string and parses it', () => {
      render(<NumberComponent value='250' />);
      expect(screen.getByText('250')).toBeInTheDocument();
    });

    it('renders a negative number', () => {
      render(<NumberComponent value={-99} />);
      expect(screen.getByText('-99')).toBeInTheDocument();
    });
  });

  // ─── precision props ────────────────────────────────────────────────────────

  describe('precision props', () => {
    it('pads decimals when minimumFractionDigits is set', () => {
      render(<NumberComponent value={5} minimumFractionDigits={2} />);
      expect(screen.getByText('5.00')).toBeInTheDocument();
    });

    it('rounds to maximumFractionDigits', () => {
      render(<NumberComponent value={1.999} maximumFractionDigits={1} />);
      expect(screen.getByText('2')).toBeInTheDocument();
    });

    it('shows no decimals when maximumFractionDigits is 0', () => {
      render(<NumberComponent value={3.7} maximumFractionDigits={0} />);
      expect(screen.getByText('4')).toBeInTheDocument();
    });
  });

  // ─── suffix ─────────────────────────────────────────────────────────────────

  describe('suffix', () => {
    it('renders suffix when provided', () => {
      render(<NumberComponent value={100} suffix='%' />);
      expect(screen.getByText('%')).toBeInTheDocument();
    });

    it('renders both value and suffix together', () => {
      render(<NumberComponent value={99} suffix='cores' />);
      expect(screen.getByText('99')).toBeInTheDocument();
      expect(screen.getByText('cores')).toBeInTheDocument();
    });
  });

  // ─── tooltip ─────────────────────────────────────────────────────────────────

  describe('tooltip', () => {
    it('always renders a Tooltip wrapper', () => {
      render(<NumberComponent value={42} />);
      expect(screen.getByTestId('tooltip')).toBeInTheDocument();
    });

    it('tooltip title shows the value formatted with 6 decimal places', () => {
      render(<NumberComponent value={1234} />);
      // formatNumber(1234, '-', 0, max(6,2)=6) → '1,234' (no trailing zeros needed)
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '1,234');
    });

    it('tooltip title shows full precision for a decimal value', () => {
      render(<NumberComponent value={3.14159265} />);
      // formatNumber(3.14159265, '-', 0, 6) → '3.141593' (rounded to 6 places)
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '3.141593');
    });

    it('tooltip title respects a custom maximumFractionDigits when it exceeds 6', () => {
      render(<NumberComponent value={1.123456789} maximumFractionDigits={8} />);
      // max(6, 8) = 8
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '1.12345679');
    });

    it('renders Tooltip even for null value', () => {
      render(<NumberComponent value={null} />);
      expect(screen.getByTestId('tooltip')).toBeInTheDocument();
    });

    it('tooltip title shows defaultVal for null value', () => {
      render(<NumberComponent value={null} />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '-');
    });
  });
});
