import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import DevOpsTimelineMUI from '@shared/widgets/DevOpsTimelineMUI';
import apiKubernetes1 from '@api1/kubernetes1';

jest.mock('@utils/colors');

jest.mock('@api1/kubernetes1', () => ({
  getTimelineData: jest.fn(),
}));

jest.mock('@assets', () => ({ ExternalLinkIcon: 'mock-external-link-icon' }), { virtual: true });

jest.mock('next/image', () => ({
  __esModule: true,
  default: (props: React.ImgHTMLAttributes<HTMLImageElement>) => React.createElement('img', { ...props, alt: props.alt }),
}));

jest.mock('@ui/WidgetCard', () => ({ children }: { children: React.ReactNode }) => <div data-testid='widget-card'>{children}</div>);
jest.mock('@shared/Loader', () => () => <div data-testid='loader'>Loading...</div>);
jest.mock('@shared/buttons/CopyButton', () => () => <button data-testid='copy-button' />);
jest.mock('@shared/icons/SafeIcon', () => () => <span data-testid='safe-icon' />);
jest.mock('@ui/Toast', () => ({ toast: { error: jest.fn(), success: jest.fn() } }));

describe('DevOpsTimelineMUI', () => {
  const mockEventId = 'event-123';
  const mockTimelineData = {
    data: {
      data: {
        event_get_timeline: {
          event_id: 'event-123',
          timeline: [
            {
              timestamp: '2023-10-27T10:00:00Z',
              ref_type: 'event',
              ref_id: 'evt-1',
              action: 'fired',
              summary: 'Alert fired',
              metadata: {},
            },
            {
              timestamp: '2023-10-27T11:00:00Z',
              ref_type: 'workload',
              ref_id: 'wl-1',
              action: 'created',
              summary: 'Workload created',
              metadata: {
                cloud_account_id: 'acc-1',
                namespace: 'ns-1',
                workload_name: 'deployment-1',
              },
            },
          ],
        },
      },
    },
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  test('renders timeline events correctly', async () => {
    (apiKubernetes1.getTimelineData as jest.Mock).mockResolvedValue(mockTimelineData);

    render(<DevOpsTimelineMUI eventId={mockEventId} />);

    await waitFor(() => {
      expect(screen.getByText('Alert fired')).toBeInTheDocument();
    });

    expect(screen.getByText('Workload created')).toBeInTheDocument();
    expect(screen.getByText('event-123')).toBeInTheDocument();
  });

  test('shows loader while fetching', () => {
    (apiKubernetes1.getTimelineData as jest.Mock).mockReturnValue(new Promise(() => {}));
    render(<DevOpsTimelineMUI eventId={mockEventId} />);
    expect(screen.getByTestId('loader')).toBeInTheDocument();
  });

  test('shows empty state when no timeline events', async () => {
    (apiKubernetes1.getTimelineData as jest.Mock).mockResolvedValue({
      data: { data: { event_get_timeline: { event_id: 'event-123', timeline: [] } } },
    });

    render(<DevOpsTimelineMUI eventId={mockEventId} />);

    await waitFor(() => {
      expect(screen.getByText('No timeline events found.')).toBeInTheDocument();
    });
  });
});
