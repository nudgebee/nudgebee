import { renderHook, waitFor } from '@testing-library/react';
import useMessageAdditionalData from '@hooks/useMessageAdditionalData';

jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listReferences: jest.fn(),
    listMemory: jest.fn(),
  },
}));

import apiAskNudgebee from '@api1/ask-nudgebee';
const mockListReferences = apiAskNudgebee.listReferences;
const mockListMemory = apiAskNudgebee.listMemory;

const makeGroup = (id, status = 'COMPLETED', tool = 'response') => ({
  children: [{ id, type: tool, tool, status }],
});

describe('useMessageAdditionalData', () => {
  beforeEach(() => jest.clearAllMocks());

  it('returns empty object initially', () => {
    const { result } = renderHook(() => useMessageAdditionalData([], 'acc-1', 'conv-1'));
    expect(result.current).toEqual({});
  });

  it('does not fetch for non-response messages', async () => {
    const groups = [makeGroup('msg-1', 'COMPLETED', 'question')];
    renderHook(() => useMessageAdditionalData(groups, 'acc-1', 'conv-1'));
    expect(mockListReferences).not.toHaveBeenCalled();
  });

  it('does not fetch for incomplete (non-COMPLETED) response messages', async () => {
    const groups = [makeGroup('msg-1', 'IN_PROGRESS', 'response')];
    renderHook(() => useMessageAdditionalData(groups, 'acc-1', 'conv-1'));
    expect(mockListReferences).not.toHaveBeenCalled();
  });

  it('fetches references and memories for completed response messages', async () => {
    mockListReferences.mockResolvedValue({ data: [{ url: 'http://example.com' }] });
    mockListMemory.mockResolvedValue({ data: [{ content: 'Memory 1' }] });
    const groups = [makeGroup('msg-1', 'COMPLETED', 'response')];

    const { result } = renderHook(() => useMessageAdditionalData(groups, 'acc-1', 'conv-1'));

    await waitFor(() => expect(result.current['msg-1']).toBeDefined());
    expect(result.current['msg-1'].references).toEqual([{ url: 'http://example.com' }]);
    expect(result.current['msg-1'].memories).toEqual([{ content: 'Memory 1' }]);
  });

  it('calls listReferences with correct parameters', async () => {
    mockListReferences.mockResolvedValue({ data: [] });
    mockListMemory.mockResolvedValue({ data: [] });
    const groups = [makeGroup('msg-42', 'COMPLETED', 'response')];

    renderHook(() => useMessageAdditionalData(groups, 'acc-1', 'conv-99'));

    await waitFor(() => expect(mockListReferences).toHaveBeenCalled());
    expect(mockListReferences).toHaveBeenCalledWith({
      accountId: 'acc-1',
      messageId: 'msg-42',
      conversationId: 'conv-99',
    });
  });

  it('does not fetch the same message id twice', async () => {
    mockListReferences.mockResolvedValue({ data: [] });
    mockListMemory.mockResolvedValue({ data: [] });
    const groups = [makeGroup('msg-1', 'COMPLETED', 'response')];

    const { rerender } = renderHook(({ g }) => useMessageAdditionalData(g, 'acc-1', 'conv-1'), { initialProps: { g: groups } });
    await waitFor(() => expect(mockListReferences).toHaveBeenCalledTimes(1));

    // Re-render with same groups — should not re-fetch
    rerender({ g: [...groups] });
    expect(mockListReferences).toHaveBeenCalledTimes(1);
  });

  it('resets additional data when conversationId changes', async () => {
    mockListReferences.mockResolvedValue({ data: [{ url: 'https://ref.com' }] });
    mockListMemory.mockResolvedValue({ data: [] });
    const groups = [makeGroup('msg-1', 'COMPLETED', 'response')];

    const { result, rerender } = renderHook(({ convId }) => useMessageAdditionalData(groups, 'acc-1', convId), {
      initialProps: { convId: 'conv-1' },
    });
    await waitFor(() => expect(result.current['msg-1']).toBeDefined());

    rerender({ convId: 'conv-2' });
    expect(result.current).toEqual({});
  });

  it('also handles SUCCESS status as completed', async () => {
    mockListReferences.mockResolvedValue({ data: [] });
    mockListMemory.mockResolvedValue({ data: [] });
    const groups = [makeGroup('msg-success', 'SUCCESS', 'response')];

    renderHook(() => useMessageAdditionalData(groups, 'acc-1', 'conv-1'));
    await waitFor(() => expect(mockListReferences).toHaveBeenCalled());
  });
});
