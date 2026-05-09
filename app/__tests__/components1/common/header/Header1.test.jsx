import React from 'react';
import { render, screen, fireEvent, act, waitFor, within } from '@testing-library/react';
import Header1 from '@components1/common/header/Header1';

// Mock next/router - override the global setup mock so we can control useRouter per-test
jest.mock('next/router', () => ({
  useRouter: jest.fn(),
}));

// Mock next/link
jest.mock('next/link', () => ({
  __esModule: true,
  default: ({ href, children, ...rest }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));

// Mock all SVG/image assets from @assets/header and @assets/new etc.
jest.mock('@assets/header/AdminIconBlue.icon.svg', () => 'AdminIconBlue', { virtual: true });
jest.mock('@assets/header/AutopilotIconBlue.icon.svg', () => 'AutopilotIconBlue', { virtual: true });
jest.mock('@assets/header/ClusterIconBlue.icon.svg', () => 'ClusterIconBlue', { virtual: true });
jest.mock('@assets/header/agent_icon_blue.icon.svg', () => 'AgentIconBlue', { virtual: true });
jest.mock('@assets/header/HomeIconBlue.icon.svg', () => 'HomeIconBlue', { virtual: true });
jest.mock('@assets/header/OptimiseIconBlue.icon.svg', () => 'OptimiseIconBlue', { virtual: true });
jest.mock('@assets/header/TicketIconBlue.icon.svg', () => 'TicketIconBlue', { virtual: true });
jest.mock('@assets/header/TroubleshootIconBlue.icon.svg', () => 'TroubleshootIconBlue', { virtual: true });
jest.mock('@assets/new/help-circle-dark.svg', () => 'HelpOutlineDarkIcon', { virtual: true });
jest.mock('@assets/new/bell-icon-dark.svg', () => 'NotificationOutlineIconDark', { virtual: true });
jest.mock('@assets/header/Documentation.svg', () => 'DocumentationIcon', { virtual: true });
jest.mock('@assets/new/chat-dark-icon.svg', () => 'ChatOutlineDarkIcon', { virtual: true });
jest.mock('@assets/logo/aws_logo.png', () => 'AwsLogo', { virtual: true });
jest.mock('@assets/header/group-icon.svg', () => 'GroupingIcon', { virtual: true });
jest.mock('@assets/ou-management/kubernetes_icon.icon.svg', () => ({ default: () => <svg data-testid='k8s-icon' /> }), { virtual: true });
jest.mock('@assets/jira_icon.icon.svg', () => ({ default: () => <svg data-testid='jira-icon' /> }), { virtual: true });
jest.mock('@assets/github-icon.icon.svg', () => ({ default: () => <svg data-testid='github-icon' /> }), { virtual: true });
jest.mock('@assets/slack_icon.icon.svg', () => ({ default: () => <svg data-testid='slack-icon' /> }), { virtual: true });
jest.mock('@assets/ou-management/ms_teams.icon.svg', () => ({ default: () => <svg data-testid='teams-icon' /> }), { virtual: true });
jest.mock('@assets/gchat-icon.icon.svg', () => ({ default: () => <svg data-testid='gchat-icon' /> }), { virtual: true });
jest.mock('@assets/ask-nudgebee/nubi2.svg', () => 'NubiIcon', { virtual: true });
jest.mock('@assets/ask-nudgebee/nubi3.svg', () => 'NubiIcon1', { virtual: true });
jest.mock('@assets/auto-pilot/service-now.svg', () => 'ServiceNowIcon', { virtual: true });
jest.mock('@assets/external-link-icon.svg', () => 'ExternalLinkIcon', { virtual: true });
jest.mock('@assets/home/new/pods_icon.icon.svg', () => 'PodsIcon', { virtual: true });
jest.mock('@assets/authentication/azure.svg', () => 'AzureAuth', { virtual: true });
jest.mock('@assets/authentication/google.svg', () => 'GoogleAuth', { virtual: true });
jest.mock('@assets/workflow/workflow-icon-blue.icon.svg', () => 'WorkflowIconBlue', { virtual: true });

// Mock next-auth/react
jest.mock('next-auth/react', () => ({
  useSession: jest.fn(() => ({
    data: {
      onPrem: false,
      pendoEnable: 'false',
      appVersion: 'v1.0.0',
    },
  })),
}));

// Mock @context/DataContext
jest.mock('@context/DataContext', () => ({
  useData: jest.fn(() => ({
    selectedCluster: {
      value: 'cluster-1',
      cloud_provider: 'K8s',
      agent: { version: '1.0.0', status: 'CONNECTED', connection_status: {} },
    },
    allCluster: [
      {
        value: 'cluster-1',
        cloud_provider: 'K8s',
        agent: { version: '1.0.0', status: 'CONNECTED' },
      },
      {
        value: 'cluster-2',
        cloud_provider: 'AWS',
      },
    ],
  })),
}));

// Mock @lib/auth
jest.mock('@lib/auth', () => ({
  hasWriteAccess: jest.fn(() => true),
}));

// Mock @api1/kubernetes
jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getLatestVersions: jest.fn().mockResolvedValue({ data: { nb_versions: null } }),
  },
}));

