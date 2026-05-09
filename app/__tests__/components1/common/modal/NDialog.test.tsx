import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import NDialog from '@components1/common/modal/NDialog';

// Mock LinearLoader (must be before Modal mock that references it)
jest.mock('@components1/k8s/common/LinearLoader', () => ({
  __esModule: true,
  default: () => <div data-testid='linear-loader'>Loading...</div>,
}));

// Mock Modal component
jest.mock('@components1/common/modal', () => ({
  __esModule: true,
  Modal: ({
    open,
    handleClose,
    children,
    title,
    width,
    loader,
  }: {
    open: boolean;
    handleClose: (e: string, r: string) => void;
    children: React.ReactNode;
    title: React.ReactNode;
    width: string;
    loader: boolean;
  }) => {
    if (!open) return null;
    const LinearLoader = require('@components1/k8s/common/LinearLoader').default;
    return (
      <div data-testid='mock-modal' data-width={width}>
        <div data-testid='modal-title'>{title}</div>
        {loader && <LinearLoader />}
        <div data-testid='modal-content'>{children}</div>
        <button data-testid='modal-backdrop-btn' onClick={() => handleClose('event', 'backdropClick')}>
          Backdrop
        </button>
        <button data-testid='modal-escape-btn' onClick={() => handleClose('event', 'escapeKeyDown')}>
          Escape
        </button>
        <button data-testid='modal-programmatic-close' onClick={() => handleClose('event', 'programmatic')}>
          Close Programmatic
        </button>
      </div>
    );
  },
}));

// Mock NewCustomButton
jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({
    text,
    onClick,
    variant: _variant,
    id,
    type,
    disabled,
  }: {
    text: string;
    onClick: () => void;
    variant: string;
    id: string;
    type: 'button' | 'submit' | 'reset';
    disabled: boolean;
  }) => (
    <button data-testid={`btn-${id}`} onClick={onClick} disabled={disabled} type={type}>
      {text}
    </button>
  ),
}));

