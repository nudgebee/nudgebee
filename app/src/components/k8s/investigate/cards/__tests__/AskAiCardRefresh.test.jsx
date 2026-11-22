import { act, fireEvent, render, screen, within } from '@testing-library/react';
import apiKubernetes from '@api1/kubernetes';
import AskAiCard from '../AskAiCard';

jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: { generateAiRecommendation: jest.fn() } }));
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: { llmConversationHistoryForInvestigation: jest.fn().mockResolvedValue({ data: { data: { llm_conversations: [] } } }) },
  createConversationFetcher: () => ({ fetch: (...args) => mockConversationFetch(...args), reset: jest.fn() }),
}));
const mockConversationFetch = jest.fn().mockResolvedValue(null);
jest.mock('@components/llm/common/EvidenceDrawer', () => ({
  __esModule: true,
  default: ({ open }) => (open ? <div data-testid='evidence-drawer' /> : null),
}));
jest.mock('@hooks/useConversationSuggestions', () => ({
  useConversationSuggestions: () => ({ suggestions: [], fetchSuggestions: jest.fn(), clearSuggestions: jest.fn() }),
}));
jest.mock('@hooks/useTenantBranding', () => ({
  getNubiIconUrl: () => '',
  getAssistantLabel: () => 'Nubi',
  useTenantBranding: () => ({ assistantName: 'nubi' }),
}));
const mockLoopFlag = { on: false };
jest.mock('@lib/auth', () => ({ hasWriteAccess: () => true, hasFeatureAccess: () => Promise.resolve(mockLoopFlag.on) }));
jest.mock('@shared/viewers/MarkDowns', () => ({ __esModule: true, default: ({ data }) => <div data-testid='md'>{data}</div> }));

const previous = {
  id: 'v1',
  event_id: 'ev-1',
  generated_at: '2026-10-23T10:00:00Z',
  data: { status: 'completed', summary: 'Old summary', analysis: 'Old analysis', generated_at: '2026-10-23T10:00:00Z' },
};
const base = { event_id: 'ev-1', analysis_versions: [previous], message_id: 'msg-2', conversation_id: 'conv-2' };

beforeEach(() => {
  jest.useFakeTimers();
  apiKubernetes.generateAiRecommendation.mockReset();
});
afterEach(() => jest.useRealTimers());

describe('AskAiCard after Refresh investigation', () => {
  it('a refresh → progress → all stages complete → shows the new version without a reload', async () => {
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    // As refreshInvestigation leaves it: the new run, forced in progress.
    card.aiData = { ...base, status: 'in_progress', task_statuses: { investigation: 'IN_PROGRESS' } };
    apiKubernetes.generateAiRecommendation
      .mockResolvedValueOnce({ ...base, status: 'in_progress', task_statuses: { investigation: 'IN_PROGRESS' } })
      .mockResolvedValue({
        ...base,
        status: 'completed',
        summary: 'New summary',
        analysis: 'New analysis',
        generated_at: '2026-10-23T10:54:55Z',
        task_statuses: { investigation: 'COMPLETED', summary: 'COMPLETED' },
      });
    const Content = card.getContentComponents()[0];
    render(<Content />);
    expect(screen.getByText(/A new analysis is in progress/)).toBeInTheDocument();
    await act(async () => {
      jest.advanceTimersByTime(5000);
    });
    await act(async () => {
      jest.advanceTimersByTime(5000);
    });
    expect(screen.queryByText(/A new analysis is in progress/)).toBeNull();
    expect(card.isCompleted()).toBe(true);
  });

  it('with the remediation loop on, a run read as killed mid-way keeps being read, and shows the new version once it completes', async () => {
    mockLoopFlag.on = true;
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { ...base, status: 'in_progress' };
    apiKubernetes.generateAiRecommendation
      .mockResolvedValueOnce({ ...base, status: 'killed' })
      .mockResolvedValueOnce({ ...base, status: 'in_progress' })
      .mockResolvedValue({ ...base, status: 'completed', summary: 'New summary', analysis: 'New analysis', generated_at: '2026-10-23T10:54:55Z' });
    const Content = card.getContentComponents()[0];
    render(<Content />);
    for (let i = 0; i < 3; i += 1) {
      await act(async () => {
        jest.advanceTimersByTime(5000);
      });
    }
    expect(apiKubernetes.generateAiRecommendation).toHaveBeenCalledTimes(3);
    expect(screen.queryByText(/A new analysis is in progress/)).toBeNull();
    expect(card.isCompleted()).toBe(true);
    mockLoopFlag.on = false;
  });

  it('a run still killed after 30 minutes is no longer read, loop on or off', async () => {
    mockLoopFlag.on = true;
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { ...base, status: 'in_progress' };
    apiKubernetes.generateAiRecommendation.mockResolvedValue({ ...base, status: 'killed', task_statuses: { investigation: 'IN_PROGRESS' } });
    const Content = card.getContentComponents()[0];
    render(<Content />);
    for (let i = 0; i < 40; i += 1) {
      await act(async () => {
        jest.advanceTimersByTime(60 * 1000);
      });
    }
    const reads = apiKubernetes.generateAiRecommendation.mock.calls.length;
    await act(async () => {
      jest.advanceTimersByTime(10 * 60 * 1000);
    });
    expect(apiKubernetes.generateAiRecommendation).toHaveBeenCalledTimes(reads);
    mockLoopFlag.on = false;
  });

  it('with the remediation loop off, a killed run ends the poll, as before the loop', async () => {
    mockLoopFlag.on = false;
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { ...base, status: 'in_progress' };
    apiKubernetes.generateAiRecommendation.mockResolvedValue({ ...base, status: 'killed' });
    const Content = card.getContentComponents()[0];
    render(<Content />);
    for (let i = 0; i < 4; i += 1) {
      await act(async () => {
        jest.advanceTimersByTime(5000);
      });
    }
    expect(apiKubernetes.generateAiRecommendation).toHaveBeenCalledTimes(1);
  });

  it('with the remediation loop off, a killed read while a re-run is under way keeps being read', async () => {
    mockLoopFlag.on = false;
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { ...base, status: 'in_progress' };
    apiKubernetes.generateAiRecommendation
      .mockResolvedValueOnce({ ...base, status: 'killed', task_statuses: { investigation: 'IN_PROGRESS' } })
      .mockResolvedValue({ ...base, status: 'completed', summary: 'New summary', analysis: 'New analysis', generated_at: '2026-10-23T10:54:55Z' });
    const Content = card.getContentComponents()[0];
    render(<Content />);
    for (let i = 0; i < 3; i += 1) {
      await act(async () => {
        jest.advanceTimersByTime(5000);
      });
    }
    expect(apiKubernetes.generateAiRecommendation).toHaveBeenCalledTimes(2);
    expect(card.isCompleted()).toBe(true);
  });
});

