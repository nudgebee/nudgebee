import { render, screen } from '@testing-library/react';
import ChannelContextDrawerContent from '@components/llm/common/ChannelContextDrawerContent';

const reference = (overrides = {}) => ({
  id: 'r1',
  type: 'channel_context',
  content: 'payments-incident',
  metadata: {
    platform: 'slack',
    team_id: 'T1',
    channel_id: 'C1',
    channel_name: 'payments-incident',
    messages: [
      {
        id: '1753.100',
        author: 'Dana',
        posted_at: '2026-07-22T09:14:00Z',
        preview: 'primary failing health checks',
        permalink: 'https://acme.slack.com/archives/C1/p1753100',
      },
      {
        id: '1753.200',
        author: 'Arjun',
        posted_at: '2026-07-24T10:42:00Z',
        preview: 'standby promoted',
        permalink: null,
      },
    ],
  },
  ...overrides,
});

describe('ChannelContextDrawerContent', () => {
  it('renders the channel header with message count and span', () => {
    render(<ChannelContextDrawerContent references={[reference()]} />);
    expect(screen.getByText('payments-incident')).toBeInTheDocument();
    expect(screen.getByText(/2 messages/)).toBeInTheDocument();
  });

  it('links each message to its Slack permalink, falling back to the channel', () => {
    render(<ChannelContextDrawerContent references={[reference()]} />);
    const links = screen.getAllByLabelText('Open in Slack');
    expect(links[0]).toHaveAttribute('href', 'https://acme.slack.com/archives/C1/p1753100');
    // No permalink → channel-level web client URL, so the citation still leads somewhere.
    expect(links[1]).toHaveAttribute('href', 'https://app.slack.com/client/T1/C1');
  });

  it('ignores non-channel references and says so when none remain', () => {
    render(<ChannelContextDrawerContent references={[{ id: 'x', type: 'memory' }]} />);
    expect(screen.getByText(/No channel conversation/)).toBeInTheDocument();
  });

  it('survives a reference with no metadata', () => {
    render(<ChannelContextDrawerContent references={[{ id: 'r2', type: 'channel_context', content: 'ops' }]} />);
    expect(screen.getByText('ops')).toBeInTheDocument();
  });
});