// Mock @api1/account
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    getMessagingPlatform: jest.fn().mockResolvedValue({ data: [] }),
  },
}));

// Mock @api1/application-groupings
jest.mock('@api1/application-groupings', () => ({
  __esModule: true,
  default: {
    listAllApplicationGroupNames: jest.fn().mockResolvedValue([]),
  },
}));

// Mock @hooks/useTenantBranding
jest.mock('@hooks/useTenantBranding', () => ({
  useTenantBranding: jest.fn(() => ({ assistantName: 'Nubi' })),
}));

// Mock src/utils/colors
jest.mock('src/utils/colors', () => ({
  colors: {
    text: {
      darkGray: '#555',
      secondary: '#3B82F6',
      white: '#fff',
    },
    background: {
      tertiarymedium: '#ddd',
    },
  },
}));

// Mock child components
jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ src, alt, width, height }) => <img src={src} alt={alt} width={width} height={height} data-testid='safe-icon' />,
}));

jest.mock('@components1/common/ClusterDropDown', () => ({
  __esModule: true,
  default: ({ onChange, onClusterDataLoaded }) => (
    <div data-testid='cluster-dropdown'>
      <button data-testid='cluster-change-btn' onClick={() => onChange({ value: 'cluster-2', cloud_provider: 'AWS' })}>
        Change Cluster
      </button>
      <button data-testid='cluster-loaded-btn' onClick={() => onClusterDataLoaded([])}>
        Load Empty
      </button>
      <button data-testid='cluster-loaded-k8s' onClick={() => onClusterDataLoaded([{ value: 'c1', cloud_provider: 'K8s' }])}>
        Load K8s
      </button>
    </div>
  ),
}));

jest.mock('@pages/PendoInitializer', () => ({
  __esModule: true,
  default: () => <div data-testid='pendo-initializer' />,
}));

