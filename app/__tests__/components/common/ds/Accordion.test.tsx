import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { Accordion } from '@ui/Accordion';

const items = [
  { id: 'item-1', label: 'Section 1', body: <div>Body 1</div> },
  { id: 'item-2', label: 'Section 2', body: <div>Body 2</div> },
  { id: 'item-3', label: 'Section 3', body: <div>Body 3</div>, disabled: true },
];

describe('Accordion', () => {
  it('renders all item labels', () => {
    render(<Accordion items={items} />);
    expect(screen.getByText('Section 1')).toBeInTheDocument();
    expect(screen.getByText('Section 2')).toBeInTheDocument();
    expect(screen.getByText('Section 3')).toBeInTheDocument();
  });

  it('shows body when defaultExpandedIds includes item id', () => {
    render(<Accordion items={items} defaultExpandedIds={['item-1']} />);
    expect(screen.getByText('Body 1')).toBeInTheDocument();
  });

  it('does not show body of collapsed item', () => {
    render(<Accordion items={items} />);
    expect(screen.queryByText('Body 1')).not.toBeInTheDocument();
  });

  it('expands item on click', () => {
    render(<Accordion items={items} />);
    fireEvent.click(screen.getByText('Section 1'));
    expect(screen.getByText('Body 1')).toBeInTheDocument();
  });

  it('collapses item on second click', () => {
    render(<Accordion items={items} defaultExpandedIds={['item-1']} />);
    fireEvent.click(screen.getByText('Section 1'));
    expect(screen.queryByText('Body 1')).not.toBeInTheDocument();
  });

  it('single selection mode: opening one closes another', () => {
    render(<Accordion items={items} selection='single' defaultExpandedIds={['item-1']} />);
    fireEvent.click(screen.getByText('Section 2'));
    expect(screen.queryByText('Body 1')).not.toBeInTheDocument();
    expect(screen.getByText('Body 2')).toBeInTheDocument();
  });

  it('multi selection mode: both items can be open simultaneously', () => {
    render(<Accordion items={items} selection='multi' />);
    fireEvent.click(screen.getByText('Section 1'));
    fireEvent.click(screen.getByText('Section 2'));
    expect(screen.getByText('Body 1')).toBeInTheDocument();
    expect(screen.getByText('Body 2')).toBeInTheDocument();
  });

  it('calls onExpandedChange when toggled', () => {
    const onChange = jest.fn();
    render(<Accordion items={items} onExpandedChange={onChange} />);
    fireEvent.click(screen.getByText('Section 1'));
    expect(onChange).toHaveBeenCalledWith(['item-1']);
  });

  it('controlled mode: responds to expandedIds prop', () => {
    const { rerender } = render(<Accordion items={items} expandedIds={['item-1']} />);
    expect(screen.getByText('Body 1')).toBeInTheDocument();
    rerender(<Accordion items={items} expandedIds={[]} />);
    expect(screen.queryByText('Body 1')).not.toBeInTheDocument();
  });

  it('renders sm density', () => {
    render(<Accordion items={items} density='sm' />);
    expect(screen.getByText('Section 1')).toBeInTheDocument();
  });

  it('renders item with icon and meta slots', () => {
    const richItems = [{ id: 'a', label: 'Label A', body: 'Body A', icon: <span>icon</span>, meta: <span>meta</span> }];
    render(<Accordion items={richItems} defaultExpandedIds={['a']} />);
    expect(screen.getByText('Label A')).toBeInTheDocument();
    expect(screen.getByText('meta')).toBeInTheDocument();
  });
});
