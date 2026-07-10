import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import { DropdownMenu } from '@ui/DropdownMenu';

const items = [
  { label: 'Edit', onSelect: jest.fn() },
  { label: 'Duplicate', onSelect: jest.fn() },
  { label: 'Delete', tone: 'danger' as const, onSelect: jest.fn() },
];

describe('DropdownMenu', () => {
  it('renders the trigger element', () => {
    render(<DropdownMenu trigger={<button>Open Menu</button>} items={items} />);
    expect(screen.getByText('Open Menu')).toBeInTheDocument();
  });

  it('opens menu on trigger click', () => {
    render(<DropdownMenu trigger={<button>Open</button>} items={items} />);
    fireEvent.click(screen.getByText('Open'));
    expect(screen.getByRole('menu', { hidden: true })).toBeInTheDocument();
  });

  it('renders all item labels when open', () => {
    render(<DropdownMenu trigger={<button>Open</button>} items={items} />);
    fireEvent.click(screen.getByText('Open'));
    expect(screen.getByText('Edit')).toBeInTheDocument();
    expect(screen.getByText('Duplicate')).toBeInTheDocument();
    expect(screen.getByText('Delete')).toBeInTheDocument();
  });

  it('calls onSelect and closes menu when item clicked', () => {
    const onSelect = jest.fn();
    render(<DropdownMenu trigger={<button>Open</button>} items={[{ label: 'Action', onSelect }]} />);
    fireEvent.click(screen.getByText('Open'));
    fireEvent.click(screen.getByText('Action'));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it('renders section headers', () => {
    const withSection = [
      { type: 'section' as const, label: 'Group 1' },
      { label: 'Item 1', onSelect: jest.fn() },
    ];
    render(<DropdownMenu trigger={<button>Open</button>} items={withSection} />);
    fireEvent.click(screen.getByText('Open'));
    expect(screen.getByText('Group 1')).toBeInTheDocument();
  });

  it('renders loading state without crashing', () => {
    render(<DropdownMenu trigger={<button>Open</button>} items={[]} loading />);
    fireEvent.click(screen.getByText('Open'));
    expect(document.body).toBeInTheDocument();
  });

  it('renders searchable header', () => {
    render(<DropdownMenu trigger={<button>Open</button>} items={items} searchable />);
    fireEvent.click(screen.getByText('Open'));
    expect(screen.getByPlaceholderText('Search…')).toBeInTheDocument();
  });

  it('filters items by search input', () => {
    const searchItems = [
      { label: 'Apple', onSelect: jest.fn(), searchText: 'Apple' },
      { label: 'Banana', onSelect: jest.fn(), searchText: 'Banana' },
    ];
    render(<DropdownMenu trigger={<button>Open</button>} items={searchItems} searchable />);
    fireEvent.click(screen.getByText('Open'));
    fireEvent.change(screen.getByPlaceholderText('Search…'), { target: { value: 'ban' } });
    expect(screen.queryByText('Apple')).not.toBeInTheDocument();
    expect(screen.getByText('Banana')).toBeInTheDocument();
  });

  describe('keepMounted', () => {
    it('does not render item content before open by default', () => {
      render(<DropdownMenu trigger={<button>Open</button>} items={items} />);
      expect(screen.queryByText('Edit')).not.toBeInTheDocument();
      expect(screen.queryByRole('menu', { hidden: true })).not.toBeInTheDocument();
    });

    it('mounts item content hidden before open when keepMounted is set', () => {
      render(<DropdownMenu trigger={<button>Open</button>} items={items} keepMounted />);
      expect(screen.getByText('Edit')).toBeInTheDocument();
      expect(screen.getByRole('menu', { hidden: true })).toBeInTheDocument();
    });

    it('still opens and remains interactive when keepMounted is set', () => {
      const onSelect = jest.fn();
      render(<DropdownMenu trigger={<button>Open</button>} items={[{ label: 'Action', onSelect }]} keepMounted />);
      fireEvent.click(screen.getByText('Open'));
      fireEvent.click(screen.getByText('Action'));
      expect(onSelect).toHaveBeenCalledTimes(1);
    });
  });
});
