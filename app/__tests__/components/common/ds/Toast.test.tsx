jest.mock('@shared/snackbarService', () => ({
  __esModule: true,
  snackbar: {
    success: jest.fn(),
    error: jest.fn(),
    warning: jest.fn(),
    info: jest.fn(),
  },
}));

jest.mock('@shared/SnackbarComponent', () => ({
  __esModule: true,
  SnackbarComponent: () => <div data-testid='snackbar-component' />,
  default: () => <div data-testid='snackbar-default' />,
}));

import { toast, snackbar, Toast } from '@ui/Toast';
import React from 'react';
import { render, screen } from '@testing-library/react';

describe('Toast exports', () => {
  it('exports toast as the snackbar singleton', () => {
    expect(toast).toBeDefined();
    expect(typeof toast.success).toBe('function');
    expect(typeof toast.error).toBe('function');
  });

  it('exports snackbar as the same object as toast', () => {
    expect(toast).toBe(snackbar);
  });

  it('exports Toast as SnackbarComponent', () => {
    expect(Toast).toBeDefined();
  });
});

describe('Toast component', () => {
  it('renders the SnackbarComponent without crashing', () => {
    render(<Toast />);
    expect(screen.getByTestId('snackbar-component')).toBeInTheDocument();
  });
});
