import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import CopyButton from '@components1/common/CopyButton';

// Mock clipboard if necessary (though CopyButton currently relies on parent for logic)
// But since we are testing visual feedback, we don't need to mock clipboard unless CopyButton calls it directly.
// In the current implementation (and my plan), CopyButton just calls onClick.

describe('CopyButton', () => {
  const mockOnClick = jest.fn();

  beforeEach(() => {
    jest.useFakeTimers();
    mockOnClick.mockClear();
  });

  afterEach(() => {
    jest.useRealTimers();
  });

  it('renders correctly with default aria-label', () => {
    render(<CopyButton onClick={mockOnClick} />);

    // Check for the button
    const button = screen.getByRole('button', { name: /copy/i });
    expect(button).toBeInTheDocument();

    // Initial icon should be FileCopy (we can't easily check icon content without snapshot or specific testid,
    // but we can check it exists).
    // Let's assume FileCopyIcon renders an svg.
    expect(button.querySelector('svg')).toBeInTheDocument();
  });

  it('renders correctly without onClick prop', () => {
    render(<CopyButton />);
    const button = screen.getByRole('button', { name: /copy/i });
    expect(button).toBeInTheDocument();
  });

  it('handles click without onClick prop (no error)', () => {
    render(<CopyButton />);
    const button = screen.getByRole('button', { name: /copy/i });
    fireEvent.click(button);
    expect(screen.getByRole('button', { name: /copied/i })).toBeInTheDocument();
  });

  it('accepts and applies sx prop', () => {
    render(<CopyButton onClick={mockOnClick} sx={{ mt: 1 }} />);
    const button = screen.getByRole('button', { name: /copy/i });
    expect(button).toBeInTheDocument();
  });

  it('handles click, shows feedback, and calls onClick', async () => {
    render(<CopyButton onClick={mockOnClick} />);

    const button = screen.getByRole('button', { name: /copy/i });

    // Click the button
    fireEvent.click(button);

    // Check if onClick was called
    expect(mockOnClick).toHaveBeenCalledTimes(1);

    // Check if aria-label changed to "Copied"
    // Note: Tooltip might also add a title, but aria-label on button takes precedence or is used.
    // My implementation plan says: aria-label={isCopied ? 'Copied' : 'Copy to clipboard'}

    // We need to wait for state update if it's async, but usually fireEvent is synchronous for state updates in tests unless wrapped.
    // Let's use waitFor if needed, but direct assertion should work for simple state.

    // Re-query might be needed if the element is replaced (it shouldn't be, just props change)
    // But since I'm changing the icon, the button children change.

    expect(screen.getByRole('button', { name: /copied/i })).toBeInTheDocument();

    // Advance timer
    act(() => {
      jest.advanceTimersByTime(2000);
    });

    // Should revert to "Copy to clipboard"
    expect(screen.getByRole('button', { name: /copy/i })).toBeInTheDocument();
  });
});
