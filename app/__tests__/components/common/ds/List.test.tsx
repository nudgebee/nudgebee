jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));

import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { List } from '@ui/List';

describe('List', () => {
  it('renders all string items', () => {
    render(<List items={['Apple', 'Banana', 'Cherry']} />);
    expect(screen.getByText('Apple')).toBeInTheDocument();
    expect(screen.getByText('Banana')).toBeInTheDocument();
    expect(screen.getByText('Cherry')).toBeInTheDocument();
  });

  it('renders list role', () => {
    render(<List items={['Item 1', 'Item 2']} />);
    expect(screen.getByRole('list')).toBeInTheDocument();
  });

  it('renders custom renderItem', () => {
    const items = [
      { id: 1, name: 'Pod A' },
      { id: 2, name: 'Pod B' },
    ];
    render(<List items={items} renderItem={(item) => <span data-testid={`item-${item.id}`}>{item.name}</span>} />);
    expect(screen.getByTestId('item-1')).toBeInTheDocument();
    expect(screen.getByTestId('item-2')).toBeInTheDocument();
  });

  it('renders empty state when items array is empty', () => {
    render(<List items={[]} empty={<div>No items found</div>} />);
    expect(screen.getByText('No items found')).toBeInTheDocument();
  });

  it('returns null when items are empty and no empty prop', () => {
    const { container } = render(<List items={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('renders bullet points when bullet=true', () => {
    const { container } = render(<List items={['A', 'B']} bullet />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('truncates items and shows show-more button', () => {
    const items = ['A', 'B', 'C', 'D', 'E', 'F', 'G'];
    render(<List items={items} truncate={{ show: 3 }} />);
    expect(screen.queryByText('G')).not.toBeInTheDocument();
    expect(screen.getByText(/Show.*more/i)).toBeInTheDocument();
  });

  it('expands items when show-more button clicked', () => {
    const items = ['A', 'B', 'C', 'D', 'E', 'F', 'G'];
    render(<List items={items} truncate={{ show: 3 }} />);
    fireEvent.click(screen.getByText(/Show.*more/i));
    expect(screen.getByText('G')).toBeInTheDocument();
  });

  it('collapses items when show-less button clicked', () => {
    const items = ['A', 'B', 'C', 'D', 'E'];
    render(<List items={items} truncate={{ show: 2 }} />);
    fireEvent.click(screen.getByText(/Show.*more/i));
    fireEvent.click(screen.getByText(/Show less/i));
    expect(screen.queryByText('E')).not.toBeInTheDocument();
  });

  it('calls onItemClick when item is clicked in bullet mode', () => {
    const onItemClick = jest.fn();
    render(<List items={['Click me']} onItemClick={onItemClick} />);
    fireEvent.click(screen.getByText('Click me'));
    expect(onItemClick).toHaveBeenCalledWith('Click me', 0);
  });

  it('renders listitem roles', () => {
    render(<List items={['Item 1', 'Item 2']} />);
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
  });
});
