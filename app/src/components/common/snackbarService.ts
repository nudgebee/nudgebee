import type { ReactNode } from 'react';

export type SnackbarSeverity = 'default' | 'success' | 'info' | 'warning' | 'error';

export interface SnackbarAction {
  /** Action button label. */
  label: string;
  /** Invoked when the action button is clicked. The toast dismisses afterwards. */
  onClick: () => void;
}

export interface SnackbarOptions {
  message: ReactNode;
  severity: SnackbarSeverity;
  /**
   * Optional duration in ms.
   * When omitted, the visual mount applies a severity-based default
   * (default/info: 4000, success: 3000, warning: 5000, error: 6000).
   */
  duration?: number;
  /** Optional secondary line under the message — smaller and gray. */
  description?: ReactNode;
  /** Optional action button (rendered with the Button `secondary` tone) before close. */
  action?: SnackbarAction;
}

/** Per-severity helper options — everything except `message` and `severity`. */
export type SnackbarExtras = Omit<SnackbarOptions, 'message' | 'severity'>;

// Back-compat: the helpers historically accepted a bare `duration` number as the
// second argument. Keep that working while also accepting the richer options bag.
type ExtrasOrDuration = number | SnackbarExtras;

function toExtras(opts?: ExtrasOrDuration): SnackbarExtras {
  if (opts === undefined) return {};
  return typeof opts === 'number' ? { duration: opts } : opts;
}

class SnackbarService {
  private listeners: ((options: SnackbarOptions) => void)[] = [];

  show(message: ReactNode, severity: SnackbarSeverity, opts?: ExtrasOrDuration) {
    const options: SnackbarOptions = { message, severity, ...toExtras(opts) };
    this.listeners.forEach((listener) => listener(options));
  }

  default(message: ReactNode, opts?: ExtrasOrDuration) {
    this.show(message, 'default', opts);
  }

  success(message: ReactNode, opts?: ExtrasOrDuration) {
    this.show(message, 'success', opts);
  }

  error(message: ReactNode, opts?: ExtrasOrDuration) {
    this.show(message, 'error', opts);
  }

  warning(message: ReactNode, opts?: ExtrasOrDuration) {
    this.show(message, 'warning', opts);
  }

  info(message: ReactNode, opts?: ExtrasOrDuration) {
    this.show(message, 'info', opts);
  }

  subscribe(listener: (options: SnackbarOptions) => void) {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }
}

export const snackbar = new SnackbarService();
