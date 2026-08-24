import { render, screen } from '@testing-library/react';
import Text from '@shared/format/Text';

// jsdom does not implement ResizeObserver
global.ResizeObserver = class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
};

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children, title }) => (
    <div data-testid='tooltip'>
      <div data-testid='tooltip-title'>{title}</div>
      {children}
    </div>
  ),
}));

jest.mock('@shared/buttons/CopyButton', () => ({
  __esModule: true,
  default: ({ text }) => <button data-testid='copy-button' data-text={text} />,
}));

jest.mock('@shared/viewers/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }) => <div data-testid='markdown'>{data}</div>,
}));

// ─── value / defaultVal ───────────────────────────────────────────────────────

describe('Text', () => {
  describe('value and defaultVal', () => {
    it('renders the value when provided', () => {
      render(<Text value='Hello world' />);
      expect(screen.getByText('Hello world')).toBeInTheDocument();
    });

    it('renders "-" when value is falsy (null)', () => {
      render(<Text value={null} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is undefined', () => {
      render(<Text value={undefined} />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders "-" when value is empty string', () => {
      render(<Text value='' />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders custom defaultVal when value is falsy', () => {
      render(<Text value={null} defaultVal='N/A' />);
      expect(screen.getByText('N/A')).toBeInTheDocument();
    });
  });

  // ─── tooltip behaviour ────────────────────────────────────────────────────────

  describe('tooltip', () => {
    it('does not render Tooltip by default (no overflow, no copyableTooltip)', () => {
      render(<Text value='Hello' />);
      expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
    });

    it('renders Tooltip with CopyButton when copyableTooltip=true', () => {
      render(<Text value='copy me' copyableTooltip={true} />);
      expect(screen.getByTestId('tooltip')).toBeInTheDocument();
      expect(screen.getByTestId('copy-button')).toBeInTheDocument();
      expect(screen.getByTestId('copy-button')).toHaveAttribute('data-text', 'copy me');
    });

    it('does not render Tooltip when requiredToolTip=false even with copyableTooltip=true', () => {
      render(<Text value='copy me' copyableTooltip={true} requiredToolTip={false} />);
      expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
    });

    it('tooltip title renders the value text when copyableTooltip=true', () => {
      render(<Text value='copy me' copyableTooltip={true} />);
      expect(screen.getByTestId('tooltip-title')).toHaveTextContent('copy me');
    });
  });

  // ─── format ───────────────────────────────────────────────────────────────────

  describe('format', () => {
    it('renders plain text by default (format="text")', () => {
      render(<Text value='plain text' />);
      expect(screen.getByText('plain text')).toBeInTheDocument();
      expect(screen.queryByTestId('markdown')).not.toBeInTheDocument();
    });

    it('renders MarkDowns when format="markdown" and requiredToolTip=false', () => {
      render(<Text value='**bold**' format='markdown' requiredToolTip={false} />);
      expect(screen.getByTestId('markdown')).toBeInTheDocument();
      expect(screen.getByTestId('markdown')).toHaveTextContent('**bold**');
    });

    it('renders plain text when format="markdown" but requiredToolTip=true (default)', () => {
      render(<Text value='**bold**' format='markdown' />);
      expect(screen.queryByTestId('markdown')).not.toBeInTheDocument();
      expect(screen.getByText('**bold**')).toBeInTheDocument();
    });
  });

  // ─── showAutoEllipsis ─────────────────────────────────────────────────────────

  describe('showAutoEllipsis', () => {
    it('still renders value when showAutoEllipsis=true (overflow is false in jsdom)', () => {
      render(<Text value='some long text' showAutoEllipsis={true} />);
      expect(screen.getByText('some long text')).toBeInTheDocument();
    });

    it('does not show tooltip when text is not overflowing', () => {
      render(<Text value='short' showAutoEllipsis={true} />);
      expect(screen.queryByTestId('tooltip')).not.toBeInTheDocument();
    });
  });
});