describe('NDialog', () => {
  const defaultProps = {
    open: true,
    dialogTitle: 'Test Dialog',
    dialogContent: <span>Dialog Content</span>,
    additionalComponent: null,
    handleClose: jest.fn(),
    handleSubmit: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('basic rendering', () => {
    it('renders dialog when open=true', () => {
      render(<NDialog {...defaultProps} />);
      expect(screen.getByTestId('mock-modal')).toBeInTheDocument();
    });

    it('does not render when open=false', () => {
      render(<NDialog {...defaultProps} open={false} />);
      expect(screen.queryByTestId('mock-modal')).not.toBeInTheDocument();
    });

    it('renders dialog title', () => {
      render(<NDialog {...defaultProps} />);
      expect(screen.getByTestId('modal-title')).toHaveTextContent('Test Dialog');
    });
  });

  describe('loading state', () => {
    it('shows LinearLoader when loading=true', () => {
      render(<NDialog {...defaultProps} loading={true} />);
      expect(screen.getByTestId('linear-loader')).toBeInTheDocument();
    });

    it('does not show LinearLoader when loading=false', () => {
      render(<NDialog {...defaultProps} loading={false} />);
      expect(screen.queryByTestId('linear-loader')).not.toBeInTheDocument();
    });
  });

  describe('dialogContent', () => {
    it('renders dialogContent when provided', () => {
      render(<NDialog {...defaultProps} dialogContent={<span data-testid='dialog-text'>Content Here</span>} />);
      expect(screen.getByTestId('dialog-text')).toBeInTheDocument();
    });

    it('renders empty fragment when dialogContent is not provided', () => {
      render(<NDialog {...defaultProps} dialogContent={undefined} />);
      // No dialog content text element, but dialog still renders
      expect(screen.getByTestId('mock-modal')).toBeInTheDocument();
    });

    it('renders empty fragment when dialogContent is null', () => {
      render(<NDialog {...defaultProps} dialogContent={null} />);
      expect(screen.getByTestId('mock-modal')).toBeInTheDocument();
    });
  });

  describe('additionalComponent', () => {
    it('renders additionalComponent when provided', () => {
      render(<NDialog {...defaultProps} additionalComponent={<div data-testid='extra-component'>Extra</div>} />);
      expect(screen.getByTestId('extra-component')).toBeInTheDocument();
    });

    it('does not render additionalComponent when null', () => {
      render(<NDialog {...defaultProps} additionalComponent={null} />);
      expect(screen.queryByTestId('extra-component')).not.toBeInTheDocument();
    });

    it('does not render additionalComponent when undefined', () => {
      render(<NDialog {...defaultProps} additionalComponent={undefined} />);
      expect(screen.queryByTestId('extra-component')).not.toBeInTheDocument();
    });
  });

  describe('isCancelRequired and isSubmitRequired', () => {
    it('shows both cancel and submit buttons by default', () => {
      render(<NDialog {...defaultProps} />);
      expect(screen.getByTestId('btn-cancel')).toBeInTheDocument();
      expect(screen.getByTestId('btn-submit')).toBeInTheDocument();
    });

    it('hides cancel button when isCancelRequired=false', () => {
      render(<NDialog {...defaultProps} isCancelRequired={false} />);
      expect(screen.queryByTestId('btn-cancel')).not.toBeInTheDocument();
      expect(screen.getByTestId('btn-submit')).toBeInTheDocument();
    });

    it('hides submit button when isSubmitRequired=false', () => {
      render(<NDialog {...defaultProps} isSubmitRequired={false} />);
      expect(screen.getByTestId('btn-cancel')).toBeInTheDocument();
      expect(screen.queryByTestId('btn-submit')).not.toBeInTheDocument();
    });

    it('hides both buttons when both are false', () => {
      render(<NDialog {...defaultProps} isCancelRequired={false} isSubmitRequired={false} />);
      expect(screen.queryByTestId('btn-cancel')).not.toBeInTheDocument();
      expect(screen.queryByTestId('btn-submit')).not.toBeInTheDocument();
    });
  });

  describe('button interactions', () => {
    it('calls handleClose when cancel button clicked', () => {
      const handleClose = jest.fn();
      render(<NDialog {...defaultProps} handleClose={handleClose} />);
      fireEvent.click(screen.getByTestId('btn-cancel'));
      expect(handleClose).toHaveBeenCalled();
    });

    it('calls handleSubmit when submit button clicked', () => {
      const handleSubmit = jest.fn();
      render(<NDialog {...defaultProps} handleSubmit={handleSubmit} />);
      fireEvent.click(screen.getByTestId('btn-submit'));
      expect(handleSubmit).toHaveBeenCalled();
    });

    it('submit button is disabled when disabled=true', () => {
      render(<NDialog {...defaultProps} disabled={true} />);
      expect(screen.getByTestId('btn-submit')).toBeDisabled();
    });

    it('submit button shows custom buttonText', () => {
      render(<NDialog {...defaultProps} buttonText='Confirm' />);
      expect(screen.getByTestId('btn-submit')).toHaveTextContent('Confirm');
    });
  });

  describe('backdropClickClose', () => {
    it('does NOT call handleClose on backdrop click when backdropClickClose=false', () => {
      const handleClose = jest.fn();
      render(<NDialog {...defaultProps} handleClose={handleClose} backdropClickClose={false} />);
      fireEvent.click(screen.getByTestId('modal-backdrop-btn'));
      expect(handleClose).not.toHaveBeenCalled();
    });

    it('does NOT call handleClose on escape key when backdropClickClose=false', () => {
      const handleClose = jest.fn();
      render(<NDialog {...defaultProps} handleClose={handleClose} backdropClickClose={false} />);
      fireEvent.click(screen.getByTestId('modal-escape-btn'));
      expect(handleClose).not.toHaveBeenCalled();
    });

    it('calls handleClose on backdrop click when backdropClickClose=true', () => {
      const handleClose = jest.fn();
      render(<NDialog {...defaultProps} handleClose={handleClose} backdropClickClose={true} />);
      fireEvent.click(screen.getByTestId('modal-backdrop-btn'));
      expect(handleClose).toHaveBeenCalled();
    });

    it('calls handleClose on escape key when backdropClickClose=true', () => {
      const handleClose = jest.fn();
      render(<NDialog {...defaultProps} handleClose={handleClose} backdropClickClose={true} />);
      fireEvent.click(screen.getByTestId('modal-escape-btn'));
      expect(handleClose).toHaveBeenCalled();
    });

    it('calls handleClose for non-backdrop/escape reason when backdropClickClose=false', () => {
      const handleClose = jest.fn();
      render(<NDialog {...defaultProps} handleClose={handleClose} backdropClickClose={false} />);
      fireEvent.click(screen.getByTestId('modal-programmatic-close'));
      expect(handleClose).toHaveBeenCalled();
    });

    it('does not throw when handleClose is undefined on close', () => {
      render(<NDialog {...defaultProps} handleClose={undefined} backdropClickClose={true} />);
      // Should not throw
      fireEvent.click(screen.getByTestId('modal-backdrop-btn'));
    });
  });

  describe('width prop', () => {
    it('passes width to Modal', () => {
      render(<NDialog {...defaultProps} width='lg' />);
      expect(screen.getByTestId('mock-modal')).toHaveAttribute('data-width', 'lg');
    });

    it('defaults to md width', () => {
      render(<NDialog {...defaultProps} />);
      expect(screen.getByTestId('mock-modal')).toHaveAttribute('data-width', 'md');
    });
  });
});
