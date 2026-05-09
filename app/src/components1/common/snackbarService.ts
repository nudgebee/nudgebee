import type { ReactNode } from 'react';

type SnackbarOptions = {
  message: ReactNode;
  severity: 'success' | 'info' | 'warning' | 'error';
  duration?: number;
};

class SnackbarService {
  private listeners: ((options: SnackbarOptions) => void)[] = [];

  show(message: ReactNode, severity: 'success' | 'info' | 'warning' | 'error', duration = 5000) {
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
