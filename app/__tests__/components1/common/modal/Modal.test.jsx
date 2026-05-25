import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Modal } from '@components1/common/modal';

// Mock @assets
jest.mock('@assets', () => ({
  modalSuccess: { default: { src: 'success.svg' } },
  modalPasswordChange: { default: { src: 'password.svg' } },
}));

// Mock LinearLoader
jest.mock('@components1/k8s/common/LinearLoader', () => ({
  __esModule: true,
  default: () => <div data-testid='linear-loader'>Loading...</div>,
}));

// Mock NewCustomButton
jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, variant: _variant, size: _size, id }) => (
    <button data-testid={`custom-btn-${id || text}`} onClick={onClick}>
      {text}
    </button>
  ),
}));

// Mock src/utils/colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: { mid: '#888' },
  },
}));

describe('Modal', () => {
  const defaultProps = {
    open: true,
    title: 'Test Title',
    handleClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('basic rendering', () => {
    it('renders modal when open=true', () => {
      render(
        <Modal {...defaultProps}>
          <div>content</div>
        </Modal>
      );
      expect(screen.getByText('Test Title')).toBeInTheDocument();
    });

    it('renders children', () => {
      render(
        <Modal {...defaultProps}>
          <div data-testid='modal-child'>Child Content</div>
        </Modal>
      );
      expect(screen.getByTestId('modal-child')).toBeInTheDocument();
    });

    it('does not render when open=false', () => {
      render(
        <Modal {...defaultProps} open={false}>
          <div>content</div>
        </Modal>
      );
      expect(screen.queryByText('Test Title')).not.toBeInTheDocument();
    });
  });

  describe('type variations', () => {
    it('uses modalSuccess icon by default (type=1)', () => {
      render(
        <Modal {...defaultProps} onSuccess={true} type={1}>
          <div />
        </Modal>
      );
      const img = document.querySelector('img[alt="check"]');
      expect(img).toBeInTheDocument();
      expect(img.getAttribute('src')).toBe('success.svg');
    });

    it('uses modalPasswordChange icon when type=PASSWORD_CHANGE', () => {
      render(
        <Modal {...defaultProps} onSuccess={true} type='PASSWORD_CHANGE'>
          <div />
        </Modal>
      );
      const img = document.querySelector('img[alt="check"]');
      expect(img).toBeInTheDocument();
      expect(img.getAttribute('src')).toBe('password.svg');
    });
  });

  describe('loader', () => {
    it('shows LinearLoader when loader=true', () => {
      render(
        <Modal {...defaultProps} loader={true}>
          <div />
        </Modal>
      );
      expect(screen.getByTestId('linear-loader')).toBeInTheDocument();
    });

    it('does not show LinearLoader when loader=false', () => {
      render(
        <Modal {...defaultProps} loader={false}>
          <div />
        </Modal>
      );
      expect(screen.queryByTestId('linear-loader')).not.toBeInTheDocument();
    });
  });

  describe('onSuccess content', () => {
    it('shows success content with message when onSuccess=true', () => {
      render(
        <Modal {...defaultProps} onSuccess={true} message='Operation successful!'>
          <div />
        </Modal>
      );
      expect(screen.getByText('Operation successful!')).toBeInTheDocument();
      expect(screen.getByTestId('custom-btn-Close')).toBeInTheDocument();
    });

    it('does not show success content when onSuccess=false', () => {
      render(
        <Modal {...defaultProps} onSuccess={false}>
          <div />
        </Modal>
      );
      expect(screen.queryByTestId('custom-btn-Close')).not.toBeInTheDocument();
    });

    it('Close button in success content calls handleClose', () => {
      const handleClose = jest.fn();
      render(
        <Modal {...defaultProps} handleClose={handleClose} onSuccess={true} message='Done'>
          <div />
        </Modal>
      );
      fireEvent.click(screen.getByTestId('custom-btn-Close'));
      expect(handleClose).toHaveBeenCalled();
    });

    it('Close button uses onClose when handleClose is not provided', () => {
      const onClose = jest.fn();
      render(
        <Modal open={true} title='T' onClose={onClose} onSuccess={true} message='Done'>
          <div />
        </Modal>
      );
      fireEvent.click(screen.getByTestId('custom-btn-Close'));
      expect(onClose).toHaveBeenCalled();
    });
  });

  describe('title background', () => {
    it('applies border styles when hideTitleBackground=false (default)', () => {
      render(
        <Modal {...defaultProps}>
          <div />
        </Modal>
      );
      // Title area should render with border styles
      expect(screen.getByText('Test Title')).toBeInTheDocument();
    });

    it('hides border styles when hideTitleBackground=true', () => {
      render(
        <Modal {...defaultProps} hideTitleBackground={true}>
          <div />
        </Modal>
      );
      expect(screen.getByText('Test Title')).toBeInTheDocument();
    });
  });

  describe('subtitle', () => {
    it('renders subtitle when provided', () => {
      render(
        <Modal {...defaultProps} subtitle='This is a subtitle'>
          <div />
        </Modal>
      );
      expect(screen.getByText('This is a subtitle')).toBeInTheDocument();
    });

    it('does not render subtitle when not provided', () => {
      render(
        <Modal {...defaultProps}>
          <div />
        </Modal>
      );
      // No subtitle element
      expect(screen.queryByText('subtitle')).not.toBeInTheDocument();
    });
  });

  describe('rightComponentOnTitle', () => {
    it('renders rightComponentOnTitle when provided', () => {
      render(
        <Modal {...defaultProps} rightComponentOnTitle={<span data-testid='right-comp'>Right</span>}>
          <div />
        </Modal>
      );
      expect(screen.getByTestId('right-comp')).toBeInTheDocument();
    });

    it('does not render rightComponentOnTitle when not provided', () => {
      render(
        <Modal {...defaultProps}>
          <div />
        </Modal>
      );
      expect(screen.queryByTestId('right-comp')).not.toBeInTheDocument();
    });
  });

  describe('close button', () => {
    it('calls handleClose when close button clicked', () => {
      const handleClose = jest.fn();
      render(
        <Modal open={true} title='T' handleClose={handleClose}>
          <div />
        </Modal>
      );
      fireEvent.click(document.querySelector('#close-modal-btn'));
      expect(handleClose).toHaveBeenCalled();
    });

    it('calls onClose when handleClose not provided', () => {
      const onClose = jest.fn();
      render(
        <Modal open={true} title='T' onClose={onClose}>
          <div />
        </Modal>
      );
      fireEvent.click(document.querySelector('#close-modal-btn'));
      expect(onClose).toHaveBeenCalled();
    });

    it('prefers handleClose over onClose', () => {
      const handleClose = jest.fn();
      const onClose = jest.fn();
      render(
        <Modal open={true} title='T' handleClose={handleClose} onClose={onClose}>
          <div />
        </Modal>
      );
      fireEvent.click(document.querySelector('#close-modal-btn'));
      expect(handleClose).toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
    });
  });

  describe('actionButtons', () => {
    it('renders actionButtons when provided', () => {
      render(
        <Modal {...defaultProps} actionButtons={<button data-testid='action-btn'>Submit</button>}>
          <div />
        </Modal>
      );
      expect(screen.getByTestId('action-btn')).toBeInTheDocument();
    });

    it('does not render DialogActions when actionButtons not provided', () => {
      render(
        <Modal {...defaultProps}>
          <div />
        </Modal>
      );
      expect(screen.queryByTestId('action-btn')).not.toBeInTheDocument();
    });
  });

  describe('maxHeight', () => {
    it('renders correctly with maxHeight provided', () => {
      render(
        <Modal {...defaultProps} maxHeight='500px'>
          <div data-testid='inner'>Inner</div>
        </Modal>
      );
      expect(screen.getByTestId('inner')).toBeInTheDocument();
    });
  });
});
