import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Card } from '@ui/Card';

describe('Card', () => {
  it('renders children', () => {
    render(<Card>Card body</Card>);
    expect(screen.getByText('Card body')).toBeInTheDocument();
  });

  it('renders header slot', () => {
    render(<Card header={<div>My Header</div>}>body</Card>);
    expect(screen.getByText('My Header')).toBeInTheDocument();
  });

  it('renders footer slot', () => {
    render(<Card footer={<div>Footer</div>}>body</Card>);
    expect(screen.getByText('Footer')).toBeInTheDocument();
  });

  it('renders all variants', () => {
    const variants = ['elevated', 'outlined', 'accent', 'tinted'] as const;
    variants.forEach((variant) => {
      const { unmount } = render(<Card variant={variant}>content</Card>);
      unmount();
    });
  });

  it('renders all sizes', () => {
    const sizes = ['sm', 'md', 'lg'] as const;
    sizes.forEach((size) => {
      const { unmount } = render(<Card size={size}>content</Card>);
      unmount();
    });
  });

  it('renders interactive card with role=button', () => {
    render(
      <Card interactive onClick={jest.fn()}>
        Click me
      </Card>
    );
    expect(screen.getByRole('button')).toBeInTheDocument();
  });

  it('fires onClick when interactive', () => {
    const onClick = jest.fn();
    render(
      <Card interactive onClick={onClick}>
        Click
      </Card>
    );
    fireEvent.click(screen.getByRole('button'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('activates onClick on Enter key for interactive card', () => {
    const onClick = jest.fn();
    render(
      <Card interactive onClick={onClick}>
        Click
      </Card>
    );
    fireEvent.keyDown(screen.getByRole('button'), { key: 'Enter' });
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders selected card', () => {
    render(<Card selected>Selected card</Card>);
    expect(screen.getByText('Selected card')).toBeInTheDocument();
  });

  it('applies custom id', () => {
    render(<Card id='my-card'>test</Card>);
    expect(document.getElementById('my-card')).toBeInTheDocument();
  });

  it('renders flat elevation', () => {
    render(<Card elevation='flat'>flat</Card>);
    expect(screen.getByText('flat')).toBeInTheDocument();
  });
});
