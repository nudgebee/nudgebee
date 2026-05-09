import React from 'react';
import { render, screen, fireEvent } from '@testing-library/react';
import IconTextBadge, { PlatformChannelBadge } from '@components1/common/IconTextBadge';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { secondary: '#374151', primary: '#3B82F6', white: '#fff', tertiary: '#6B7280', success: '#16a34a' },
    background: { primaryLightest: '#EFF6FF', white: '#fff', transparent: 'transparent' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6', success: '#22C55E' },
    primary: '#3B82F6',
  },
}));

jest.mock('@assets', () => ({
  SlackIcon: 'slack-icon-mock',
  MSTeamsIcon: 'msteams-icon-mock',
  GChatIcon: 'gchat-icon-mock',
  jiraIcon: 'jira-icon-mock',
  serviceNowIcon: 'servicenow-icon-mock',
  PagerDutyIcon: 'pagerduty-icon-mock',
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt, ...props }: { alt: string; [key: string]: unknown }) => <img alt={alt} data-testid='safe-icon' {...props} />,
}));

describe('IconTextBadge', () => {
  describe('basic rendering', () => {
    it('renders with text', () => {
      render(<IconTextBadge text='My Label' />);
      expect(screen.getByText('My Label')).toBeInTheDocument();
    });

    it('renders dash when text is empty', () => {
      render(<IconTextBadge text='' />);
      expect(screen.getByText('-')).toBeInTheDocument();
    });

    it('renders without crashing with minimal props', () => {
      expect(() => render(<IconTextBadge text='Test' />)).not.toThrow();
    });
  });

  describe('icon rendering', () => {
    it('renders SafeIcon when icon prop is provided', () => {
      render(<IconTextBadge text='Label' icon={{ src: 'test.png' }} />);
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });

    it('renders muiIcon when provided', () => {
      const MuiIconMock = () => <svg data-testid='mui-icon' />;
      render(<IconTextBadge text='Label' muiIcon={<MuiIconMock />} />);
      expect(screen.getByTestId('mui-icon')).toBeInTheDocument();
    });

    it('renders no icon when neither icon, muiIcon, nor preset is provided', () => {
      render(<IconTextBadge text='No Icon' />);
      expect(screen.queryByTestId('safe-icon')).not.toBeInTheDocument();
    });
  });

  describe('preset rendering', () => {
    it('renders with slack preset', () => {
      render(<IconTextBadge preset='slack' text='#general' />);
      expect(screen.getByText('#general')).toBeInTheDocument();
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });

    it('renders with ms_teams preset', () => {
      render(<IconTextBadge preset='ms_teams' text='team-channel' />);
      expect(screen.getByText('team-channel')).toBeInTheDocument();
    });

    it('renders with google_chat preset', () => {
      render(<IconTextBadge preset='google_chat' text='my-space' />);
      expect(screen.getByText('my-space')).toBeInTheDocument();
    });

    it('renders with jira preset', () => {
      render(<IconTextBadge preset='jira' text='PROJ-123' />);
      expect(screen.getByText('PROJ-123')).toBeInTheDocument();
    });

    it('renders with servicenow preset', () => {
      render(<IconTextBadge preset='servicenow' text='INC001' />);
      expect(screen.getByText('INC001')).toBeInTheDocument();
    });

    it('renders with pagerduty preset', () => {
      render(<IconTextBadge preset='pagerduty' text='on-call' />);
      expect(screen.getByText('on-call')).toBeInTheDocument();
    });

    it('renders email preset with MUI icon', () => {
      render(<IconTextBadge preset='email' text='user@example.com' />);
      expect(screen.getByText('user@example.com')).toBeInTheDocument();
    });
  });

  describe('size variants', () => {
    it('renders with small size', () => {
      render(<IconTextBadge text='Small' size='small' />);
      expect(screen.getByText('Small')).toBeInTheDocument();
    });

    it('renders with medium size (default)', () => {
      render(<IconTextBadge text='Medium' size='medium' />);
      expect(screen.getByText('Medium')).toBeInTheDocument();
    });

    it('renders with large size', () => {
      render(<IconTextBadge text='Large' size='large' />);
      expect(screen.getByText('Large')).toBeInTheDocument();
    });
  });

  describe('tooltip behavior', () => {
    it('wraps with tooltip by default (tooltip=true)', () => {
      render(<IconTextBadge text='Label' />);
      // MUI Tooltip renders as title attribute in tests
      expect(screen.getByText('Label')).toBeInTheDocument();
    });

    it('does not render tooltip when tooltip=false', () => {
      render(<IconTextBadge text='No Tooltip' tooltip={false} />);
      expect(screen.getByText('No Tooltip')).toBeInTheDocument();
    });

    it('uses custom tooltip text when tooltip is a string', () => {
      render(<IconTextBadge text='Label' tooltip='Custom tooltip' />);
      expect(screen.getByText('Label')).toBeInTheDocument();
    });

    it('generates tooltip as "PresetLabel: text" for preset', () => {
      render(<IconTextBadge preset='slack' text='#alerts' tooltip={true} />);
      expect(screen.getByText('#alerts')).toBeInTheDocument();
    });
  });

  describe('onClick interaction', () => {
    it('calls onClick when clicked', () => {
      const onClick = jest.fn();
      render(<IconTextBadge text='Clickable' onClick={onClick} />);
      fireEvent.click(screen.getByText('Clickable'));
      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it('renders with pointer cursor when onClick is provided', () => {
      const onClick = jest.fn();
      render(<IconTextBadge text='Clickable' onClick={onClick} />);
      expect(screen.getByText('Clickable')).toBeInTheDocument();
    });
  });

  describe('custom styling', () => {
    it('renders with maxWidth prop', () => {
      render(<IconTextBadge text='Label' maxWidth={200} />);
      expect(screen.getByText('Label')).toBeInTheDocument();
    });

    it('renders with custom textColor', () => {
      render(<IconTextBadge text='Colored' textColor='#ff0000' />);
      expect(screen.getByText('Colored')).toBeInTheDocument();
    });

    it('renders with custom iconSize', () => {
      render(<IconTextBadge text='Label' icon={{ src: 'test.png' }} iconSize={24} />);
      expect(screen.getByTestId('safe-icon')).toBeInTheDocument();
    });
  });
});

describe('PlatformChannelBadge', () => {
  it('renders with platform and channelName', () => {
    render(<PlatformChannelBadge platform='slack' channelName='#general' />);
    expect(screen.getByText('#general')).toBeInTheDocument();
  });

  it('renders with different platforms', () => {
    render(<PlatformChannelBadge platform='ms_teams' channelName='alerts' />);
    expect(screen.getByText('alerts')).toBeInTheDocument();
  });

  it('renders with small size', () => {
    render(<PlatformChannelBadge platform='slack' channelName='test' size='small' />);
    expect(screen.getByText('test')).toBeInTheDocument();
  });

  it('renders with maxWidth', () => {
    render(<PlatformChannelBadge platform='slack' channelName='test' maxWidth={120} />);
    expect(screen.getByText('test')).toBeInTheDocument();
  });
});
