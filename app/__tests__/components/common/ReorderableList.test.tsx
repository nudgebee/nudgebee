import { render, screen } from '@testing-library/react';
import { ReorderableList } from '@shared/ReorderableList';

const items = ['Apple', 'Banana', 'Cherry'];

const renderItem = (item: string, _index: number, dragHandleProps: any) => (
  <div key={item} data-testid={`item-${item}`}>
    <span {...dragHandleProps} data-testid={`handle-${item}`}>
      ⠿
    </span>
    <span>{item}</span>
  </div>
);

const getItemKey = (item: string) => item;

describe('ReorderableList', () => {
  describe('rendering', () => {
    it('renders all items', () => {
      render(<ReorderableList items={items} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} />);
      expect(screen.getByTestId('item-Apple')).toBeInTheDocument();
      expect(screen.getByTestId('item-Banana')).toBeInTheDocument();
      expect(screen.getByTestId('item-Cherry')).toBeInTheDocument();
    });

    it('renders helper text by default', () => {
      render(<ReorderableList items={items} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} />);
      expect(screen.getByText('Drag the handle to reorder')).toBeInTheDocument();
    });

    it('hides helper text when only one item', () => {
      render(<ReorderableList items={['Only']} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} />);
      expect(screen.queryByText('Drag the handle to reorder')).not.toBeInTheDocument();
    });

    it('renders custom helperText', () => {
      render(<ReorderableList items={items} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} helperText='Drag to sort' />);
      expect(screen.getByText('Drag to sort')).toBeInTheDocument();
    });

    it('suppresses helper text when helperText is empty string', () => {
      render(<ReorderableList items={items} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} helperText='' />);
      expect(screen.queryByText('Drag the handle to reorder')).not.toBeInTheDocument();
    });

    it('renders empty list without crashing', () => {
      const { container } = render(<ReorderableList items={[]} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} />);
      expect(container.firstChild).toBeInTheDocument();
    });
  });

  describe('drag handles', () => {
    it('renders drag handles for each item', () => {
      render(<ReorderableList items={items} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} />);
      expect(screen.getByTestId('handle-Apple')).toBeInTheDocument();
      expect(screen.getByTestId('handle-Banana')).toBeInTheDocument();
    });

    it('drag handles have draggable=true prop', () => {
      render(<ReorderableList items={items} onReorder={jest.fn()} getItemKey={getItemKey} renderItem={renderItem} />);
      expect(screen.getByTestId('handle-Apple')).toHaveAttribute('draggable', 'true');
    });
  });
});
