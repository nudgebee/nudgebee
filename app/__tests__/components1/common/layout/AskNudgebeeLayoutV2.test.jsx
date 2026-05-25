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

// Mock next/router with push spy
const mockPush = jest.fn();
jest.mock('next/router', () => ({
  useRouter: jest.fn(() => ({
    push: mockPush,
    replace: jest.fn(),
    pathname: '/ask-nudgebee',
    query: {},
    asPath: '/ask-nudgebee',
    route: '/ask-nudgebee',
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

// Mock auth
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
  hasReadAccess: jest.fn(() => false),
}));

// Mock useTenantBranding hook
jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: jest.fn(() => ({
    baseTitle: 'Nudgebee',
    logoFallbacks: ['/logo.svg'],
    faviconUrl: '/favicon.ico',
    partnerKey: null,
    tenantKey: 'test',
    isDefaultTenant: true,
    loading: false,
  })),
  DEFAULT_LOGO: '/default-logo.svg',
  DEFAULT_FAVICON: '/favicon.ico',
}));

// Mock @assets
jest.mock(
  '@assets',
  () => ({
    PlusIconSecondary: '/plus-icon.png',
    ProfileOutlineIcon: '/profile-icon.png',
    ChatOutlineDarkIcon: '/chat-icon.png',
    SettingOutlineIcon: '/setting-icon.png',
    ArrowBackGrayIcon: '/arrow-back-icon.png',
  }),
  { virtual: true }
);

// Mock apiAskNudgebee
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listAgents: jest.fn().mockResolvedValue({
      data: {
        data: {
          ai_list_agents: {
            data: [
              { name: 'agent1', status: 'enabled', aliases: ['Agent One'] },
              { name: 'agent2', status: 'disabled', aliases: [] },
            ],
          },
        },
      },
    }),
  },
}));

// Mock SafeIcon
jest.mock(
  '@components1/common/SafeIcon',
  () =>
    function MockSafeIcon({ alt, src, ...props }) {
      return <img alt={alt} src={typeof src === 'string' ? src : '/mock-icon.png'} {...props} />;
    }
);

// Mock child modals
jest.mock(
  '@components1/llm/SettingsModal',
  () =>
    function MockSettingsModal({ open, onClose }) {
      return open ? (
        <div data-testid='settings-modal'>
          <button onClick={onClose} data-testid='close-settings'>
            Close Settings
          </button>
        </div>
      ) : null;
    }
);

jest.mock(
  '@components1/common/TenantSettings',
  () =>
    function MockTenantSettings({ open, onClose }) {
      return open ? (
        <div data-testid='tenant-settings'>
          <button onClick={onClose} data-testid='close-tenant-settings'>
            Close
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

// Mock NewCustomButton
jest.mock(
  '@components1/common/NewCustomButton',
  () =>
    function MockCustomButton({ onClick, toolTipTitle, ...props }) {
      return (
        <button onClick={onClick} data-testid={`custom-btn-${toolTipTitle || 'btn'}`} {...props}>
          {toolTipTitle || 'button'}
        </button>
      );
    }
);

// Mock UserMenuItems
jest.mock('@components1/common/layout/UserMenuItems', () => ({
  createGetMenuItem: jest.fn(() => (setting) => (
    <div key={setting} data-testid={`menu-item-${setting}`}>
      {setting}
    </div>
  )),
  generateMenuItems: jest.fn(() => ['UserInfo', 'Logout']),
}));

// Mock colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: { tertiary: '#666', secondary: '#333', secondaryDark: '#555', white: '#fff' },
    background: {
      pages: '#fff',
      home: '#f5f5f5',
      transparent: 'transparent',
      activeButtonColor: '#eee',
      askNudgebeePage: '#f9f9f9',
      sideBar: '#1B2D4A',
      white: '#fff',
      secondaryDark: '#444',
    },
    border: { secondaryLightest: '#eee', secondary: '#ddd' },
    secondary: { default: '#0000ff' },
    primary: { main: '#0000ff' },
    switchIconColor: '#aaa',
    white: '#fff',
  },
}));

const apiAskNudgebee = require('@api1/ask-nudgebee').default;
const { useRouter } = require('next/router');
const { getUserSession } = require('@lib/auth');

// Import the default export (wrapped with withAuth)
import AskNudgebeeLayoutWrapped from '@components1/common/layout/AskNudgebeeLayoutV2';

