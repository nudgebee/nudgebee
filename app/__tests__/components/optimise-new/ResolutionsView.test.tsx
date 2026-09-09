import React from 'react';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import '@testing-library/jest-dom';
import ResolutionsView from '@components/optimise-new/ResolutionsView';

const mockGetResolutions = jest.fn();
const mockGetStatusCounts = jest.fn();
const mockGetDistinct = jest.fn();
const mockRetry = jest.fn();

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    getRecommendationResolution: (...args: any[]) => mockGetResolutions(...args),
    getRecommendationResolutionStatusCounts: (...args: any[]) => mockGetStatusCounts(...args),
    getDistinctResolverTypes: (...args: any[]) => mockGetDistinct(...args),
    retryRecommendationResolution: (...args: any[]) => mockRetry(...args),
  },
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: { getCloudAccounts: () => Promise.resolve([]) },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: () => 10 },
}));

jest.mock('next/router', () => ({
  __esModule: true,
  useRouter: () => ({ isReady: true, query: {}, pathname: '/optimise', push: jest.fn(), replace: jest.fn() }),
}));

jest.mock('@components/cloudaccount/CommandExecutionHistory', () => ({
  __esModule: true,
  default: () => <div data-testid='command-history' />,
}));

const lastPanelProps: Record<string, any> = {};
jest.mock('@components/optimise-new/ResolutionDetailPanel', () => ({
  __esModule: true,
  default: (props: any) => {
    Object.keys(lastPanelProps).forEach((key) => delete lastPanelProps[key]);
    Object.assign(lastPanelProps, props);
    return props.open ? <div data-testid='resolution-panel'>{props.resolution?.id}</div> : null;
  },
}));

// One listing row, in the shape the api layer hands back.
const ROW = {
  id: 'res-1',
  recommendation_id: 'rec-1',
  account_id: 'acct-a',
  status: 'Failed',
  status_message: 'Failed to execute code agent: llm: max retry attempts reached',
  resolver_type: 'AutoOptimize',
  type: 'PullRequest',
  type_reference_id: 'cli_execution',
  data: {},
  created_at: '2026-08-21T10:00:00Z',
  updated_at: '2026-08-21T10:15:00Z',
  recommendation: {
    recommendation: {},
    rule_name: 'pod_right_sizing',
    severity: 'Critical',
    estimated_savings: 16.2,
    status: 'InProgress',
    cloud_resourse: { name: 'workflow-server', meta: {} },
  },
};

const listingWithRow = {
  data: { data: { recommendation_resolution: [ROW], recommendation_resolution_aggregate: { aggregate: { count: 1 } } } },
};

const emptyListing = {
  data: { data: { recommendation_resolution: [], recommendation_resolution_aggregate: { aggregate: { count: 0 } } } },
};

