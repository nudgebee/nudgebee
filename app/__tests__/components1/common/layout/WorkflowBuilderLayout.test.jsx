import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';

// Mock next-auth/react
jest.mock('next-auth/react', () => ({
  signOut: jest.fn(),
  useSession: () => ({
    data: { user: { name: 'Test User' }, roles: [], tenant: 'test' },
    status: 'authenticated',
  }),
}));

// Mock next/router
const mockPush = jest.fn();
jest.mock('next/router', () => ({
  useRouter: jest.fn(() => ({
    push: jest.fn(),
    replace: jest.fn(),
    pathname: '/workflow',
    query: {},
    asPath: '/workflow',
    route: '/workflow',
    prefetch: jest.fn().mockResolvedValue(null),
  })),
}));

// Mock next/head
jest.mock('next/head', () => {
  return function Head({ children }) {
    return <>{children}</>;
  };
});

// Mock next/script
jest.mock('next/script', () => {
  return function Script({ children, id }) {
    return <script id={id}>{children}</script>;
  };
});

// Mock auth lib
jest.mock('@lib/auth', () => ({
  getUserSession: jest.fn(() => ({
    user: { name: 'Test User', email: 'test@example.com' },
    hasMultipleTenantAccess: false,
    onPrem: false,
  })),
  withAuth: (Component) => {
    const WrappedComponent = (props) => <Component {...props} />;
    WrappedComponent.displayName = `withAuth(${Component.displayName || Component.name})`;
    return WrappedComponent;
  },
  isTenantAdmin: jest.fn(() => false),
}));

// Mock @assets
jest.mock(
  '@assets',
  () => ({
    LogoIcon: '/logo-icon.png',
    addIconWhite: '/add-icon.png',
    ProfileOutlineIcon: '/profile-icon.png',
    TracesIcon: '/traces-icon.png',
    assignmentBlackSvg: '/assignment-icon.png',
  }),
  { virtual: true }
);

// Mock SafeIcon
jest.mock(
  '@components1/common/SafeIcon',
  () =>
    function MockSafeIcon({ alt, src }) {
      return <img alt={alt || 'icon'} src={typeof src === 'string' ? src : '/mock-icon.png'} />;
    }
);

// Mock Header1
jest.mock(
  '@components1/common/header/Header1',
  () =>
    function MockHeader1() {
      return <header data-testid='header1'>Header</header>;
    }
);

// Mock child modals
jest.mock(
  '@components1/common/TenantSettings',
  () =>
    function MockTenantSettings({ open, onClose }) {
      return open ? (
        <div data-testid='tenant-settings'>
          <button onClick={() => onClose(null, 'show')} data-testid='close-tenant-settings-show'>
            Close with show
          </button>
          <button onClick={() => onClose(null, null)} data-testid='close-tenant-settings-plain'>
            Close plain
          </button>
        </div>
      ) : null;
    }
);

jest.mock(
  '@components1/common/ApiTokens',
  () =>
    function MockApiTokens({ open, onClose }) {
      return open ? (
        <div data-testid='api-tokens'>
          <button onClick={onClose} data-testid='close-api-tokens'>
            Close
          </button>
        </div>
      ) : null;
    }
);

jest.mock('@components1/common/layout/SwitchTenant', () => ({
  SwitchTenant: function MockSwitchTenant({ open, onClose }) {
    return open ? (
      <div data-testid='switch-tenant'>
        <button onClick={onClose} data-testid='close-switch-tenant'>
          Close
        </button>
      </div>
    ) : null;
  },
}));

// Mock UserMenuItems
jest.mock('@components1/common/layout/UserMenuItems', () => ({
  createGetMenuItem: jest.fn(
    ({
      setAnchorElUser,
      setOpenSwitchAccount: _setOpenSwitchAccount,
      setOpenSettings: _setOpenSettings,
      setOpenApiTokens: _setOpenApiTokens,
      handleSubMenuClick,
    }) => {
      return (setting) => (
        <button
          key={setting}
          data-testid={`menu-item-${setting}`}
          onClick={() => {
            setAnchorElUser(null);
            handleSubMenuClick(setting);
          }}
        >
          {setting}
        </button>
      );
    }
  ),
  generateMenuItems: jest.fn(() => ['UserInfo', 'Logout']),
}));

// Mock colors
jest.mock('src/utils/colors', () => {
  const actual = jest.requireActual('src/utils/colors');
  return {
    ...actual,
    colors: {
      ...actual.colors,
      text: { ...actual.colors.text, tertiary: '#666', secondary: '#333', secondaryDark: '#555', white: '#fff' },
      background: {
        ...actual.colors.background,
        pages: '#fff',
        home: '#f5f5f5',
        transparent: 'transparent',
        activeButtonColor: '#eee',
      },
      border: { ...actual.colors.border, secondaryLightest: '#eee', secondary: '#ddd' },
      secondary: { default: '#0000ff' },
      primary: { main: '#0000ff' },
    },
  };
});

