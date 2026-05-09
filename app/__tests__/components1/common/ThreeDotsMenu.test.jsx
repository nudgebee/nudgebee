import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280', secondaryDark: '#1F2937' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    primary: '#3B82F6',
  },
}));

jest.mock('next/router', () => ({ useRouter: jest.fn(() => ({ push: jest.fn(), pathname: '/', asPath: '/' })) }));

jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid='menu-icon'>{alt}</span>,
}));

describe('ThreeDotsMenu', () => {
  it('renders nothing (empty fragment) when menuItems is empty', () => {
    render(<ThreeDotsMenu menuItems={[]} />);
    // empty fragment renders no meaningful DOM element
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('renders three-dots button when menuItems is non-empty', () => {
    render(<ThreeDotsMenu menuItems={[{ label: 'Action 1' }]} />);
    expect(screen.getByRole('button', { name: 'more' })).toBeInTheDocument();
  });

  it('clicking three-dots opens menu showing item labels', () => {
    render(<ThreeDotsMenu menuItems={[{ label: 'Edit' }, { label: 'Delete' }]} />);
    const button = screen.getByRole('button', { name: 'more' });
    fireEvent.click(button);
    expect(screen.getByText('Edit')).toBeInTheDocument();
    expect(screen.getByText('Delete')).toBeInTheDocument();
  });

  it('clicking menu item calls onMenuClick with item and data', () => {
    const onMenuClick = jest.fn();
    const data = { id: 42 };
    const menuItems = [{ label: 'Edit' }];
    render(<ThreeDotsMenu menuItems={menuItems} onMenuClick={onMenuClick} data={data} />);
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    fireEvent.click(screen.getByText('Edit'));
    expect(onMenuClick).toHaveBeenCalledWith(menuItems[0], data);
  });

  it('clicking menu item closes menu', () => {
    const onMenuClick = jest.fn();
    const data = { id: 1 };
    const menuItems = [{ label: 'Remove' }];
    render(<ThreeDotsMenu menuItems={menuItems} onMenuClick={onMenuClick} data={data} />);
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    expect(screen.getByText('Remove')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Remove'));
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  });

  it('menu item is disabled when item.disabled=true', () => {
    render(<ThreeDotsMenu menuItems={[{ label: 'Disabled Action', disabled: true }]} />);
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    const menuItem = screen.getByRole('menuitem', { name: /Disabled Action/i });
    expect(menuItem).toHaveAttribute('aria-disabled', 'true');
  });

  it('renders item with icon', () => {
    const menuItems = [{ label: 'With Icon', icon: 'some-icon-src' }];
    render(<ThreeDotsMenu menuItems={menuItems} />);
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    expect(screen.getByTestId('menu-icon')).toBeInTheDocument();
  });

  it('renders submenu items when item has subMenu', () => {
    const menuItems = [
      {
        label: 'Parent',
        subMenu: [{ label: 'Child 1' }, { label: 'Child 2' }],
      },
    ];
    render(<ThreeDotsMenu menuItems={menuItems} />);
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    expect(screen.getByText('Parent')).toBeInTheDocument();
  });

  it('clicking submenu parent toggles submenu collapse', () => {
    const menuItems = [
      {
        label: 'Parent',
        subMenu: [{ label: 'Child 1' }],
      },
    ];
    render(<ThreeDotsMenu menuItems={menuItems} />);
    fireEvent.click(screen.getByRole('button', { name: 'more' }));
    const parentItem = screen.getByText('Parent');
    expect(parentItem).toBeInTheDocument();
    // Child not visible before expand
    expect(screen.queryByText('Child 1')).not.toBeInTheDocument();
    // Click parent to expand submenu
    fireEvent.click(parentItem.closest('[role="menuitem"]'));
    expect(screen.getByText('Child 1')).toBeInTheDocument();
  });
});
