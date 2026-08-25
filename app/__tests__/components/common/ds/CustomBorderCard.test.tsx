import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { CustomBorderCard } from '@ui/CustomBorderCard';

describe('CustomBorderCard', () => {
  it('renders children', () => {
    render(<CustomBorderCard>Card content</CustomBorderCard>);
    expect(screen.getByText('Card content')).toBeInTheDocument();
  });

  it('applies borderColor', () => {
    const { container } = render(<CustomBorderCard borderColor='#ff0000'>content</CustomBorderCard>);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('shows left border when showLeftBorder=true', () => {
    const { container } = render(
      <CustomBorderCard showLeftBorder borderLeftColor='#0000ff'>
        content
      </CustomBorderCard>
    );
    expect(container.firstChild).toBeInTheDocument();
  });

  it('applies custom borderLeftWidth', () => {
    const { container } = render(
      <CustomBorderCard showLeftBorder borderLeftWidth={4}>
        content
      </CustomBorderCard>
    );
    expect(container.firstChild).toBeInTheDocument();
  });

  it('applies custom padding', () => {
    const { container } = render(<CustomBorderCard padding={16}>content</CustomBorderCard>);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<CustomBorderCard onClick={onClick}>Clickable</CustomBorderCard>);
    fireEvent.click(screen.getByText('Clickable'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders multiple children', () => {
    render(
      <CustomBorderCard>
        <span>First</span>
        <span>Second</span>
      </CustomBorderCard>
    );
    expect(screen.getByText('First')).toBeInTheDocument();
    expect(screen.getByText('Second')).toBeInTheDocument();
  });
});
