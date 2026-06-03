import React from 'react';
import { render, screen, act } from '@testing-library/react';
import Text from '@components1/common/format/Text';

// Mock external dependencies
jest.mock('@components1/common/CopyableText', () => {
  const CopyableText = ({ children, copyableText, iconColor, format }) => (
    <div data-testid='copyable-text' data-copyable={copyableText} data-icon-color={iconColor} data-format={format}>
      {children}
    </div>
  );
  CopyableText.displayName = 'CopyableText';
  return CopyableText;
});

jest.mock('@components1/common/MarkDowns', () => {
  const MarkDowns = ({ data, sx }) => (
    <div data-testid='markdowns' data-sx={JSON.stringify(sx)}>
      {data}
    </div>
  );
  MarkDowns.displayName = 'MarkDowns';
  return MarkDowns;
});

jest.mock('@components1/common/CustomTooltip', () => {
  const CustomTooltip = ({ children, title, tooltipClassName: _tooltipClassName, tooltipStyle: _tooltipStyle }) => (
    <div data-testid='custom-tooltip' data-title={typeof title === 'string' ? title : 'element'}>
      {/* Render title so that any React elements in it (like CopyableText) appear in the DOM */}
      <div data-testid='tooltip-title-content'>{title}</div>
      {children}
    </div>
  );
  CustomTooltip.displayName = 'CustomTooltip';
  return CustomTooltip;
});

// Mock colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      secondary: '#666',
      secondaryDark: '#333',
    },
  },
}));

