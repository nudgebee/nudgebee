import { render, screen, fireEvent } from '@testing-library/react';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';

jest.mock('@ui/DropdownMenu', () => ({
  DropdownMenu: ({ trigger, items, keepMounted }) => (
    <div data-testid='dropdown-menu' data-keep-mounted={String(!!keepMounted)}>
      {trigger}
      <ul data-testid='menu-items'>
        {items.map((item) => (
          <li key={item.id}>
            <button data-testid={`menu-item-${item.id}`} disabled={item.disabled} onClick={item.onSelect}>
              {item.label}
            </button>
          </li>
        ))}
      </ul>
    </div>
  ),
}));

jest.mock('@ui/Button', () => ({
  Button: ({ 'aria-label': ariaLabel, id, onClick }) => <button aria-label={ariaLabel} id={id} onClick={onClick} data-testid='trigger-btn' />,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

const baseItems = [
  { id: 'edit', label: 'Edit' },
  { id: 'delete', label: 'Delete' },
];

describe('ThreeDotsMenu', () => {
  describe('rendering', () => {
    it('renders nothing when menuItems is empty', () => {
      const { container } = render(<ThreeDotsMenu menuItems={[]} />);
      expect(container.firstChild).toBeNull();
    });

    it('renders nothing when menuItems is not provided', () => {
      const { container } = render(<ThreeDotsMenu />);
      expect(container.firstChild).toBeNull();
    });

    it('renders DropdownMenu when menuItems are provided', () => {
      render(<ThreeDotsMenu menuItems={baseItems} />);
      expect(screen.getByTestId('dropdown-menu')).toBeInTheDocument();
    });

    it('renders trigger button with aria-label=More actions', () => {
      render(<ThreeDotsMenu menuItems={baseItems} />);
      expect(screen.getByLabelText('More actions')).toBeInTheDocument();
    });

    it('uses "three-dot-menu" as default trigger button id', () => {
      render(<ThreeDotsMenu menuItems={baseItems} />);
      expect(screen.getByTestId('trigger-btn')).toHaveAttribute('id', 'three-dot-menu');
    });

    it('uses provided id for trigger button', () => {
      render(<ThreeDotsMenu id='my-menu' menuItems={baseItems} />);
      expect(screen.getByTestId('trigger-btn')).toHaveAttribute('id', 'my-menu');
    });

    it('renders all menu item labels', () => {
      render(<ThreeDotsMenu menuItems={baseItems} />);
      expect(screen.getByText('Edit')).toBeInTheDocument();
      expect(screen.getByText('Delete')).toBeInTheDocument();
    });

    it('renders disabled menu item as disabled', () => {
      render(<ThreeDotsMenu menuItems={[{ id: 'del', label: 'Delete', disabled: true }]} />);
      expect(screen.getByTestId('menu-item-del')).toBeDisabled();
    });

    it('passes keepMounted to DropdownMenu so icons preload before first click', () => {
      render(<ThreeDotsMenu menuItems={baseItems} />);
      expect(screen.getByTestId('dropdown-menu')).toHaveAttribute('data-keep-mounted', 'true');
    });
  });

  describe('click behavior', () => {
    it('calls onMenuClick with item and data when item is clicked', () => {
      const onMenuClick = jest.fn();
      const data = { id: 42 };
      render(<ThreeDotsMenu menuItems={baseItems} onMenuClick={onMenuClick} data={data} />);
      fireEvent.click(screen.getByTestId('menu-item-edit'));
      expect(onMenuClick).toHaveBeenCalledWith(baseItems[0], data);
    });

    it('does not call onMenuClick when data prop is not provided', () => {
      const onMenuClick = jest.fn();
      render(<ThreeDotsMenu menuItems={baseItems} onMenuClick={onMenuClick} />);
      fireEvent.click(screen.getByTestId('menu-item-edit'));
      expect(onMenuClick).not.toHaveBeenCalled();
    });
  });
});
