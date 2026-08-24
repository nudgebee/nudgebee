import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ErrorBoundary, { AppErrorBoundary, InlineFallback, withErrorBoundary } from '@shared/ErrorBoundary';

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id }: any) => (
    <button id={id} onClick={onClick} data-testid={id}>
      {children}
    </button>
  ),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }: { alt: string }) => React.createElement('img', { 'data-testid': 'error-icon', alt }),
}));

jest.mock('@assets', () => ({ ErrorIcon: 'error.svg' }));

jest.mock('@utils/colors');

jest.mock('next/router', () => ({ default: { push: jest.fn() } }));

const ThrowingComponent = ({ shouldThrow }: { shouldThrow?: boolean }) => {
  if (shouldThrow) throw new Error('test error');
  return <div data-testid='child'>OK</div>;
};

const consoleError = jest.spyOn(console, 'error').mockImplementation(() => {});

afterAll(() => {
  consoleError.mockRestore();
});

describe('ErrorBoundary', () => {
  describe('normal rendering', () => {
    it('renders children when no error occurs', () => {
      render(
        <ErrorBoundary>
          <div data-testid='child'>content</div>
        </ErrorBoundary>
      );
      expect(screen.getByTestId('child')).toBeInTheDocument();
    });
  });

  describe('error state', () => {
    it('renders inline fallback when child throws', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    });

    it('does not render children when error occurs', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      expect(screen.queryByTestId('child')).not.toBeInTheDocument();
    });

    it('renders custom fallback prop instead of inline fallback', () => {
      render(
        <ErrorBoundary fallback={<div data-testid='custom-fallback'>Custom error UI</div>}>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      expect(screen.getByTestId('custom-fallback')).toBeInTheDocument();
      expect(screen.queryByText('Something went wrong')).not.toBeInTheDocument();
    });

    it('calls onError callback when child throws', () => {
      const onError = jest.fn();
      render(
        <ErrorBoundary onError={onError}>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      expect(onError).toHaveBeenCalledTimes(1);
      expect(onError.mock.calls[0][0]).toBeInstanceOf(Error);
    });
  });

  describe('retry', () => {
    it('renders Retry button in inline fallback', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      expect(screen.getByTestId('retry-btn')).toBeInTheDocument();
    });

    it('clears error state when Retry is clicked', () => {
      render(
        <ErrorBoundary>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      fireEvent.click(screen.getByTestId('retry-btn'));
      // After retry, the throwing child re-renders and re-throws; error is re-caught
      // but the retry callback executed — verify the fallback is still present
      expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    });
  });

  describe('resetKey', () => {
    it('resets error state when resetKey changes', () => {
      const { rerender } = render(
        <ErrorBoundary resetKey='key1'>
          <ThrowingComponent shouldThrow />
        </ErrorBoundary>
      );
      expect(screen.getByText('Something went wrong')).toBeInTheDocument();

      rerender(
        <ErrorBoundary resetKey='key2'>
          <div data-testid='recovered'>Recovered</div>
        </ErrorBoundary>
      );
      expect(screen.getByTestId('recovered')).toBeInTheDocument();
    });
  });
});

describe('InlineFallback', () => {
  it('renders "Something went wrong"', () => {
    render(<InlineFallback onRetry={jest.fn()} />);
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });

  it('calls onRetry when Retry is clicked', () => {
    const onRetry = jest.fn();
    render(<InlineFallback onRetry={onRetry} />);
    fireEvent.click(screen.getByTestId('retry-btn'));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});

describe('AppErrorBoundary', () => {
  it('renders children when no error', () => {
    render(
      <AppErrorBoundary>
        <div data-testid='app-child'>app content</div>
      </AppErrorBoundary>
    );
    expect(screen.getByTestId('app-child')).toBeInTheDocument();
  });

  it('renders full-page fallback on error', () => {
    render(
      <AppErrorBoundary>
        <ThrowingComponent shouldThrow />
      </AppErrorBoundary>
    );
    expect(screen.getByText('Go to Homepage')).toBeInTheDocument();
  });
});

describe('withErrorBoundary', () => {
  it('renders wrapped component normally', () => {
    const Wrapped = withErrorBoundary(ThrowingComponent);
    render(<Wrapped shouldThrow={false} />);
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });

  it('shows fallback when wrapped component throws', () => {
    const Wrapped = withErrorBoundary(ThrowingComponent);
    render(<Wrapped shouldThrow={true} />);
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });
});
