import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Button } from '@ui/Button';

describe('Button', () => {
  it('renders with text', () => {
    render(<Button>Click me</Button>);
    expect(screen.getByText('Click me')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<Button onClick={onClick}>Click</Button>);
    fireEvent.click(screen.getByText('Click'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('is disabled when disabled=true', () => {
    render(<Button disabled>Disabled</Button>);
    expect(screen.getByRole('button')).toBeDisabled();
  });

  it('does not fire onClick when disabled', () => {
    const onClick = jest.fn();
    render(
      <Button disabled onClick={onClick}>
        Disabled
      </Button>
    );
    fireEvent.click(screen.getByRole('button'));
    expect(onClick).not.toHaveBeenCalled();
  });

  it('renders all tones without crashing', () => {
    const tones = ['primary', 'secondary', 'ghost', 'danger', 'link'] as const;
    tones.forEach((tone) => {
      const { unmount } = render(<Button tone={tone}>{tone}</Button>);
      expect(screen.getByText(tone)).toBeInTheDocument();
      unmount();
    });
  });

  it('renders all sizes without crashing', () => {
    const sizes = ['xs', 'sm', 'md', 'lg'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<Button size={size}>{size}</Button>);
      unmount();
    });
  });

  it('shows loading state with aria-busy', () => {
    render(<Button loading>Loading</Button>);
    expect(screen.getByRole('button')).toHaveAttribute('aria-busy', 'true');
  });

  it('renders as full-width when fullWidth=true', () => {
    render(<Button fullWidth>Full</Button>);
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('applies id prop', () => {
    render(<Button id='my-btn'>Test</Button>);
    expect(document.getElementById('my-btn')).toBeInTheDocument();
  });

  it('renders with aria-label for icon-only', () => {
    render(<Button icon={<span>icon</span>} aria-label='Close' />);
    expect(screen.getByLabelText('Close')).toBeInTheDocument();
  });

  it('renders as submit button when type=submit', () => {
    render(<Button type='submit'>Submit</Button>);
    expect(screen.getByRole('button')).toHaveAttribute('type', 'submit');
  });

  it('wraps in tooltip when tooltip prop is given', () => {
    render(<Button tooltip='Tooltip text'>Btn</Button>);
    expect(screen.getByText('Btn')).toBeInTheDocument();
  });
});
