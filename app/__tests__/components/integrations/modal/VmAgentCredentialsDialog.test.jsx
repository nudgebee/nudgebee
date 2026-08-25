import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import VmAgentCredentialsDialog from '@components/integrations/modal/VmAgentCredentialsDialog';

// Source now uses @ui/Modal (handleClose, title, open, children).
// Old NDialog mock is gone; mock the new Modal and expose a close button.
jest.mock('@ui/Modal', () => ({
  Modal: ({ open, handleClose, title, children }) =>
    open ? (
      <div data-testid='dialog'>
        <span data-testid='dialog-title'>{title}</span>
        <button data-testid='dialog-close' onClick={handleClose} />
        {children}
      </div>
    ) : null,
}));

// CopyButton moved from @components/integrations/modal/CopyButton to @shared/buttons/CopyButton
jest.mock('@shared/buttons/CopyButton', () => ({
  __esModule: true,
  default: ({ text }) => <button data-testid='copy-btn' data-text={text} />,
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: jest.fn().mockReturnValue({ relayUrl: 'https://relay.example.com', signingPublicKey: '' }),
}));

jest.mock('@lib/externalUrls', () => ({
  docsUrl: jest.fn((path) => `https://docs.example.com${path}`),
}));

jest.mock('@utils/colors');

const baseProps = {
  open: true,
  onClose: jest.fn(),
  accessKey: 'test_access_key',
  accessSecret: 'test_access_secret',
};

describe('VmAgentCredentialsDialog', () => {
  describe('rendering', () => {
    it('renders nothing when accessKey is missing', () => {
      render(<VmAgentCredentialsDialog {...baseProps} accessKey={null} />);
      expect(screen.queryByTestId('dialog')).not.toBeInTheDocument();
    });

    it('renders nothing when accessSecret is missing', () => {
      render(<VmAgentCredentialsDialog {...baseProps} accessSecret={null} />);
      expect(screen.queryByTestId('dialog')).not.toBeInTheDocument();
    });

    it('renders dialog when both accessKey and accessSecret are provided', () => {
      render(<VmAgentCredentialsDialog {...baseProps} />);
      expect(screen.getByTestId('dialog')).toBeInTheDocument();
    });

    it('does not render when open=false', () => {
      render(<VmAgentCredentialsDialog {...baseProps} open={false} />);
      expect(screen.queryByTestId('dialog')).not.toBeInTheDocument();
    });

    it('shows accessKey value', () => {
      render(<VmAgentCredentialsDialog {...baseProps} />);
      expect(screen.getAllByText('test_access_key').length).toBeGreaterThan(0);
    });

    it('shows accessSecret value', () => {
      render(<VmAgentCredentialsDialog {...baseProps} />);
      expect(screen.getAllByText('test_access_secret').length).toBeGreaterThan(0);
    });

    it('shows "Save these credentials now" warning', () => {
      render(<VmAgentCredentialsDialog {...baseProps} />);
      expect(screen.getByText(/Save these credentials now/)).toBeInTheDocument();
    });
  });

  describe('deploy method tabs', () => {
    it('shows script deploy content by default', () => {
      render(<VmAgentCredentialsDialog {...baseProps} />);
      expect(screen.getByText(/curl.*install\.sh/s)).toBeInTheDocument();
    });
  });

  describe('close', () => {
    it('calls onClose when dialog is closed', () => {
      const onClose = jest.fn();
      render(<VmAgentCredentialsDialog {...baseProps} onClose={onClose} />);
      fireEvent.click(screen.getByTestId('dialog-close'));
      expect(onClose).toHaveBeenCalledTimes(1);
    });
  });
});
