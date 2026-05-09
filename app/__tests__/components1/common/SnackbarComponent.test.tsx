import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import { SnackbarComponent } from '@components1/common/SnackbarComponent';

let snackbarCallback: ((opts: { message: string; severity: string }) => void) | undefined;

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: {
    subscribe: jest.fn((cb) => {
      snackbarCallback = cb;
      return () => {};
    }),
    success: jest.fn(),
    error: jest.fn(),
  },
}));

describe('SnackbarComponent', () => {
  beforeEach(() => {
    snackbarCallback = undefined;
  });

  test('renders null initially (nothing visible)', () => {
    const { container } = render(<SnackbarComponent />);
    // When open is false, the component returns null
    expect(container.firstChild).toBeNull();
  });

  test('shows snackbar when subscribe callback is called with a message', () => {
    render(<SnackbarComponent />);
    act(() => {
      snackbarCallback?.({ message: 'Test message', severity: 'success' });
    });
    expect(screen.getByRole('alert')).toBeInTheDocument();
  });

  test('displays the message text', () => {
    render(<SnackbarComponent />);
    act(() => {
      snackbarCallback?.({ message: 'Hello world', severity: 'info' });
    });
    expect(screen.getByText('Hello world')).toBeInTheDocument();
  });

  test('shows correct severity for success', () => {
    render(<SnackbarComponent />);
    act(() => {
      snackbarCallback?.({ message: 'Success!', severity: 'success' });
    });
    const alert = screen.getByRole('alert');
    expect(alert).toBeInTheDocument();
    // MUI Alert with severity success has class containing "success"
    expect(alert.className).toMatch(/success/i);
  });

  test('shows correct severity for error', () => {
    render(<SnackbarComponent />);
    act(() => {
      snackbarCallback?.({ message: 'Error occurred', severity: 'error' });
    });
    const alert = screen.getByRole('alert');
    expect(alert.className).toMatch(/error/i);
  });

  test('hides after close button clicked', () => {
    const { container } = render(<SnackbarComponent />);
    act(() => {
      snackbarCallback?.({ message: 'Close me', severity: 'success' });
    });
    expect(screen.getByRole('alert')).toBeInTheDocument();

    const closeButton = screen.getByTitle('Close');
    act(() => {
      fireEvent.click(closeButton);
    });
    expect(container.firstChild).toBeNull();
  });
});
