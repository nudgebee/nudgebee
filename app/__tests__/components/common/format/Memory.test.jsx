import { render, screen } from '@testing-library/react';
import Memory from '@shared/format/Memory';

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='tooltip' data-title={String(title ?? '')}>
      {children}
    </div>
  ),
}));

// ─── null / undefined ────────────────────────────────────────────────────────

describe('Memory', () => {
  describe('null / undefined value', () => {
    it('renders "-" when value is null', () => {
      render(<Memory value={null} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is undefined', () => {
      render(<Memory value={undefined} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });
  });

  // ─── unit conversions ────────────────────────────────────────────────────────

  describe('unit conversions', () => {
    it('converts bytes → GB by default (1 GiB = 1)', () => {
      render(<Memory value={1073741824} sourceUnit='bytes' targetUnit='gb' suffix={false} />);
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    it('converts MB → GB (1024 MB = 1)', () => {
      render(<Memory value={1024} sourceUnit='mb' targetUnit='gb' suffix={false} />);
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    it('converts KB → GB (1024² KB = 1)', () => {
      render(<Memory value={1024 * 1024} sourceUnit='kb' targetUnit='gb' suffix={false} />);
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    it('converts bytes → MB (1 MiB = 1)', () => {
      render(<Memory value={1048576} sourceUnit='bytes' targetUnit='mb' suffix={false} />);
      expect(screen.getByText('1')).toBeInTheDocument();
    });

    it('passes GB → GB through unchanged', () => {
      render(<Memory value={4} sourceUnit='gb' targetUnit='gb' suffix={false} />);
      expect(screen.getByText('4')).toBeInTheDocument();
    });

    it('renders "-" for a zero value', () => {
      render(<Memory value={0} />);
      // formatNumber(0) returns '-' since 0 is falsy in the formatter
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('formats fractional results to up to 2 decimal places', () => {
      // 1.5 GB = 1536 MB; mb-gb → 1536/1024 = 1.5
      render(<Memory value={1536} sourceUnit='mb' targetUnit='gb' suffix={false} />);
      expect(screen.getByText('1.5')).toBeInTheDocument();
    });

    it('formats large values with thousands separators', () => {
      // 2048 GB = 2048 (gb-gb passthrough)
      render(<Memory value={2048} sourceUnit='gb' targetUnit='gb' suffix={false} />);
      expect(screen.getByText('2,048')).toBeInTheDocument();
    });
  });

  // ─── suffix ──────────────────────────────────────────────────────────────────

  describe('suffix', () => {
    it('shows the uppercased targetUnit when suffix=true (default)', () => {
      render(<Memory value={1073741824} sourceUnit='bytes' targetUnit='gb' />);
      expect(screen.getByText('GB')).toBeInTheDocument();
    });

    it('does not render suffix when suffix=false', () => {
      render(<Memory value={1073741824} sourceUnit='bytes' targetUnit='gb' suffix={false} />);
      expect(screen.queryByText('GB')).not.toBeInTheDocument();
    });

    it('uppercases custom targetUnit in the suffix', () => {
      render(<Memory value={1048576} sourceUnit='bytes' targetUnit='mb' />);
      expect(screen.getByText('MB')).toBeInTheDocument();
    });
  });

  // ─── tooltip ─────────────────────────────────────────────────────────────────

  describe('tooltip', () => {
    it('always renders a Tooltip wrapper for valid values', () => {
      render(<Memory value={1073741824} />);
      expect(screen.getByTestId('tooltip')).toBeInTheDocument();
    });

    it('tooltip title shows formatted value with 6 decimal places and unit', () => {
      render(<Memory value={1073741824} />);
      // formatMemory(1073741824, 'bytes', 'gb', true, 6) → '1 GB'
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '1 GB');
    });

    it('tooltip title shows full precision for a fractional value', () => {
      // 1536 MB → 1536/1024 = 1.5 GB; with 6 decimal places → '1.5 GB'
      render(<Memory value={1536} sourceUnit='mb' targetUnit='gb' />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '1.5 GB');
    });

    it('tooltip suffix matches targetUnit, not hardcoded GB', () => {
      render(<Memory value={1048576} sourceUnit='bytes' targetUnit='mb' />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '1 MB');
    });

    it('tooltip title shows higher precision than the cell for a sub-GB value', () => {
      // 104857600 bytes / (1024^3) = 0.09765625 GB → 6 decimal places → '0.097656'
      render(<Memory value={104857600} sourceUnit='bytes' targetUnit='gb' />);
      expect(screen.getByTestId('tooltip')).toHaveAttribute('data-title', '0.097656 GB');
    });

    it('does not render a Tooltip for null value', () => {
      render(<Memory value={null} />);
      expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
    });
  });
});
