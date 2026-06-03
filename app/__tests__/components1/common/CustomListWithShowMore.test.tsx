import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ShowMoreList from '@components1/common/CustomListWithShowMore';

jest.mock('@components1/common/CustomDivider', () => ({
  __esModule: true,
  default: () => <hr data-testid='divider' />,
}));

const sampleData = ['Item 1', 'Item 2', 'Item 3', 'Item 4', 'Item 5', 'Item 6', 'Item 7'];

describe('CustomListWithShowMore (ShowMoreList)', () => {
  test('renders initial items (up to initialCount)', () => {
    render(<ShowMoreList data={sampleData} initialCount={3} />);
    expect(screen.getByText('Item 1')).toBeInTheDocument();
    expect(screen.getByText('Item 2')).toBeInTheDocument();
    expect(screen.getByText('Item 3')).toBeInTheDocument();
  });

  test('does not show items beyond initialCount initially', () => {
    render(<ShowMoreList data={sampleData} initialCount={3} />);
    expect(screen.queryByText('Item 4')).not.toBeInTheDocument();
    expect(screen.queryByText('Item 5')).not.toBeInTheDocument();
  });

  test('shows "Show more (N)" when there are more items than initialCount', () => {
    render(<ShowMoreList data={sampleData} initialCount={3} />);
    // 7 items - 3 initialCount = 4 remaining
    expect(screen.getByText('Show more (4)')).toBeInTheDocument();
  });

  test('shows all items after clicking "Show more"', () => {
    render(<ShowMoreList data={sampleData} initialCount={3} />);
    fireEvent.click(screen.getByText('Show more (4)'));
    sampleData.forEach((item) => {
      expect(screen.getByText(item)).toBeInTheDocument();
    });
  });

  test('shows "Show less" after expanding', () => {
    render(<ShowMoreList data={sampleData} initialCount={3} />);
    fireEvent.click(screen.getByText('Show more (4)'));
    expect(screen.getByText('Show less')).toBeInTheDocument();
  });

  test('hides extra items after clicking "Show less"', () => {
    render(<ShowMoreList data={sampleData} initialCount={3} />);
    fireEvent.click(screen.getByText('Show more (4)'));
    fireEvent.click(screen.getByText('Show less'));
    expect(screen.queryByText('Item 4')).not.toBeInTheDocument();
    expect(screen.getByText('Item 1')).toBeInTheDocument();
  });

  test('calls onItemClick when item is clicked', () => {
    const onItemClick = jest.fn();
    render(<ShowMoreList data={sampleData} initialCount={5} onItemClick={onItemClick} />);
    fireEvent.click(screen.getByText('Item 1'));
    expect(onItemClick).toHaveBeenCalledWith('Item 1');
  });

  test('does not show "Show more" when items <= initialCount', () => {
    const smallData = ['Item 1', 'Item 2'];
    render(<ShowMoreList data={smallData} initialCount={5} />);
    expect(screen.queryByText(/Show more/)).not.toBeInTheDocument();
  });
});
