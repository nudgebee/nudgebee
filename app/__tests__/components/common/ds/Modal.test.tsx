jest.mock('@components/k8s/common/LinearLoader', () => ({
  __esModule: true,
  default: () => <div data-testid='linear-loader' />,
}));

import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Modal } from '@ui/Modal';

describe('Modal', () => {
  it('renders modal content when open=true', () => {
    render(
      <Modal open={true} handleClose={jest.fn()} title='My Dialog'>
        Modal body
      </Modal>
    );
    expect(screen.getByText('Modal body')).toBeInTheDocument();
  });

  it('does not render modal content when open=false', () => {
    render(
      <Modal open={false} handleClose={jest.fn()} title='My Dialog'>
        Hidden body
      </Modal>
    );
    expect(screen.queryByText('Hidden body')).not.toBeInTheDocument();
  });

  it('renders title', () => {
    render(
      <Modal open={true} handleClose={jest.fn()} title='Confirm Action'>
        body
      </Modal>
    );
    expect(screen.getByText('Confirm Action')).toBeInTheDocument();
  });

  it('renders subtitle when provided', () => {
    render(
      <Modal open={true} handleClose={jest.fn()} title='Title' subtitle='Subtitle here'>
        body
      </Modal>
    );
    expect(screen.getByText('Subtitle here')).toBeInTheDocument();
  });

  it('calls handleClose when close button clicked', () => {
    const handleClose = jest.fn();
    render(
      <Modal open={true} handleClose={handleClose} title='Close me'>
        body
      </Modal>
    );
    fireEvent.click(screen.getByRole('button', { name: /close/i }));
    expect(handleClose).toHaveBeenCalledTimes(1);
  });

  it('renders confirm button when confirmText and onConfirm are provided', () => {
    render(
      <Modal open={true} handleClose={jest.fn()} title='Delete?' confirmText='Delete' onConfirm={jest.fn()}>
        Are you sure?
      </Modal>
    );
    expect(screen.getByText('Delete')).toBeInTheDocument();
  });

  it('calls onConfirm when confirm button is clicked', () => {
    const onConfirm = jest.fn();
    render(
      <Modal open={true} handleClose={jest.fn()} title='OK?' confirmText='OK' onConfirm={onConfirm}>
        body
      </Modal>
    );
    fireEvent.click(screen.getByText('OK'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('shows loader when loader=true', () => {
    render(
      <Modal open={true} handleClose={jest.fn()} title='Loading' loader>
        body
      </Modal>
    );
    expect(screen.getByTestId('linear-loader')).toBeInTheDocument();
  });

  it('renders custom actionButtons', () => {
    render(
      <Modal open={true} handleClose={jest.fn()} title='Actions' actionButtons={<button>Custom Action</button>}>
        body
      </Modal>
    );
    expect(screen.getByText('Custom Action')).toBeInTheDocument();
  });

  describe('content-driven height animation', () => {
    const setScrollHeight = (value: number) => {
      Object.defineProperty(HTMLElement.prototype, 'scrollHeight', { configurable: true, value });
    };

    afterEach(() => {
      delete (HTMLElement.prototype as { scrollHeight?: number }).scrollHeight;
    });

    it('sizes the body wrapper to the measured content height when maxHeight is not set', () => {
      setScrollHeight(240);
      render(
        <Modal open={true} handleClose={jest.fn()} title='Resizable'>
          body content
        </Modal>
      );
      const dialogContent = document.querySelector('.MuiDialogContent-root') as HTMLElement;
      expect(dialogContent).not.toBeNull();
      expect(dialogContent.parentElement).toHaveClass('MuiBox-root');
      expect(dialogContent.parentElement).toHaveStyle({ height: '240px' });
    });

    it('re-measures and grows the wrapper height when content changes size across a re-render', () => {
      setScrollHeight(120);
      const { rerender } = render(
        <Modal open={true} handleClose={jest.fn()} title='Resizable'>
          short content
        </Modal>
      );
      let dialogContent = document.querySelector('.MuiDialogContent-root') as HTMLElement;
      expect(dialogContent.parentElement).toHaveStyle({ height: '120px' });

      // Simulates a table swapping its loading skeleton for real, taller rows.
      setScrollHeight(600);
      rerender(
        <Modal open={true} handleClose={jest.fn()} title='Resizable'>
          much taller content after data loads
        </Modal>
      );
      dialogContent = document.querySelector('.MuiDialogContent-root') as HTMLElement;
      expect(dialogContent.parentElement).toHaveStyle({ height: '600px' });
    });

    it('skips the measured wrapper and keeps the fixed-height behavior when maxHeight is set', () => {
      setScrollHeight(9999);
      render(
        <Modal open={true} handleClose={jest.fn()} title='Fixed height' maxHeight='400px'>
          body content
        </Modal>
      );
      const dialogContent = document.querySelector('.MuiDialogContent-root') as HTMLElement;
      expect(dialogContent).not.toBeNull();
      expect(dialogContent.parentElement).not.toHaveClass('MuiBox-root');
      expect(dialogContent).toHaveStyle({ height: '100%', maxHeight: '400px' });
    });
  });
});