describe('AskAiCard: a step named in the URL hash', () => {
  it('opens once per page visit, not again when the card mounts again', async () => {
    jest.useRealTimers();
    window.location.hash = '#task-E1';
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { ...base, status: 'completed', summary: 'Done', analysis: 'Done' };
    const Content = card.getContentComponents()[0];
    const first = render(<Content />);
    expect(await screen.findByTestId('evidence-drawer')).toBeInTheDocument();
    first.unmount();
    render(<Content />);
    await act(async () => {});
    expect(screen.queryByTestId('evidence-drawer')).toBeNull();
    window.location.hash = '';
  });
});

describe('AskAiCard: the report’s next steps point to the Remediation tab', () => {
  const report = 'Root cause text.\n\n### 🧭 Next Steps\n- Patch the image back.\n';
  const completedCard = () => {
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    card.aiData = { ...base, analysis_versions: [], status: 'completed', summary: report, generated_at: '2026-10-23T10:00:00Z' };
    return card;
  };

  it('puts one line just above the Next Steps heading, and it opens the Remediation tab', () => {
    jest.useRealTimers();
    const card = completedCard();
    const open = jest.fn();
    card.remediationLink = { count: 2, preparing: false, open };
    const Content = card.getContentComponents()[0];
    render(<Content />);
    const line = screen.getByTestId('investigation-remediation-link');
    expect(line).toHaveTextContent('Nubi made these runnable → Remediation (2 fixes ready)');
    const mds = screen.getAllByTestId('md');
    expect(mds[0]).toHaveTextContent('Root cause text.');
    expect(mds[1].textContent.startsWith('### 🧭 Next Steps')).toBe(true);
    expect(line.compareDocumentPosition(mds[1]) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(within(line).getByRole('button'));
    expect(open).toHaveBeenCalled();
  });

  it('says a fix is being prepared when the page reports it, and nothing without the loop', () => {
    jest.useRealTimers();
    const card = completedCard();
    const Content = card.getContentComponents()[0];
    render(<Content />);
    expect(screen.queryByTestId('investigation-remediation-link')).toBeNull();
    act(() => {
      window.dispatchEvent(
        new CustomEvent('nb:remediation-link', { detail: { eventId: 'ev-1', link: { count: 0, preparing: true, open: jest.fn() } } })
      );
    });
    expect(screen.getByTestId('investigation-remediation-link')).toHaveTextContent('Nubi is preparing a runnable fix → Remediation');
  });
  it('a run waiting on an approval says why and does not show a step as under way', async () => {
    const card = new AskAiCard();
    card.event = { id: 'ev-1', cloud_account_id: 'acc-1', fingerprint: 'fp1' };
    const waiting = { ...base, status: 'in_progress', status_reason: 'waiting on an approval', task_statuses: { investigation: 'IN_PROGRESS' } };
    card.aiData = waiting;
    apiKubernetes.generateAiRecommendation.mockResolvedValue(waiting);
    const Content = card.getContentComponents()[0];
    render(<Content />);
    expect(screen.getByText('The investigation is waiting')).toBeInTheDocument();
    expect(screen.getByText(/until the new analysis starts/)).toBeInTheDocument();
    expect(screen.queryByText(/A new analysis is in progress/)).toBeNull();
  });
});