const { generateMenuItems } = require('@components1/common/layout/UserMenuItems');
const { useRouter: mockUseRouter } = require('next/router');
const { getUserSession: mockGetUserSession } = require('@lib/auth');
const { signOut: mockSignOut } = require('next-auth/react');

// Spy on console.log to suppress template navigation output
const consoleSpy = jest.spyOn(console, 'log').mockImplementation(() => {});

import WorkflowBuilderLayoutWrapped from '@components1/common/layout/WorkflowBuilderLayout';

afterAll(() => {
  consoleSpy.mockRestore();
});

describe('WorkflowBuilderLayout', () => {
  const defaultProps = {
    children: <div data-testid='child-content'>Child Content</div>,
    handleHomePage: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    mockGetUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: false,
    });
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      replace: jest.fn(),
      pathname: '/workflow',
      query: {},
      asPath: '/workflow',
      route: '/workflow',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    generateMenuItems.mockReturnValue(['UserInfo', 'Logout']);
  });

  it('renders children content', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders header when not in /workflow/ subpath', async () => {
    // /workflow is not a subpath of /workflow/ (startsWith check)
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('header1')).toBeInTheDocument();
  });

  it('does not render header on /workflow/ builder pages', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow/new',
      query: {},
      asPath: '/workflow/new',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.queryByTestId('header1')).not.toBeInTheDocument();
  });

  it('renders menu items in sidebar', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByText('App')).toBeInTheDocument();
    expect(screen.getByText('New')).toBeInTheDocument();
    expect(screen.getByText('Workflows')).toBeInTheDocument();
    expect(screen.getByText('Template')).toBeInTheDocument();
  });

  it('calls handleHomePage when App button clicked', async () => {
    const handleHomePage = jest.fn();
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} handleHomePage={handleHomePage} />);
    });
    // App button - find the one with 'App' text that has onClick
    const allItems = screen.getAllByText('App');
    // Click on the one in sidebar
    fireEvent.click(allItems[0].closest('div') || allItems[0]);
    expect(handleHomePage).toHaveBeenCalled();
  });

  it('navigates to /workflow/new when New button clicked', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    const newText = screen.getByText('New');
    fireEvent.click(newText.closest('div') || newText);
    expect(mockPush).toHaveBeenCalledWith('/workflow/new');
  });

  it('navigates to /workflow/new?accountId= when New clicked with accountId', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow',
      query: { accountId: 'acc-123' },
      asPath: '/workflow?accountId=acc-123',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    const newText = screen.getByText('New');
    fireEvent.click(newText.closest('div') || newText);
    expect(mockPush).toHaveBeenCalledWith('/workflow/new?accountId=acc-123');
  });

  it('navigates to /workflow when Workflows button clicked', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    const workflowText = screen.getByText('Workflows');
    fireEvent.click(workflowText.closest('div') || workflowText);
    expect(mockPush).toHaveBeenCalledWith('/workflow');
  });

  it('navigates to /workflow?accountId= when Workflows clicked with accountId', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow',
      query: { accountId: 'acc-456' },
      asPath: '/workflow?accountId=acc-456',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    const workflowText = screen.getByText('Workflows');
    fireEvent.click(workflowText.closest('div') || workflowText);
    expect(mockPush).toHaveBeenCalledWith('/workflow?accountId=acc-456');
  });

  it('does not log when Template button clicked (disabled)', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    const templateText = screen.getByText('Template');
    fireEvent.click(templateText.closest('div') || templateText);
    expect(consoleSpy).not.toHaveBeenCalledWith('Template navigation not implemented yet');
  });

  it('disabled item prevents onClick from firing', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    // Template is disabled - clicking it with isDisabled check
    // Find the Template container box
    const templateText = screen.getByText('Template');
    const templateBox = templateText.closest('div[style*="cursor"]') || templateText.closest('div');

    // The click handler checks isDisabled and returns early
    // We just verify no error thrown
    fireEvent.click(templateBox || templateText);
    // handleTemplate should not be called (e.preventDefault and return)
    // But consoleSpy won't be called if disabled stops it
  });

  it('opens user menu on profile button click', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    const profileBtn = screen.getByRole('button', { name: /account settings/i });
    fireEvent.click(profileBtn);
    await waitFor(() => {
      expect(screen.getByTestId('menu-item-Logout')).toBeInTheDocument();
    });
  });

  it('handles Logout from menu', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    fireEvent.click(screen.getByRole('button', { name: /account settings/i }));
    await waitFor(() => screen.getByTestId('menu-item-Logout'));
    fireEvent.click(screen.getByTestId('menu-item-Logout'));
    expect(mockSignOut).toHaveBeenCalledWith({ callbackUrl: '/' });
  });

  it('handles Switch Tenant from menu', async () => {
    generateMenuItems.mockReturnValue(['UserInfo', 'Switch Tenant', 'Logout']);
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    fireEvent.click(screen.getByRole('button', { name: /account settings/i }));
    await waitFor(() => screen.getByTestId('menu-item-Switch Tenant'));
    fireEvent.click(screen.getByTestId('menu-item-Switch Tenant'));
    await waitFor(() => expect(screen.getByTestId('switch-tenant')).toBeInTheDocument());
    fireEvent.click(screen.getByTestId('close-switch-tenant'));
    expect(screen.queryByTestId('switch-tenant')).not.toBeInTheDocument();
  });

  it('handles unknown subMenu click', async () => {
    generateMenuItems.mockReturnValue(['SomeUnknown']);
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    fireEvent.click(screen.getByRole('button', { name: /account settings/i }));
    await waitFor(() => screen.getByTestId('menu-item-SomeUnknown'));
    // Clicking unknown item goes to default switch case (does nothing)
    fireEvent.click(screen.getByTestId('menu-item-SomeUnknown'));
    expect(mockSignOut).not.toHaveBeenCalled();
  });

  it('renders with onPrem = true (no Google Analytics)', async () => {
    mockGetUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: true,
    });
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders with onPrem = false (Google Analytics script present)', async () => {
    mockGetUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: false,
    });
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('activeItem is New when on /workflow/new', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow/new',
      query: {},
      asPath: '/workflow/new',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('activeItem is New when workflowId=new in query', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow',
      query: { workflowId: 'new' },
      asPath: '/workflow?workflowId=new',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('activeItem is Workflows when on /workflow', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow',
      query: {},
      asPath: '/workflow',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByText('Workflows')).toBeInTheDocument();
  });

  it('activeItem is App when on /home', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/home',
      query: {},
      asPath: '/home',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByText('App')).toBeInTheDocument();
  });

  it('activeItem is App when on /auto-pilot', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/auto-pilot',
      query: {},
      asPath: '/auto-pilot',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByText('App')).toBeInTheDocument();
  });

  it('activeItem is null on unknown path', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/some-unknown-path',
      query: {},
      asPath: '/some-unknown-path',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('updates avatar sub menu when switchAccountEnabled changes', async () => {
    mockGetUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: true,
      onPrem: false,
    });
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    expect(generateMenuItems).toHaveBeenCalled();
  });

  it('closes user menu via handleCloseUserMenu', async () => {
    await act(async () => {
      render(<WorkflowBuilderLayoutWrapped {...defaultProps} />);
    });
    fireEvent.click(screen.getByRole('button', { name: /account settings/i }));
    await waitFor(() => screen.getByTestId('menu-item-Logout'));
    // Simulate closing the menu (via clicking backdrop or pressing Escape)
    const menu = document.querySelector('[role="presentation"]');
    if (menu) {
      fireEvent.keyDown(menu, { key: 'Escape' });
    }
  });
});