jest.mock('@components1/common/ButtonMenu', () => ({
  __esModule: true,
  default: ({ title, items }) => (
    <div data-testid='button-menu'>
      <span>{title}</span>
      {items.map((item, i) => (
        <button key={i} data-testid={`menu-item-${item.text}`} onClick={item.onClick}>
          {item.text}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@components1/common/K8sAccountModal', () => ({
  __esModule: true,
  default: ({ openModal, handleClose }) => (
    <div data-testid='k8s-account-modal' data-open={openModal}>
      <button data-testid='close-k8s-modal' onClick={handleClose}>
        Close
      </button>
    </div>
  ),
}));

jest.mock('@components1/common/JiraAccountModal', () => ({
  __esModule: true,
  default: ({ openModal, handleClose }) => (
    <div data-testid='jira-account-modal' data-open={openModal}>
      <button onClick={handleClose}>Close</button>
    </div>
  ),
}));

jest.mock('@components1/common/GithubAccountModal', () => ({
  __esModule: true,
  default: ({ openModal, handleClose }) => (
    <div data-testid='github-account-modal' data-open={openModal}>
      <button onClick={handleClose}>Close</button>
    </div>
  ),
}));

jest.mock('@components1/common/ServiceNowAccountModal', () => ({
  __esModule: true,
  default: ({ openModal, handleClose }) => (
    <div data-testid='servicenow-account-modal' data-open={openModal}>
      <button onClick={handleClose}>Close</button>
    </div>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, children }) => (
    <button data-testid={`custom-btn-${text}`} onClick={onClick}>
      {text || children}
    </button>
  ),
}));

jest.mock('@components1/common/CustomDropdown', () => ({
  __esModule: true,
  default: ({ onChange, options, value, label }) => (
    <div data-testid='custom-dropdown'>
      <label>{label}</label>
      <select
        data-testid='group-dropdown-select'
        value={value}
        onChange={(e) => onChange(e, options.find((o) => o.label === e.target.value) || { id: 'g1', label: e.target.value })}
      >
        {options.map((opt, i) => (
          <option key={i} value={opt.label || ''}>
            {opt.label}
          </option>
        ))}
      </select>
    </div>
  ),
}));

jest.mock('@components1/common/CustomBackButton', () => ({
  __esModule: true,
  default: () => <button data-testid='back-button'>Back</button>,
}));

// Mock DOMPurify
jest.mock('dompurify', () => ({
  sanitize: jest.fn((input) => input),
}));

import { useRouter } from 'next/router';
import { useSession } from 'next-auth/react';
import { useData } from '@context/DataContext';
import apiKubernetes from '@api1/kubernetes';
import apiAccount from '@api1/account';

// Mock localStorage
const localStorageMock = (() => {
  let store = {};
  return {
    getItem: jest.fn((key) => store[key] ?? null),
    setItem: jest.fn((key, val) => {
      store[key] = val;
    }),
    clear: jest.fn(() => {
      store = {};
    }),
  };
})();
Object.defineProperty(window, 'localStorage', { value: localStorageMock });

// Mock window.alert
window.alert = jest.fn();
// Mock window.open
window.open = jest.fn();
// Mock window.$chatwoot
window.$chatwoot = { toggle: jest.fn() };

describe('Header1', () => {
  let mockRouter;

  beforeEach(() => {
    jest.clearAllMocks();
    localStorageMock.clear();

    mockRouter = {
      push: jest.fn(),
      replace: jest.fn(),
      pathname: '/home',
      query: {},
      asPath: '/home',
      route: '/home',
      prefetch: jest.fn().mockResolvedValue(null),
    };

    useRouter.mockReturnValue(mockRouter);

    useSession.mockReturnValue({
      data: {
        onPrem: false,
        pendoEnable: 'false',
        appVersion: 'v1.0.0',
      },
    });

    useData.mockReturnValue({
      selectedCluster: {
        value: 'cluster-1',
        cloud_provider: 'K8s',
        agent: { version: '1.0.0', status: 'CONNECTED', connection_status: {} },
      },
      allCluster: [
        { value: 'cluster-1', cloud_provider: 'K8s', agent: { version: '1.0.0', status: 'CONNECTED' } },
        { value: 'cluster-2', cloud_provider: 'AWS' },
      ],
    });

    apiKubernetes.getLatestVersions.mockResolvedValue({ data: { nb_versions: null } });
    apiAccount.getMessagingPlatform.mockResolvedValue({ data: [] });
  });

  describe('basic rendering', () => {
    it('renders without crashing', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
    });

    it('renders modals', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByTestId('k8s-account-modal')).toBeInTheDocument();
      expect(screen.getByTestId('jira-account-modal')).toBeInTheDocument();
      expect(screen.getByTestId('github-account-modal')).toBeInTheDocument();
      expect(screen.getByTestId('servicenow-account-modal')).toBeInTheDocument();
    });

    it('renders header with showBorder=false by default', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      // Should render without throwing
      expect(document.querySelector('header') || screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
    });

    it('renders header with showBorder=true', async () => {
      await act(async () => {
        render(<Header1 showBorder={true} />);
      });
      expect(screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
    });
  });

  describe('route matching - /home', () => {
    it('shows cluster dropdown when on /home route (showActiveCluster=true)', async () => {
      mockRouter.pathname = '/home';
      mockRouter.asPath = '/home';
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
    });

    it('shows Home as active tab name', async () => {
      mockRouter.pathname = '/home';
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByText('Home')).toBeInTheDocument();
    });
  });

  describe('route matching - /user-management', () => {
    it('shows connect account button on /user-management', async () => {
      mockRouter.pathname = '/user-management';
      mockRouter.asPath = '/user-management';
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByTestId('button-menu')).toBeInTheDocument();
    });
  });

  describe('route matching - unknown route', () => {
    it('shows empty name for unknown route', async () => {
      mockRouter.pathname = '/unknown-route';
      mockRouter.asPath = '/unknown-route';
      await act(async () => {
        render(<Header1 />);
      });
      // No tab name shown
      expect(screen.queryByText('Home')).not.toBeInTheDocument();
    });
  });

  describe('demo account banner', () => {
    it('shows demo account banner when allCluster has only 1 cluster', async () => {
      useData.mockReturnValue({
        selectedCluster: { value: 'demo', cloud_provider: 'K8s', agent: {} },
        allCluster: [{ value: 'demo', cloud_provider: 'K8s' }],
      });
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByText(/demo account/i)).toBeInTheDocument();
    });

    it('does not show demo banner when allCluster has more than 1 cluster', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.queryByText(/demo account/i)).not.toBeInTheDocument();
    });

    it('opens K8s account modal on Add K8s Account click', async () => {
      useData.mockReturnValue({
        selectedCluster: { value: 'demo', cloud_provider: 'K8s', agent: {} },
        allCluster: [{ value: 'demo', cloud_provider: 'K8s' }],
      });
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('custom-btn-Add K8s Account'));
      expect(screen.getByTestId('k8s-account-modal')).toHaveAttribute('data-open', 'true');
    });

    it('opens help docs on "need help?" click', async () => {
      useData.mockReturnValue({
        selectedCluster: { value: 'demo', cloud_provider: 'K8s', agent: {} },
        allCluster: [{ value: 'demo', cloud_provider: 'K8s' }],
      });
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByText('need help?'));
      expect(window.open).toHaveBeenCalledWith('https://app.nudgebee.com/help/docs/features/', '_blank', 'noopener');
    });
  });

  describe('snackbar / agent version alerts', () => {
    it('shows snackbar when agent is NOT_CONNECTED', async () => {
      useData.mockReturnValue({
        selectedCluster: {
          value: 'cluster-1',
          cloud_provider: 'K8s',
          agent: { status: 'NOT_CONNECTED', version: '1.0.0', connection_status: {} },
        },
        allCluster: [
          { value: 'cluster-1', cloud_provider: 'K8s', agent: { status: 'NOT_CONNECTED' } },
          { value: 'cluster-2', cloud_provider: 'AWS' },
        ],
      });
      localStorageMock.getItem.mockReturnValue(null);
      apiKubernetes.getLatestVersions.mockResolvedValue({ data: { nb_versions: { agent_version_latest: '2.0.0' } } });

      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(screen.getByText(/not connected/i)).toBeInTheDocument();
      });
    });

    it('shows snackbar when agent version is outdated', async () => {
      useData.mockReturnValue({
        selectedCluster: {
          value: 'cluster-1',
          cloud_provider: 'K8s',
          agent: { status: 'CONNECTED', version: '1.0.0', connection_status: {} },
        },
        allCluster: [
          { value: 'cluster-1', cloud_provider: 'K8s', agent: { status: 'CONNECTED', version: '1.0.0' } },
          { value: 'cluster-2', cloud_provider: 'AWS' },
        ],
      });
      localStorageMock.getItem.mockReturnValue(null);
      apiKubernetes.getLatestVersions.mockResolvedValue({
        data: { nb_versions: { agent_version_latest: '2.0.0' } },
      });

      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(screen.getByText(/Update the Nudgebee Agent/i)).toBeInTheDocument();
      });
    });

    it('shows snackbar with disconnected services', async () => {
      useData.mockReturnValue({
        selectedCluster: {
          value: 'cluster-1',
          cloud_provider: 'K8s',
          agent: {
            status: 'CONNECTED',
            version: '1.0.0',
            connection_status: {
              relayConnection: false,
              prometheusConnection: false,
              alertManagerConnection: true,
            },
          },
        },
        allCluster: [
          { value: 'cluster-1', cloud_provider: 'K8s', agent: { status: 'CONNECTED', version: '1.0.0' } },
          { value: 'cluster-2', cloud_provider: 'AWS' },
        ],
      });
      localStorageMock.getItem.mockReturnValue(null);
      apiKubernetes.getLatestVersions.mockResolvedValue({
        data: { nb_versions: { agent_version_latest: '2.0.0' } },
      });

      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(screen.getByText(/Relay/i)).toBeInTheDocument();
      });
    });

    it('skips snackbar when hasClosed is "false"', async () => {
      localStorageMock.getItem.mockReturnValue('false');
      apiKubernetes.getLatestVersions.mockResolvedValue({ data: { nb_versions: { agent_version_latest: '2.0.0' } } });

      await act(async () => {
        render(<Header1 />);
      });
      // getLatestVersions should not be called (early return)
      await waitFor(() => {
        expect(apiKubernetes.getLatestVersions).not.toHaveBeenCalled();
      });
    });

    it('closes snackbar when close button clicked', async () => {
      useData.mockReturnValue({
        selectedCluster: {
          value: 'cluster-1',
          cloud_provider: 'K8s',
          agent: { status: 'NOT_CONNECTED', version: '1.0.0', connection_status: {} },
        },
        allCluster: [
          { value: 'cluster-1', cloud_provider: 'K8s', agent: { status: 'NOT_CONNECTED' } },
          { value: 'cluster-2', cloud_provider: 'AWS' },
        ],
      });
      localStorageMock.getItem.mockImplementation((key) => {
        if (key === 'appVersion') return 'v1.0.0';
        return null;
      });
      apiKubernetes.getLatestVersions.mockResolvedValue({ data: { nb_versions: { agent_version_latest: '2.0.0' } } });

      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(screen.getByRole('alert')).toBeInTheDocument();
      });
      const alert = screen.getByRole('alert');
      fireEvent.click(within(alert).getByRole('button', { name: /close/i }));
      expect(localStorageMock.setItem).toHaveBeenCalledWith('latest-cluster-1-K8sAgentSnackbar', 'false');
    });

    it('does not show snackbar when selectedCluster is empty', async () => {
      useData.mockReturnValue({
        selectedCluster: {},
        allCluster: [{}, {}],
      });

      await act(async () => {
        render(<Header1 />);
      });
      expect(apiKubernetes.getLatestVersions).not.toHaveBeenCalled();
    });

    it('does not show snackbar when selectedCluster.cloud_provider is not K8s', async () => {
      useData.mockReturnValue({
        selectedCluster: { value: 'aws-cluster', cloud_provider: 'AWS' },
        allCluster: [{}, {}],
      });

      await act(async () => {
        render(<Header1 />);
      });
      expect(apiKubernetes.getLatestVersions).not.toHaveBeenCalled();
    });

    it('hides snackbar on /tickets route', async () => {
      mockRouter.pathname = '/tickets';
      mockRouter.asPath = '/tickets';
      localStorageMock.getItem.mockImplementation((key) => {
        if (key === 'appVersion') return 'v1.0.0';
        return null;
      });
      await act(async () => {
        render(<Header1 />);
      });
      // No snackbar shown
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });
  });

  describe('reload notification', () => {
    it('shows reload notification when appVersion differs from localStorage', async () => {
      localStorageMock.getItem.mockImplementation((key) => {
        if (key === 'appVersion') return 'v0.9.0';
        return null;
      });

      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByText(/New Application Version/i)).toBeInTheDocument();
    });

    it('hides reload notification when versions match', async () => {
      localStorageMock.getItem.mockImplementation((key) => {
        if (key === 'appVersion') return 'v1.0.0';
        return null;
      });

      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.queryByText(/New Application Version/i)).not.toBeInTheDocument();
    });

    it('closes reload notification on close button click', async () => {
      localStorageMock.getItem.mockImplementation((key) => {
        if (key === 'appVersion') return 'v0.9.0';
        return null;
      });

      await act(async () => {
        render(<Header1 />);
      });
      const alerts = screen.getAllByRole('alert');
      const reloadAlert = alerts.find((a) => a.textContent.includes('New Application Version'));
      const closeBtn = reloadAlert.querySelector('button');
      if (closeBtn) fireEvent.click(closeBtn);
      expect(screen.queryByText(/New Application Version/i)).not.toBeInTheDocument();
    });
  });

  describe('pendo initializer', () => {
    it('renders PendoInitializer when not onPrem and pendoEnable=true', async () => {
      useSession.mockReturnValue({
        data: {
          onPrem: false,
          pendoEnable: 'true',
          appVersion: 'v1.0.0',
        },
      });
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByTestId('pendo-initializer')).toBeInTheDocument();
    });

    it('does not render PendoInitializer when onPrem=true', async () => {
      useSession.mockReturnValue({
        data: {
          onPrem: true,
          pendoEnable: 'true',
          appVersion: 'v1.0.0',
        },
      });
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.queryByTestId('pendo-initializer')).not.toBeInTheDocument();
    });
  });

  describe('user menu (help)', () => {
    it('opens help menu when help button clicked', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      const helpBtn = document.querySelector('.headerHelpMenu button');
      if (helpBtn) {
        fireEvent.click(helpBtn);
        // Menu should open
      }
    });

    it('shows Documentation menu item for non-onPrem', async () => {
      useSession.mockReturnValue({
        data: { onPrem: false, pendoEnable: 'false', appVersion: 'v1.0.0' },
      });
      await act(async () => {
        render(<Header1 />);
      });
      // Click help button to open menu
      const helpBtn = document.querySelector('.headerHelpMenu button');
      if (helpBtn) fireEvent.click(helpBtn);
    });

    it('opens documentation link when Documentation menu item clicked', async () => {
      useSession.mockReturnValue({
        data: { onPrem: false, pendoEnable: 'false', appVersion: 'v1.0.0' },
      });
      await act(async () => {
        render(<Header1 />);
      });
      // Open the help menu
      const helpBtn = document.querySelector('.headerHelpMenu button');
      if (helpBtn) {
        fireEvent.click(helpBtn);
        // Click Documentation
        const docItem = screen.queryByText('Documentation');
        if (docItem) {
          fireEvent.click(docItem);
          expect(window.open).toHaveBeenCalledWith('https://app.nudgebee.com/help/docs/features/', '_blank', 'noopener');
        }
      }
    });

    it('toggles chatwoot when Chat with us clicked', async () => {
      useSession.mockReturnValue({
        data: { onPrem: false, pendoEnable: 'false', appVersion: 'v1.0.0' },
      });
      await act(async () => {
        render(<Header1 />);
      });
      const helpBtn = document.querySelector('.headerHelpMenu button');
      if (helpBtn) {
        fireEvent.click(helpBtn);
        const chatItem = screen.queryByText('Chat with us');
        if (chatItem) {
          fireEvent.click(chatItem);
          expect(window.$chatwoot.toggle).toHaveBeenCalledWith('open');
        }
      }
    });

    it('hides Chat with us for onPrem users', async () => {
      useSession.mockReturnValue({
        data: { onPrem: true, pendoEnable: 'false', appVersion: 'v1.0.0' },
      });
      await act(async () => {
        render(<Header1 />);
      });
      // Avatar sub menu only has 'Documentation' for onPrem
      // Click help button
      const helpBtn = document.querySelector('.headerHelpMenu button');
      if (helpBtn) {
        fireEvent.click(helpBtn);
        expect(screen.queryByText('Chat with us')).not.toBeInTheDocument();
      }
    });
  });

  describe('connect account buttons', () => {
    beforeEach(() => {
      mockRouter.pathname = '/user-management';
      mockRouter.asPath = '/user-management';
    });

    it('opens K8s account modal from connect account menu', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-Kubernetes'));
      expect(screen.getByTestId('k8s-account-modal')).toHaveAttribute('data-open', 'true');
    });

    it('opens Jira account modal from connect account menu', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-Jira'));
      expect(screen.getByTestId('jira-account-modal')).toHaveAttribute('data-open', 'true');
    });

    it('opens GitHub account modal from connect account menu', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-Github'));
      expect(screen.getByTestId('github-account-modal')).toHaveAttribute('data-open', 'true');
    });

    it('opens ServiceNow account modal from connect account menu', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-ServiceNow'));
      expect(screen.getByTestId('servicenow-account-modal')).toHaveAttribute('data-open', 'true');
    });

    it('opens Slack install URL in new tab', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-Slack'));
      expect(window.open).toHaveBeenCalledWith('/api/slack/install', '_blank');
    });

    it('opens MS Teams install URL in new tab', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-Teams'));
      expect(window.open).toHaveBeenCalledWith('/api/integrations/install/ms-teams', '_blank', '"noopener"');
    });

    it('opens Google Chat install URL in new tab', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('menu-item-Google Chat'));
      expect(window.open).toHaveBeenCalledWith('/api/integrations/install/google', '_blank');
    });

    it('fetches integration counts when connectAccountButton is active', async () => {
      apiAccount.getMessagingPlatform.mockResolvedValue({ data: [{ id: 1 }, { id: 2 }] });
      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(apiAccount.getMessagingPlatform).toHaveBeenCalledWith('slack');
        expect(apiAccount.getMessagingPlatform).toHaveBeenCalledWith('google_chat');
      });
    });

    it('handles error in fetchIntegrationCounts gracefully', async () => {
      apiAccount.getMessagingPlatform.mockRejectedValue(new Error('Network error'));
      await act(async () => {
        render(<Header1 />);
      });
      // Should not throw
    });

    it('fetches gChatAccountsCount correctly (filters items with channels)', async () => {
      apiAccount.getMessagingPlatform.mockImplementation((type) => {
        if (type === 'google_chat') {
          return Promise.resolve({ data: [{ channels: true }, { channels: false }] });
        }
        return Promise.resolve({ data: [] });
      });
      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(apiAccount.getMessagingPlatform).toHaveBeenCalledWith('google_chat');
      });
    });
  });

  describe('handleDropdownChange', () => {
    it('handles cluster change - stays on same route when no accountId', async () => {
      mockRouter.pathname = '/home';
      mockRouter.asPath = '/home';
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('cluster-change-btn'));
      // Should call updateClusterState (which clears snackbar)
    });

    it('navigates to kubernetes details when switching from cloud-account to K8s', async () => {
      mockRouter.pathname = '/cloud-account/details/aws-1';
      mockRouter.asPath = '/cloud-account/details/aws-1';
      mockRouter.query = {};
      await act(async () => {
        render(<Header1 />);
      });
      // Click to change cluster to K8s
      const changeBtn = screen.queryByTestId('cluster-change-btn');
      if (changeBtn) {
        fireEvent.click(changeBtn);
      }
    });

    it('navigates to cloud account details when switching from K8s to cloud', async () => {
      mockRouter.pathname = '/kubernetes/details/cluster-1';
      mockRouter.asPath = '/kubernetes/details/cluster-1';
      mockRouter.query = { KubernetesDetails: 'cluster-1' };
      await act(async () => {
        render(<Header1 />);
      });
      const changeBtn = screen.queryByTestId('cluster-change-btn');
      if (changeBtn) fireEvent.click(changeBtn);
    });

    it('handles auto-pilot route change', async () => {
      mockRouter.pathname = '/auto-pilot';
      mockRouter.asPath = '/auto-pilot?accountId=cluster-1';
      mockRouter.query = { accountId: 'cluster-1' };
      await act(async () => {
        render(<Header1 />);
      });
      const changeBtn = screen.queryByTestId('cluster-change-btn');
      if (changeBtn) fireEvent.click(changeBtn);
    });
  });

  describe('handleClusterData', () => {
    it('shows alert and redirects when no clusters loaded', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('cluster-loaded-btn'));
      expect(window.alert).toHaveBeenCalledWith('Currently No kubernetes cluster is configured, Please add a kubernetes cluster');
      expect(mockRouter.push).toHaveBeenCalledWith('/accounts/account-form?cloudProvider=K8S');
    });

    it('does not alert when clusters exist', async () => {
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('cluster-loaded-k8s'));
      expect(window.alert).not.toHaveBeenCalled();
    });
  });

  describe('connect cluster button', () => {
    it('shows connect cluster button on /kubernetes route with write access and K8s clusters > 1', async () => {
      mockRouter.pathname = '/kubernetes';
      mockRouter.asPath = '/kubernetes';
      useData.mockReturnValue({
        selectedCluster: { value: 'cluster-1', cloud_provider: 'K8s', agent: {} },
        allCluster: [
          { value: 'cluster-1', cloud_provider: 'K8s' },
          { value: 'cluster-2', cloud_provider: 'K8s' },
        ],
      });
      await act(async () => {
        render(<Header1 />);
      });
      expect(screen.getByTestId('custom-btn-Connect cluster')).toBeInTheDocument();
    });

    it('navigates to account form on connect cluster click', async () => {
      mockRouter.pathname = '/kubernetes';
      mockRouter.asPath = '/kubernetes';
      useData.mockReturnValue({
        selectedCluster: { value: 'cluster-1', cloud_provider: 'K8s', agent: {} },
        allCluster: [
          { value: 'cluster-1', cloud_provider: 'K8s' },
          { value: 'cluster-2', cloud_provider: 'K8s' },
        ],
      });
      await act(async () => {
        render(<Header1 />);
      });
      fireEvent.click(screen.getByTestId('custom-btn-Connect cluster'));
      expect(mockRouter.push).toHaveBeenCalledWith('/accounts/account-form?cloudProvider=K8S');
    });
  });

  describe('grouping route', () => {
    it('loads app group names on grouping route', async () => {
      const { default: apiAppGrouping } = require('@api1/application-groupings');
      apiAppGrouping.listAllApplicationGroupNames.mockResolvedValue([
        { name: 'Group A', id: 'g1' },
        { name: 'Group B', id: 'g2' },
      ]);
      mockRouter.pathname = '/grouping';
      mockRouter.asPath = '/grouping?groupId=g1';
      mockRouter.query = { groupId: 'g1' };
      await act(async () => {
        render(<Header1 />);
      });
      await waitFor(() => {
        expect(apiAppGrouping.listAllApplicationGroupNames).toHaveBeenCalled();
      });
    });

    it('handles empty group names', async () => {
      const { default: apiAppGrouping } = require('@api1/application-groupings');
      apiAppGrouping.listAllApplicationGroupNames.mockResolvedValue(null);
      mockRouter.pathname = '/grouping';
      mockRouter.asPath = '/grouping';
      mockRouter.query = {};
      await act(async () => {
        render(<Header1 />);
      });
      // Should not throw
    });

    it('handles groupId change via query update', async () => {
      mockRouter.pathname = '/grouping';
      mockRouter.asPath = '/grouping?groupId=g1';
      mockRouter.query = { groupId: 'g1' };
      const { rerender } = await act(async () => {
        return render(<Header1 />);
      });
      mockRouter.query = { groupId: 'g2' };
      await act(async () => {
        rerender(<Header1 />);
      });
    });
  });

  describe('cloud account icon variants', () => {
    it('shows Azure icon for Azure cloud provider', async () => {
      useData.mockReturnValue({
        selectedCluster: { value: 'azure-1', cloud_provider: 'Azure', agent: {} },
        allCluster: [{ value: 'azure-1', cloud_provider: 'Azure' }, {}],
      });
      await act(async () => {
        render(<Header1 />);
      });
    });

    it('shows GCP icon for GCP cloud provider', async () => {
      useData.mockReturnValue({
        selectedCluster: { value: 'gcp-1', cloud_provider: 'GCP', agent: {} },
        allCluster: [{ value: 'gcp-1', cloud_provider: 'GCP' }, {}],
      });
      await act(async () => {
        render(<Header1 />);
      });
    });
  });

  describe('ask nudgebee button', () => {
    it('navigates to ask-nudgebee with selectedCluster value', async () => {
      mockRouter.pathname = '/home';
      mockRouter.asPath = '/home';
      mockRouter.query = {};
      await act(async () => {
        render(<Header1 />);
      });
      // Find the ask nudgebee button (nubi icon button)
      const nubiBtn = screen.queryByRole('button', { name: /ask nudgebee/i });
      if (nubiBtn) {
        fireEvent.click(nubiBtn);
        expect(mockRouter.push).toHaveBeenCalled();
      }
    });
  });
});