describe('ResolutionsView status cards', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetResolutions.mockResolvedValue(emptyListing);
    mockGetDistinct.mockResolvedValue({ data: { data: { recommendation_resolution: [] } } });
    mockGetStatusCounts.mockResolvedValue({ Success: 1100, InProgress: 12, Failed: 7 });
    mockRetry.mockResolvedValue({ errors: [] });
  });

  const card = (testid: string) => screen.getByTestId(testid);

  it('surfaces the status split the paginated table cannot show', async () => {
    render(<ResolutionsView />);

    await waitFor(() => expect(within(card('resolutions-card-success')).getByText('1,100')).toBeInTheDocument());
    expect(within(card('resolutions-card-inprogress')).getByText('12')).toBeInTheDocument();
    expect(within(card('resolutions-card-failed')).getByText('7')).toBeInTheDocument();
  });

  it('totals every status the backend reports, not only the carded ones', async () => {
    mockGetStatusCounts.mockResolvedValue({ Success: 10, InProgress: 2, Failed: 1, Cancelled: 4 });
    render(<ResolutionsView />);

    // 17, not 13 — an unrecognised status still has to be counted somewhere.
    await waitFor(() => expect(within(card('resolutions-card-all')).getByText('17')).toBeInTheDocument());
  });

  it('filters the listing to a status when its card is clicked, and clears on a second click', async () => {
    render(<ResolutionsView />);
    await waitFor(() => expect(mockGetResolutions).toHaveBeenCalled());

    fireEvent.click(card('resolutions-card-failed'));
    await waitFor(() => expect(mockGetResolutions).toHaveBeenLastCalledWith(expect.objectContaining({ status: 'Failed' })));
    expect(card('resolutions-card-failed')).toHaveAttribute('aria-pressed', 'true');

    fireEvent.click(card('resolutions-card-failed'));
    await waitFor(() => expect(mockGetResolutions).toHaveBeenLastCalledWith(expect.objectContaining({ status: '' })));
    expect(card('resolutions-card-all')).toHaveAttribute('aria-pressed', 'true');
  });

  it('does not re-query the split when the status filter changes', async () => {
    render(<ResolutionsView />);
    await waitFor(() => expect(mockGetStatusCounts).toHaveBeenCalledTimes(1));

    fireEvent.click(card('resolutions-card-failed'));
    await waitFor(() => expect(mockGetResolutions).toHaveBeenLastCalledWith(expect.objectContaining({ status: 'Failed' })));

    // The cards describe every status, so the selected one is not part of their
    // scope — re-querying here would also collapse them to the filtered count.
    expect(mockGetStatusCounts).toHaveBeenCalledTimes(1);
  });

  it('asks for the split without a status, so the cards never narrow to their own selection', async () => {
    render(<ResolutionsView />);

    await waitFor(() => expect(mockGetStatusCounts).toHaveBeenCalled());
    expect(mockGetStatusCounts.mock.calls[0][0]).not.toHaveProperty('status');
  });

  it('mutes a status with nothing in it rather than offering an empty filter', async () => {
    mockGetStatusCounts.mockResolvedValue({ Success: 5, InProgress: 0, Failed: 0 });
    render(<ResolutionsView />);

    await waitFor(() => expect(card('resolutions-card-failed')).toHaveAttribute('aria-disabled', 'true'));
    fireEvent.click(card('resolutions-card-failed'));
    expect(card('resolutions-card-failed')).toHaveAttribute('aria-pressed', 'false');
  });

  it('does not tell an inert card it can be clicked', async () => {
    mockGetStatusCounts.mockResolvedValue({ Success: 5, InProgress: 3, Failed: 0 });
    render(<ResolutionsView />);

    await waitFor(() => expect(card('resolutions-card-failed')).toHaveAttribute('aria-disabled', 'true'));

    const mutedInfo = screen.getByLabelText('Info about Failed');
    fireEvent.mouseOver(mutedInfo);
    expect(await screen.findByRole('tooltip')).not.toHaveTextContent('Click to filter');

    // Closed before opening the next one — two live tooltips make getByRole ambiguous.
    fireEvent.mouseLeave(mutedInfo);
    await waitFor(() => expect(screen.queryByRole('tooltip')).not.toBeInTheDocument());

    // The contrast is the point: a card that does respond still says so.
    fireEvent.mouseOver(screen.getByLabelText('Info about In Progress'));
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Click to filter');
  });

  it('opens a resolution in a panel rather than expanding it in place', async () => {
    mockGetResolutions.mockResolvedValue(listingWithRow);
    render(<ResolutionsView />);

    await waitFor(() => expect(screen.getByText('Pod Right Sizing')).toBeInTheDocument());
    // A resolution is an individual record, so nothing is open until one is picked.
    expect(screen.queryByTestId('resolution-panel')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('Pod Right Sizing'));

    await waitFor(() => expect(screen.getByTestId('resolution-panel')).toBeInTheDocument());
    expect(lastPanelProps.resolution.id).toBe('res-1');
  });

  it('retries from the row action without also opening the panel', async () => {
    mockGetResolutions.mockResolvedValue(listingWithRow);
    render(<ResolutionsView />);

    const retry = await screen.findByRole('button', { name: 'Retry' });
    fireEvent.click(retry);

    // Retry is an action on the row, not a way into it.
    await waitFor(() => expect(mockRetry).toHaveBeenCalledWith('acct-a', 'res-1'));
    expect(screen.queryByTestId('resolution-panel')).not.toBeInTheDocument();
  });

  it('offers no retry on a resolution that did not fail', async () => {
    mockGetResolutions.mockResolvedValue({
      data: {
        data: { recommendation_resolution: [{ ...ROW, status: 'Success' }], recommendation_resolution_aggregate: { aggregate: { count: 1 } } },
      },
    });
    render(<ResolutionsView />);

    await waitFor(() => expect(screen.getByText('Pod Right Sizing')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  });

  it('keeps the status cell describing status, not carrying the action', async () => {
    mockGetResolutions.mockResolvedValue(listingWithRow);
    render(<ResolutionsView />);

    // The reason still reads inline; the button that acts on it does not.
    await waitFor(() => expect(screen.getByText(/Failed to execute code agent/)).toBeInTheDocument());
    const row = screen.getByText('Pod Right Sizing').closest('tr') as HTMLElement;
    const statusCell = within(row)
      .getByText(/Failed to execute code agent/)
      .closest('td') as HTMLElement;

    expect(within(statusCell).queryByRole('button')).toBeNull();
    expect(within(row).getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
  });

  it('renders the cards rather than failing the tab when the split cannot be loaded', async () => {
    mockGetStatusCounts.mockRejectedValue(new Error('boom'));
    render(<ResolutionsView />);

    await waitFor(() => expect(within(card('resolutions-card-all')).getByText('0')).toBeInTheDocument());
  });
});
