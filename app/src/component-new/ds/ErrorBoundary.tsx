/**
 * ErrorBoundary — DS rename of legacy ErrorBoundary + withErrorBoundary HOC.
 * Spec: app/design-system/primitives/feedback/error-boundary.html
 *
 * Re-exports the existing ErrorBoundary class, AppErrorBoundary, withErrorBoundary HOC,
 * InlineFallback component, and reportHandledError utility. Prop API preserved.
 */
export { default, AppErrorBoundary, withErrorBoundary, InlineFallback, reportHandledError } from '@components1/common/ErrorBoundary';
