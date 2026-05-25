import type { ReactNode } from 'react';

export type SnackbarSeverity = 'success' | 'info' | 'warning' | 'error';

export interface SnackbarOptions {
  message: ReactNode;
  severity: SnackbarSeverity;
  /**
   * Optional duration in ms.
   * When omitted, the visual mount applies a severity-based default
   * (success: 3000, info: 4000, warning: 6000, error: persistent).
   */
  duration?: number;
}

class SnackbarService {
  private listeners: ((options: SnackbarOptions) => void)[] = [];

  show(message: ReactNode, severity: SnackbarSeverity, duration?: number) {
    this.listeners.forEach((listener) => listener({ message, severity, duration }));
  }

  success(message: ReactNode, duration?: number) {
    this.show(message, 'success', duration);
  }

  error(message: ReactNode, duration?: number) {
    this.show(message, 'error', duration);
  }

  warning(message: ReactNode, duration?: number) {
    this.show(message, 'warning', duration);
  }

  info(message: ReactNode, duration?: number) {
    this.show(message, 'info', duration);
  }

  subscribe(listener: (options: SnackbarOptions) => void) {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }
}

export const snackbar = new SnackbarService();
