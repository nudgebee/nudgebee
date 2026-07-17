import { render, screen, fireEvent, act } from '@testing-library/react';
import CustomDrawer, { SecondaryDrawer } from '@shared/CustomDrawer';

jest.mock('@utils/colors');

describe('CustomDrawer', () => {
  it('renders title when open=true', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Drawer Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByText('Drawer Title')).toBeInTheDocument();
  });

  it('does not show content when open=false', () => {
    render(
      <CustomDrawer open={false} onClose={() => {}} title='Drawer Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.queryByText('Drawer Title')).not.toBeInTheDocument();
  });

  it('renders children inside drawer when open', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Drawer Title'>
        <div data-testid='drawer-child'>Child</div>
      </CustomDrawer>
    );
    expect(screen.getByTestId('drawer-child')).toBeInTheDocument();
  });

  it('calls onClose when close button clicked (data-testid="custom-drawer-close")', () => {
    const onClose = jest.fn();
    render(
      <CustomDrawer open={true} onClose={onClose} title='Drawer Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    const closeButton = screen.getByTestId('custom-drawer-close');
    fireEvent.click(closeButton);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders custom title', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title={<span data-testid='custom-title'>Custom Title Node</span>}>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByTestId('custom-title')).toBeInTheDocument();
  });

  it('renders without error with default width', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}}>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByTestId('custom-drawer-close')).toBeInTheDocument();
  });

  it('renders resize handle by default (resizable=true)', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Title'>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByLabelText('Resize drawer')).toBeInTheDocument();
  });

  it('does not render resize handle when resizable=false', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Title' resizable={false}>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.queryByLabelText('Resize drawer')).not.toBeInTheDocument();
  });

  it('calls onWidthChange on mount', () => {
    const onWidthChange = jest.fn();
    render(
      <CustomDrawer open={true} onClose={() => {}} onWidthChange={onWidthChange}>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(onWidthChange).toHaveBeenCalledTimes(1);
    expect(typeof onWidthChange.mock.calls[0][0]).toBe('number');
  });

  it('renders with variant=modern without error', () => {
    render(
      <CustomDrawer open={true} onClose={() => {}} title='Modern' variant='modern'>
        <div>Content</div>
      </CustomDrawer>
    );
    expect(screen.getByText('Modern')).toBeInTheDocument();
  });
});

describe('SecondaryDrawer', () => {
  it('renders title and children when open=true', async () => {
    await act(async () => {
      render(
        <SecondaryDrawer open={true} onClose={() => {}} title='Secondary Title'>
          <div data-testid='secondary-child'>Child</div>
        </SecondaryDrawer>
      );
    });
    expect(screen.getByText('Secondary Title')).toBeInTheDocument();
    expect(screen.getByTestId('secondary-child')).toBeInTheDocument();
  });

  it('does not render when open=false', () => {
    render(
      <SecondaryDrawer open={false} onClose={() => {}} title='Secondary Title'>
        <div>Child</div>
      </SecondaryDrawer>
    );
    expect(screen.queryByText('Secondary Title')).not.toBeInTheDocument();
  });

  it('calls onClose when close button clicked', async () => {
    const onClose = jest.fn();
    await act(async () => {
      render(
        <SecondaryDrawer open={true} onClose={onClose} title='Secondary Title'>
          <div>Child</div>
        </SecondaryDrawer>
      );
    });
    fireEvent.click(screen.getByTestId('custom-drawer-close'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('renders with variant=modern without error', async () => {
    await act(async () => {
      render(
        <SecondaryDrawer open={true} onClose={() => {}} title='Modern Secondary' variant='modern'>
          <div>Content</div>
        </SecondaryDrawer>
      );
    });
    expect(screen.getByText('Modern Secondary')).toBeInTheDocument();
  });
});