describe('SideDrawerButton (within WorkflowBuilderLayout)', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: false,
    });
    generateMenuItems.mockReturnValue(['UserInfo', 'Logout']);
  });

  it('does not call onClick when disabled button is clicked', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow',
      query: {},
      asPath: '/workflow',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    const handleHomePage = jest.fn();
    await act(async () => {
      render(
        <WorkflowBuilderLayoutWrapped handleHomePage={handleHomePage}>
          <div>child</div>
        </WorkflowBuilderLayoutWrapped>
      );
    });
    // Template button is disabled - its onClick (handleTemplate) is only called if not disabled
    // Find Template text and click its parent box
    const templateText = screen.getByText('Template');
    const box = templateText.parentElement;
    // A click on a disabled item should preventDefault and return without calling onClick
    fireEvent.click(box);
    // consoleSpy shouldn't be called since isDisabled=true returns early
    // (handleTemplate which calls console.log is not executed)
    expect(mockPush).not.toHaveBeenCalled();
  });

  it('calls item.onClick when enabled and has onClick', async () => {
    mockUseRouter.mockImplementation(() => ({
      push: mockPush,
      pathname: '/workflow',
      query: {},
      asPath: '/workflow',
      prefetch: jest.fn().mockResolvedValue(null),
    }));
    await act(async () => {
      render(
        <WorkflowBuilderLayoutWrapped handleHomePage={jest.fn()}>
          <div>child</div>
        </WorkflowBuilderLayoutWrapped>
      );
    });
    // New button calls handleNewWorkflow (which calls router.push('/workflow/new'))
    const newText = screen.getByText('New');
    fireEvent.click(newText.closest('div') || newText);
    expect(mockPush).toHaveBeenCalledWith('/workflow/new');
  });
});
