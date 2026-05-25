import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import DevOpsTimelineMUI from '@components1/common/DevOpsTimelineMUI';
import apiKubernetes1 from '@api1/kubernetes1';

// Mock dependencies
jest.mock('@api1/kubernetes1', () => ({
  getTimelineData: jest.fn(),
}));

jest.mock(
  '@assets',
  () => ({
    ExternalLinkIcon: 'mock-external-link-icon',
  }),
  { virtual: true }
);

jest.mock('next/image', () => ({
  __esModule: true,
  default: (props: React.ImgHTMLAttributes<HTMLImageElement>) => <img {...props} alt={props.alt} />,
}));

// Mock child components to avoid deep rendering issues
jest.mock('@components1/common/WidgetCard', () => ({ children }: { children: React.ReactNode }) => <div data-testid='widget-card'>{children}</div>);
jest.mock('@components1/common/CopyableText', () => ({ children }: { children: React.ReactNode }) => (
  <div data-testid='copyable-text'>{children}</div>
));
jest.mock('@components1/common/Loader', () => () => <div data-testid='loader'>Loading...</div>);

// Mock snackbar
jest.mock('@components1/common/snackbarService', () => ({
  snackbar: {
    error: jest.fn(),
  },
}));

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

    // Wait for data to load
    await waitFor(() => {
      expect(screen.getByText('Alert fired')).toBeInTheDocument();
    });

    // Check if event summary is present
    // Check if workload summary is present
    expect(screen.getByText('Workload created')).toBeInTheDocument();
    // Check if event ID is displayed
    expect(screen.getByText('event-123')).toBeInTheDocument();
  });
});