describe('Text component', () => {
  // ---- Basic rendering ----
  it('renders value when provided', () => {
    render(<Text value='Hello World' />);
    expect(screen.getByText('Hello World')).toBeInTheDocument();
  });

  it('renders defaultVal when value is not provided', () => {
    render(<Text />);
    expect(screen.getByText('-')).toBeInTheDocument();
  });

  it('renders custom defaultVal when value is not provided', () => {
    render(<Text defaultVal='N/A' />);
    expect(screen.getByText('N/A')).toBeInTheDocument();
  });

  it('renders value over defaultVal when both provided', () => {
    render(<Text value='actual' defaultVal='fallback' />);
    expect(screen.getByText('actual')).toBeInTheDocument();
    expect(screen.queryByText('fallback')).not.toBeInTheDocument();
  });

  // ---- secondaryText ----
  it('renders with secondaryText=true (smaller font size)', () => {
    render(<Text value='secondary' secondaryText={true} />);
    expect(screen.getByText('secondary')).toBeInTheDocument();
  });

  it('renders with secondaryText=false (default)', () => {
    render(<Text value='primary' secondaryText={false} />);
    expect(screen.getByText('primary')).toBeInTheDocument();
  });

  // ---- color prop ----
  it('renders with color prop', () => {
    render(<Text value='colored' color='red' />);
    expect(screen.getByText('colored')).toBeInTheDocument();
  });

  // ---- format='markdown' with requiredToolTip=false ----
  it('renders MarkDowns when format=markdown and requiredToolTip=false', () => {
    render(<Text value='## Heading' format='markdown' requiredToolTip={false} />);
    expect(screen.getByTestId('markdowns')).toBeInTheDocument();
    expect(screen.getByTestId('markdowns')).toHaveTextContent('## Heading');
  });

  it('does NOT render MarkDowns when format=markdown and requiredToolTip=true', () => {
    render(<Text value='## Heading' format='markdown' requiredToolTip={true} />);
    expect(screen.queryByTestId('markdowns')).not.toBeInTheDocument();
    expect(screen.getByText('## Heading')).toBeInTheDocument();
  });

  // ---- copyableTooltip ----
  it('renders CopyableText in tooltip when copyableTooltip=true', () => {
    render(<Text value='copy me' copyableTooltip={true} />);
    // CustomTooltip should wrap, CopyableText should be the title element
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
    expect(screen.getByTestId('copyable-text')).toBeInTheDocument();
  });

  it('does not render CopyableText when copyableTooltip=false (default)', () => {
    render(<Text value='normal' />);
    expect(screen.queryByTestId('copyable-text')).not.toBeInTheDocument();
  });

  it('passes format to CopyableText', () => {
    render(<Text value='some text' copyableTooltip={true} format='markdown' />);
    expect(screen.getByTestId('copyable-text')).toHaveAttribute('data-format', 'markdown');
  });

  // ---- requiredToolTip=true with toolTip set ----
  it('wraps in CustomTooltip when requiredToolTip=true and toolTip is set via copyableTooltip', () => {
    render(<Text value='tooltipped' requiredToolTip={true} copyableTooltip={true} />);
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
  });

  // ---- requiredToolTip=false — no tooltip wrapper ----
  it('does not wrap in CustomTooltip when requiredToolTip=false', () => {
    render(<Text value='no tooltip' requiredToolTip={false} copyableTooltip={true} />);
    expect(screen.queryByTestId('custom-tooltip')).not.toBeInTheDocument();
  });

  // ---- showAutoEllipsis ----
  it('renders with showAutoEllipsis=true', () => {
    render(<Text value='long text here' showAutoEllipsis={true} />);
    expect(screen.getByText('long text here')).toBeInTheDocument();
  });

  it('renders with showAutoEllipsis=true and lineClamp=3', () => {
    render(<Text value='long text here' showAutoEllipsis={true} lineClamp={3} />);
    expect(screen.getByText('long text here')).toBeInTheDocument();
  });

  // ---- isOverflowing branch ----
  it('sets toolTip to value when isOverflowing=true (simulated via scrollHeight mock)', async () => {
    // Override ResizeObserver to trigger the callback
    let capturedCallback = null;
    const OriginalResizeObserver = global.ResizeObserver;
    global.ResizeObserver = class {
      constructor(callback) {
        capturedCallback = callback;
      }
      observe() {
        if (capturedCallback) capturedCallback([]);
      }
      disconnect() {}
    };

    // Mock element properties to simulate overflow
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        return 100;
      },
    });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get() {
        return 50;
      },
    });

    await act(async () => {
      render(<Text value='overflowing text that is long' showAutoEllipsis={true} minLength={5} />);
    });

    // When overflow is detected and requiredToolTip=true, CustomTooltip is shown
    const elements = screen.getAllByText('overflowing text that is long');
    expect(elements.length).toBeGreaterThan(0);

    // Restore
    global.ResizeObserver = OriginalResizeObserver;
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        return 0;
      },
    });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get() {
        return 0;
      },
    });
  });

  // ---- showAutoEllipsis with no overflow ----
  it('does not set tooltip when showAutoEllipsis=true but no overflow', async () => {
    Object.defineProperty(HTMLElement.prototype, 'scrollHeight', {
      configurable: true,
      get() {
        return 20;
      },
    });
    Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
      configurable: true,
      get() {
        return 20;
      },
    });

    await act(async () => {
      render(<Text value='short' showAutoEllipsis={true} />);
    });

    // No tooltip wrapper
    expect(screen.queryByTestId('custom-tooltip')).not.toBeInTheDocument();
  });

  // ---- showAutoEllipsis with value shorter than minLength (covers setIsOverflowing(false) else branch) ----
  it('does not set isOverflowing when value length < minLength', async () => {
    jest.useFakeTimers();
    let utils;
    await act(async () => {
      utils = render(<Text value='hi' showAutoEllipsis={true} minLength={10} />);
    });
    act(() => {
      jest.runAllTimers();
    });
    expect(utils.queryByTestId('custom-tooltip')).not.toBeInTheDocument();
    jest.useRealTimers();
  });

  // ---- showAutoEllipsis=false explicitly triggers setIsOverflowing(false) in else branch ----
  it('hits setIsOverflowing(false) when showAutoEllipsis=false', async () => {
    jest.useFakeTimers();
    await act(async () => {
      render(<Text value='some text that is long enough' showAutoEllipsis={false} />);
    });
    act(() => {
      jest.runAllTimers();
    });
    // No tooltip since isOverflowing stays false
    expect(screen.queryByTestId('custom-tooltip')).not.toBeInTheDocument();
    jest.useRealTimers();
  });

  // ---- no value, showAutoEllipsis=true ----
  it('does not set isOverflowing when updatedValue is falsy', async () => {
    await act(async () => {
      render(<Text value='' defaultVal='' showAutoEllipsis={true} />);
    });
    // No crash
    expect(screen.queryByTestId('custom-tooltip')).not.toBeInTheDocument();
  });

  // ---- sx prop ----
  it('applies custom sx styling', () => {
    render(<Text value='styled' sx={{ color: 'blue', fontSize: '20px' }} />);
    expect(screen.getByText('styled')).toBeInTheDocument();
  });

  // ---- tooltipClassName ----
  it('passes tooltipClassName to CustomTooltip', () => {
    render(<Text value='tip' copyableTooltip={true} tooltipClassName='my-tooltip' />);
    expect(screen.getByTestId('custom-tooltip')).toBeInTheDocument();
  });

  // ---- CopyableText receives iconColor white ----
  it('CopyableText receives iconColor=white', () => {
    render(<Text value='copy' copyableTooltip={true} />);
    expect(screen.getByTestId('copyable-text')).toHaveAttribute('data-icon-color', 'white');
  });

  // ---- CopyableText receives copyableText=value ----
  it('CopyableText receives the value as copyableText', () => {
    render(<Text value='my value' copyableTooltip={true} />);
    expect(screen.getByTestId('copyable-text')).toHaveAttribute('data-copyable', 'my value');
  });
});
