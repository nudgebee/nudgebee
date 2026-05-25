import React from 'react';
import CustomButton from '@components1/common/NewCustomButton';
import { ErrorIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import Router from 'next/router';

// ─── Inline fallback (component-level) ───────────────────────────────────────

export function InlineFallback({ onRetry }: Readonly<{ onRetry: () => void }>) {
  return (
    <div style={{ width: '100%', minHeight: '280px', flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'center',
          gap: '12px',
          padding: '32px 24px',
          border: '1px solid #E5E7EB',
          borderRadius: '8px',
          background: '#FAFAFA',
          textAlign: 'center',
          width: '100%',
          maxWidth: '480px',
        }}
      >
        <SafeIcon src={ErrorIcon} alt='Error' style={{ width: '48px', height: 'auto', opacity: 0.6 }} />
        <p style={{ fontSize: '14px', fontWeight: 600, color: '#1B2D4A', margin: 0 }}>Something went wrong</p>
        <p style={{ fontSize: '12px', color: '#6B7280', margin: 0 }}>
          This section failed to load. You can retry or continue using the rest of the app.
        </p>
        <CustomButton id='retry-btn' variant='tertiary' size='Small' text='Retry' onClick={onRetry} />
      </div>
    </div>
  );
}

// ─── Full-page fallback (app-level) ──────────────────────────────────────────

function FullPageFallback({ onGoHome }: Readonly<{ onGoHome: () => void }>) {
  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        alignItems: 'center',
        height: 'auto',
        textAlign: 'center',
        marginTop: '80px',
        gap: '40px',
      }}
    >
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          textAlign: 'center',
        }}
      >
        <h1 style={{ fontSize: '170px', fontWeight: 'bold', margin: '0px', color: '#1B2D4A' }}>500</h1>
        <p style={{ fontSize: '15px', fontWeight: 500, margin: '0px', color: '#1B2D4A' }}>
          Oops! Something went wrong on our end. Please try again later.
        </p>
      </div>
      <div
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          gap: '24px',
        }}
      >
        <SafeIcon src={ErrorIcon} alt='Error Illustration' style={{ width: '200px', height: 'auto' }} />
        <CustomButton variant='tertiary' size='Medium' text='Go to Homepage' onClick={onGoHome} />
      </div>
    </div>
  );
}

// ─── Centralized error reporter ──────────────────────────────────────────────

function reportError(error: Error, info: React.ErrorInfo, boundary: string) {
  console.error(`[${boundary}]`, error.message, {
    componentStack: info.componentStack,
    error,
  });
}

/**
 * Report a handled error (caught inside catch blocks, effects, or event handlers)
 * through the same reporter the ErrorBoundary uses, so all errors share one
 * observability path even when they cannot bubble to the boundary naturally.
 */
export function reportHandledError(error: Error, context: string, metadata?: Record<string, unknown>) {
  console.error(`[${context}]`, error.message, { error, ...metadata });
}

// ─── ErrorBoundary ────────────────────────────────────────────────────────────

interface Props {
  children: React.ReactNode;
  /** Custom fallback UI. If omitted, renders the default inline card. */
  fallback?: React.ReactNode;
  /** Called when an error is caught — useful for logging. */
  onError?: (error: Error, info: React.ErrorInfo) => void;
  /**
   * When this value changes, the error state is reset WITHOUT unmounting children.
   * Use this instead of React's `key` prop when you want error recovery on navigation
   * but don't want to remount the children (e.g. a persistent header).
   */
  resetKey?: string | number;
}

interface State {
  hasError: boolean;
  resetKey?: string | number;
}

export default class ErrorBoundary extends React.Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, resetKey: props.resetKey };
  }

  static getDerivedStateFromError(_error: Error): Partial<State> {
    return { hasError: true };
  }

  static getDerivedStateFromProps(props: Props, state: State): Partial<State> | null {
    if (props.resetKey !== state.resetKey) {
      return { hasError: false, resetKey: props.resetKey };
    }
    return null;
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    reportError(error, info, 'ErrorBoundary');
    this.props.onError?.(error, info);
  }

  handleRetry = () => {
    this.setState({ hasError: false });
  };

  render() {
    if (!this.state.hasError) return this.props.children;

    if (this.props.fallback !== undefined) return this.props.fallback;

    return <InlineFallback onRetry={this.handleRetry} />;
  }
}

// ─── App-level boundary (used in _app.tsx) ────────────────────────────────────

export class AppErrorBoundary extends React.Component<{ children: React.ReactNode }, State> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { hasError: false };
  }

  static getDerivedStateFromError(): State {
    return { hasError: true };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    reportError(error, info, 'AppErrorBoundary');
  }

  handleGoHome = () => {
    this.setState({ hasError: false });
    Router.push('/home');
  };

  render() {
    if (this.state.hasError) return <FullPageFallback onGoHome={this.handleGoHome} />;
    return this.props.children;
  }
}

// ─── HOC for easy component wrapping ─────────────────────────────────────────

export function withErrorBoundary<P extends object>(
  Component: React.ComponentType<P>,
  fallback?: React.ReactNode,
  onError?: (error: Error, info: React.ErrorInfo) => void
): React.FC<P & Record<string, unknown>> {
  const Wrapped: React.FC<P & Record<string, unknown>> = (props) => (
    <ErrorBoundary fallback={fallback} onError={onError}>
      <Component {...(props as P)} />
    </ErrorBoundary>
  );
  Wrapped.displayName = `withErrorBoundary(${Component.displayName ?? Component.name})`;
  return Wrapped;
}
