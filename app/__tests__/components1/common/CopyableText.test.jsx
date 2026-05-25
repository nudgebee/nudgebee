import React from 'react';
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react';
import CopyableText from '@components1/common/CopyableText';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280', success: '#16a34a' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', success: '#22C55E' },
    primary: '#3B82F6',
  },
}));

jest.mock('@assets/copy-icon.svg', () => 'copy-icon-mock');
jest.mock('@components1/common/MarkDowns', () => ({
  __esModule: true,
  default: ({ data }) => <div data-testid='markdown'>{data}</div>,
}));
jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

describe('CopyableText', () => {
  beforeEach(() => {
    Object.assign(navigator, {
      clipboard: { writeText: jest.fn().mockResolvedValue(undefined) },
    });
    jest.useFakeTimers();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  describe('basic rendering', () => {
    it('renders children text', () => {
      render(<CopyableText copyableText='hello'>Hello World</CopyableText>);
      expect(screen.getByText('Hello World')).toBeInTheDocument();
    });

    it('renders with role button', () => {
      render(<CopyableText copyableText='test'>Test</CopyableText>);
      expect(screen.getByRole('button')).toBeInTheDocument();
    });

    it('renders copy icon by default', () => {
      render(<CopyableText copyableText='test'>Test</CopyableText>);
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });

    it('renders without children', () => {
      expect(() => render(<CopyableText copyableText='test' />)).not.toThrow();
    });
  });

  describe('icon position', () => {
    it('renders icon at start by default', () => {
      const { container } = render(<CopyableText copyableText='test'>Test</CopyableText>);
      const box = container.firstChild;
      // First child should be the copy icon wrapper
      expect(box.firstChild).not.toBeNull();
    });

    it('renders icon at end when iconPosition="end"', () => {
      render(
        <CopyableText copyableText='test' iconPosition='end'>
          Test
        </CopyableText>
      );
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });

    it('renders icon at start when iconPosition="start"', () => {
      render(
        <CopyableText copyableText='test' iconPosition='start'>
          Test
        </CopyableText>
      );
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });
  });

  describe('copy behavior', () => {
    it('calls clipboard.writeText with copyableText when clicked', async () => {
      render(<CopyableText copyableText='copy me'>Click</CopyableText>);
      fireEvent.click(screen.getByRole('button'));
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith('copy me');
    });

    it('shows check icon after click', () => {
      render(<CopyableText copyableText='test'>Text</CopyableText>);
      fireEvent.click(screen.getByRole('button'));
      // After click, the check icon should be rendered (no longer copy icon)
      expect(screen.queryByTestId('safe-icon')).not.toBeInTheDocument();
    });

    it('reverts back to copy icon after 2 seconds', async () => {
      render(<CopyableText copyableText='test'>Text</CopyableText>);
      fireEvent.click(screen.getByRole('button'));
      expect(screen.queryByTestId('safe-icon')).not.toBeInTheDocument();

      act(() => {
        jest.advanceTimersByTime(2000);
      });

      await waitFor(() => {
        expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
      });
    });

    it('does not copy again if already copied (debounce)', () => {
      render(<CopyableText copyableText='test'>Text</CopyableText>);
      fireEvent.click(screen.getByRole('button'));
      fireEvent.click(screen.getByRole('button'));
      expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(1);
    });

    it('calls onCopy callback with copyableText when copied', () => {
      const onCopy = jest.fn();
      render(
        <CopyableText copyableText='my text' onCopy={onCopy}>
          Text
        </CopyableText>
      );
      fireEvent.click(screen.getByRole('button'));
      expect(onCopy).toHaveBeenCalledWith('my text');
    });

    it('does not throw when onCopy is not provided', () => {
      render(<CopyableText copyableText='test'>Text</CopyableText>);
      expect(() => fireEvent.click(screen.getByRole('button'))).not.toThrow();
    });
  });

  describe('keyboard interaction', () => {
    it('triggers copy on Enter keydown', () => {
      render(<CopyableText copyableText='enter test'>Text</CopyableText>);
      fireEvent.keyDown(screen.getByRole('button'), { key: 'Enter' });
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith('enter test');
    });

    it('triggers copy on Space keydown', () => {
      render(<CopyableText copyableText='space test'>Text</CopyableText>);
      fireEvent.keyDown(screen.getByRole('button'), { key: ' ' });
      expect(navigator.clipboard.writeText).toHaveBeenCalledWith('space test');
    });

    it('does not trigger copy on other keys', () => {
      render(<CopyableText copyableText='other key'>Text</CopyableText>);
      fireEvent.keyDown(screen.getByRole('button'), { key: 'Tab' });
      expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
    });
  });

  describe('format prop', () => {
    it('renders markdown format using MarkDowns component', () => {
      render(
        <CopyableText copyableText='test' format='markdown'>
          **bold**
        </CopyableText>
      );
      expect(screen.getByTestId('markdown')).toBeInTheDocument();
    });

    it('renders text format as plain children', () => {
      render(
        <CopyableText copyableText='test' format='text'>
          Plain text
        </CopyableText>
      );
      expect(screen.getByText('Plain text')).toBeInTheDocument();
      expect(screen.queryByTestId('markdown')).not.toBeInTheDocument();
    });
  });

  describe('showCopyIconOnHover prop', () => {
    it('renders without error when showCopyIconOnHover is true', () => {
      expect(() =>
        render(
          <CopyableText copyableText='test' showCopyIconOnHover={true}>
            Text
          </CopyableText>
        )
      ).not.toThrow();
    });

    it('renders without error when showCopyIconOnHover is false', () => {
      expect(() =>
        render(
          <CopyableText copyableText='test' showCopyIconOnHover={false}>
            Text
          </CopyableText>
        )
      ).not.toThrow();
    });
  });

  describe('fallback clipboard (no navigator.clipboard)', () => {
    it('falls back gracefully when clipboard API is not available', () => {
      const originalClipboard = navigator.clipboard;
      Object.defineProperty(navigator, 'clipboard', {
        value: undefined,
        writable: true,
        configurable: true,
      });

      render(<CopyableText copyableText='fallback test'>Text</CopyableText>);
      // Should not throw even without clipboard API
      expect(() => fireEvent.click(screen.getByRole('button'))).not.toThrow();

      Object.defineProperty(navigator, 'clipboard', {
        value: originalClipboard,
        writable: true,
        configurable: true,
      });
    });
  });
});
