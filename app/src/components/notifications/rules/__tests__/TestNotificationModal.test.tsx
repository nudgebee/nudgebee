import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import TestNotificationModal from '@components/notifications/rules/TestNotificationModal';
import apiNotifications, { NotificationRouteCheck } from '@api1/notification';

jest.mock('@api1/notification', () => ({
  __esModule: true,
  default: { checkRoute: jest.fn() },
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { getAllK8sNamespaces: jest.fn(() => Promise.resolve({ data: [] })) },
}));

const checkRoute = apiNotifications.checkRoute as jest.Mock;

const answer = (overrides: Partial<NotificationRouteCheck>): NotificationRouteCheck => ({
  result: 'sent',
  reason: 'default_policy',
  stopped_by: null,
  rules: [],
  channels: [],
  default_channels: true,
  ...overrides,
});

const props = { defaultType: null, accounts: [{ id: 'acct-1', name: 'prod' }] as any, onClose: jest.fn(), onShowRule: jest.fn() };

beforeEach(() => checkRoute.mockReset());

it('says an approval went out because approvals always do, not because of a rule', async () => {
  checkRoute.mockResolvedValue({ answer: answer({ reason: 'always_sent' }) });
  render(<TestNotificationModal open {...props} />);

  expect(await screen.findByText('Approval requests always go out, whatever the rules say.')).toBeInTheDocument();
  expect(screen.queryByText(/^By /)).not.toBeInTheDocument();
});

it('never shows the previous answer while the next check is loading', async () => {
  checkRoute.mockResolvedValue({ answer: answer({ reason: 'default_policy' }) });
  const { rerender } = render(<TestNotificationModal open {...props} />);
  const previous = 'No rule covers it, so “When no rule matches” decides.';
  expect(await screen.findByText(previous)).toBeInTheDocument();

  checkRoute.mockReturnValue(new Promise(() => {}));
  rerender(<TestNotificationModal open={false} {...props} />);
  rerender(<TestNotificationModal open {...props} />);

  expect(screen.queryByText(previous)).not.toBeInTheDocument();
  expect(document.getElementById('test-answer')).toBeNull();
});
