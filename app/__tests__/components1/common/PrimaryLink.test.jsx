import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import PrimaryLink from '@components1/common/PrimaryLink';

describe('PrimaryLink', () => {
  it('renders children text', () => {
    render(<PrimaryLink>Click here</PrimaryLink>);
    expect(screen.getByText('Click here')).toBeInTheDocument();
  });

  it('renders children as React nodes', () => {
    render(
      <PrimaryLink>
        <span data-testid='child-node'>Node</span>
      </PrimaryLink>
    );
    expect(screen.getByTestId('child-node')).toBeInTheDocument();
  });

  it('calls onClick when clicked', () => {
    const onClick = jest.fn();
    render(<PrimaryLink onClick={onClick}>Click me</PrimaryLink>);
    fireEvent.click(screen.getByText('Click me'));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('applies custom style', () => {
    const style = { color: 'red', fontSize: '20px' };
    render(<PrimaryLink style={style}>Styled</PrimaryLink>);
    expect(screen.getByText('Styled')).toBeInTheDocument();
  });

  it('renders without onClick without crashing', () => {
    render(<PrimaryLink>No handler</PrimaryLink>);
    const el = screen.getByText('No handler');
    expect(el).toBeInTheDocument();
    fireEvent.click(el);
  });

  it('renders without style prop without crashing', () => {
    render(<PrimaryLink>No style</PrimaryLink>);
    expect(screen.getByText('No style')).toBeInTheDocument();
  });
});
