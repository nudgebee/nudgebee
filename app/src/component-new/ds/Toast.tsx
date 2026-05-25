/**
 * Toast — DS V2 of legacy snackbar / SnackbarComponent.
 *
 * Re-exports both the global service singleton (`toast` / `snackbar`) and the
 * mountable React component. New code should import from this path:
 *   import { toast } from '@components1/ds/Toast';
 *   toast.success('Saved');
 *   toast.error('Failed');
 *   <Toast />  // mount once in _app.tsx
 *
 * The mount subscribes to BOTH the new singleton (this re-export) AND the
 * legacy `@common/snackbarService` singleton, so non-migrated callers get the
 * new visual automatically.
 *
 * Severity-based default durations (when caller omits `duration`):
 *   success: 3000ms · info: 4000ms · warning: 6000ms · error: persistent (no auto-dismiss)
 */
import { snackbar } from '@common-new/snackbarService';
import { SnackbarComponent } from '@common-new/SnackbarComponent';

export { snackbar, snackbar as toast };
export { SnackbarComponent as Toast };
export default SnackbarComponent;