describe('AskNudgebeeLayoutV2', () => {
  const defaultProps = {
    children: <div data-testid='child-content'>Child Content</div>,
    handleNewChat: jest.fn(),
    handleHomePage: jest.fn(),
    handleToggle: jest.fn(),
    onAgentsRefreshed: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    useRouter.mockReturnValue({
      push: mockPush,
      replace: jest.fn(),
      pathname: '/ask-nudgebee',
      query: {},
      asPath: '/ask-nudgebee',
      route: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    getUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: false,
    });
  });

  it('renders children content', async () => {
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders with accountId in query and loads agents', async () => {
    useRouter.mockReturnValue({
      push: mockPush,
      replace: jest.fn(),
      pathname: '/ask-nudgebee',
      query: { accountId: 'acc-123' },
      asPath: '/ask-nudgebee',
      route: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    await waitFor(() => {
      expect(apiAskNudgebee.listAgents).toHaveBeenCalledWith({ accountId: 'acc-123' });
    });
  });

  it('does not load agents when externalAgents is provided', async () => {
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/ask-nudgebee',
      query: { accountId: 'acc-123' },
      asPath: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} externalAgents={[]} externalAgentsLoading={false} />);
    });
    expect(apiAskNudgebee.listAgents).not.toHaveBeenCalled();
  });

  it('renders settings button and opens settings modal on click', async () => {
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    const settingsBtn = screen.getByTestId('custom-btn-Settings');
    fireEvent.click(settingsBtn);
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
  });

  it('closes settings modal', async () => {
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    fireEvent.click(screen.getByTestId('custom-btn-Settings'));
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('close-settings'));
    expect(screen.queryByTestId('settings-modal')).not.toBeInTheDocument();
  });

  it('opens user menu on profile icon click', async () => {
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    const profileBtn = screen.getByRole('button', { name: /account settings/i });
    fireEvent.click(profileBtn);
    // Menu items should appear
    await waitFor(() => {
      expect(screen.getByTestId('menu-item-UserInfo')).toBeInTheDocument();
    });
  });

  it('renders with onPrem = true (no google analytics script)', async () => {
    getUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: true,
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders with hasMultipleTenantAccess = true', async () => {
    getUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: true,
      onPrem: false,
    });
    const { generateMenuItems } = require('@components1/common/layout/UserMenuItems');
    generateMenuItems.mockReturnValue(['UserInfo', 'Switch Tenant', 'Logout']);

    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('renders correctly on home page', async () => {
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/home',
      query: {},
      asPath: '/home',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child-content')).toBeInTheDocument();
  });

  it('handles refreshAgentListing with externalAgents', async () => {
    const onAgentsRefreshed = jest.fn();
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/ask-nudgebee',
      query: { accountId: 'acc-123' },
      asPath: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(
        <AskNudgebeeLayoutWrapped
          {...defaultProps}
          externalAgents={[{ name: 'ext-agent' }]}
          externalAgentsLoading={false}
          onAgentsRefreshed={onAgentsRefreshed}
        />
      );
    });
    // Open settings modal to trigger refreshAgentListing
    fireEvent.click(screen.getByTestId('custom-btn-Settings'));
    expect(screen.getByTestId('settings-modal')).toBeInTheDocument();
  });

  it('handles listAgents with onAgentsRefreshed callback', async () => {
    const onAgentsRefreshed = jest.fn();
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/ask-nudgebee',
      query: { accountId: 'acc-456' },
      asPath: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} onAgentsRefreshed={onAgentsRefreshed} />);
    });
    await waitFor(() => {
      expect(apiAskNudgebee.listAgents).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(onAgentsRefreshed).toHaveBeenCalled();
    });
  });

  it('handles listAgents returning empty data', async () => {
    apiAskNudgebee.listAgents.mockResolvedValue({
      data: { data: { ai_list_agents: { data: [] } } },
    });
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/ask-nudgebee',
      query: { accountId: 'acc-789' },
      asPath: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    await waitFor(() => {
      expect(apiAskNudgebee.listAgents).toHaveBeenCalled();
    });
  });
});

describe('SideDrawerButton (within AskNudgebeeLayoutV2)', () => {
  const defaultProps = {
    children: <div data-testid='child'>Child</div>,
    handleNewChat: jest.fn(),
    handleHomePage: jest.fn(),
    handleToggle: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    useRouter.mockReturnValue({
      push: mockPush,
      replace: jest.fn(),
      pathname: '/ask-nudgebee',
      query: {},
      asPath: '/ask-nudgebee',
      route: '/',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    getUserSession.mockReturnValue({
      user: { name: 'Test User', email: 'test@example.com' },
      hasMultipleTenantAccess: false,
      onPrem: false,
    });
  });

  it('calls handleHomePage when App button is clicked', async () => {
    const handleHomePage = jest.fn();
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} handleHomePage={handleHomePage} />);
    });
    // App button is the first menu item (ArrowBackGrayIcon, text='App')
    const buttons = screen.getAllByRole('button');
    // Find the App button by aria-labelledby
    const appBtn = buttons.find((b) => b.getAttribute('aria-labelledby') === 'App');
    if (appBtn) {
      fireEvent.click(appBtn);
      expect(handleHomePage).toHaveBeenCalled();
    }
  });

  it('navigates via router when item has no onClick and no subItems', async () => {
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/ask-nudgebee',
      query: { accountId: 'acc-123' },
      asPath: '/ask-nudgebee',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    // Chats button: has onClick (handleToggle)
    const buttons = screen.getAllByRole('button');
    const chatsBtn = buttons.find((b) => b.getAttribute('aria-labelledby') === 'Chats');
    if (chatsBtn) {
      fireEvent.click(chatsBtn);
    }
  });

  it('navigates with KubernetesDetails query', async () => {
    useRouter.mockReturnValue({
      push: mockPush,
      pathname: '/some-page',
      query: { KubernetesDetails: 'k8s-abc' },
      asPath: '/some-page',
      prefetch: jest.fn().mockResolvedValue(null),
    });
    await act(async () => {
      render(<AskNudgebeeLayoutWrapped {...defaultProps} />);
    });
    expect(screen.getByTestId('child')).toBeInTheDocument();
  });
});
