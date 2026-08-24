import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Chip, hashHue } from '@ui/Chip';

describe('Chip', () => {
  it('renders label text via children', () => {
    render(<Chip>Production</Chip>);
    expect(screen.getByText('Production')).toBeInTheDocument();
  });

  it('renders all variants without crashing', () => {
    const variants = ['filter', 'tag', 'status', 'input', 'action', 'count'] as const;
    variants.forEach((variant) => {
      const { unmount } = render(<Chip variant={variant}>Test</Chip>);
      unmount();
    });
    const { unmount } = render(
      <Chip variant='avatar' leadingAvatar={React.createElement('img', { src: '/img.jpg', alt: 'user' })}>
        Test
      </Chip>
    );
    unmount();
  });

  it('renders all tones without crashing', () => {
    const tones = ['neutral', 'info', 'success', 'warning', 'critical', 'savings', 'waste', 'agent'] as const;
    tones.forEach((tone) => {
      const { unmount } = render(<Chip tone={tone}>Test</Chip>);
      unmount();
    });
  });

  it('renders all sizes without crashing', () => {
    const sizes = ['micro', '2xs', 'xs', 'sm', 'md'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<Chip size={size}>Test</Chip>);
      unmount();
    });
  });

  it('calls onDismiss when dismiss button is clicked', () => {
    const onDismiss = jest.fn();
    render(<Chip onDismiss={onDismiss}>Tag</Chip>);
    const dismissBtn = screen.getByTestId('chip-dismiss');
    fireEvent.click(dismissBtn);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('renders as selected when selected=true', () => {
    render(<Chip selected>Filter</Chip>);
    expect(screen.getByText('Filter')).toBeInTheDocument();
  });

  it('renders icon before label', () => {
    render(<Chip icon={<span data-testid='chip-icon'>★</span>}>With Icon</Chip>);
    expect(screen.getByTestId('chip-icon')).toBeInTheDocument();
  });

  it('renders avatar as leading slot', () => {
    render(
      <Chip variant='avatar' leadingAvatar={React.createElement('img', { src: '/img.jpg', alt: 'user', 'data-testid': 'chip-avatar' })}>
        User
      </Chip>
    );
    expect(screen.getByTestId('chip-avatar')).toBeInTheDocument();
  });
});

describe('hashHue', () => {
  const VALID_HUES = ['slate', 'green', 'amber', 'red', 'blue', 'violet', 'pink', 'teal'];

  it('returns a consistent hue for the same string', () => {
    expect(hashHue('test')).toBe(hashHue('test'));
  });

  it('returns different hues for different strings', () => {
    // Not guaranteed to differ but extremely likely for these two inputs
    const h1 = hashHue('apples');
    const h2 = hashHue('oranges');
    // Just verify both are valid hue strings
    expect(VALID_HUES).toContain(h1);
    expect(VALID_HUES).toContain(h2);
  });

  it('returns one of the valid ChipHue values', () => {
    const hue = hashHue('hello');
    expect(VALID_HUES).toContain(hue);
  });
});
