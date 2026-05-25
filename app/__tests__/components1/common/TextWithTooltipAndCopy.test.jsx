import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import TextWithTooltipAndCopy from '@components1/common/TextWithTooltipAndCopy';

// Mock the assets
jest.mock(
  '@assets',
  () => ({
    CopyIcon: { src: 'copy-icon.svg' },
  }),
  { virtual: true }
);

// Mock clipboard
Object.assign(navigator, {
  clipboard: {
    writeText: jest.fn(),
  },
});

describe('TextWithTooltipAndCopy', () => {
  const defaultProps = {
    value: 'Some text to copy',
    maxSize: 100,
  };

  it('renders text', () => {
    render(<TextWithTooltipAndCopy {...defaultProps} />);
    expect(screen.getByText('Some text to copy')).toBeInTheDocument();
  });

  it('has an accessible copy button', async () => {
    render(<TextWithTooltipAndCopy {...defaultProps} />);

    // This is expected to fail before the fix
    const button = screen.getByRole('button', { name: /copy to clipboard/i });
    expect(button).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(button);
    });

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('Some text to copy');

    // After copy, label should change
    const copiedButton = screen.getByRole('button', { name: /copied/i });
    expect(copiedButton).toBeInTheDocument();
  });
});
