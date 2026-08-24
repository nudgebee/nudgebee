// Second miscount surface (review): investigate tab label must match the chat chip.

import { render, screen } from '@testing-library/react';
import LLMConversationWithTabs from '@components/llm/LLMConversationWithTabs';

jest.mock('@components/llm/KubernetesLLMRequestResponseV2', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/common/ConversationCollapsableCard', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/llm/common/ReferencesDrawerContent', () => ({ __esModule: true, default: () => null }));
jest.mock('@api1/ask-nudgebee', () => ({ __esModule: true, default: { getMessageReferences: jest.fn(), getMessageMemories: jest.fn() } }));

const baseProps = { accountId: 'acc-1', conversationId: 'conv-1', getCardTitle: () => 'title', collapsedObj: {} };
const tabLabel = () => screen.getByText(/^Tasks \(/).textContent;

describe('LLMConversationWithTabs task count', () => {
  it('counts an acknowledgment + one agent execution as 1', () => {
    const messages = [
      { type: 'question', text: 'Review this deployment config' },
      { type: 'acknowledgment', text: "I understand you're looking for a manual review" },
      { type: 'tool_call', tool: 'k8s_orchestrator', id: 'agent-1' },
      { type: 'response', id: 'msg-1', text: 'Configuration Review' },
    ];
    render(<LLMConversationWithTabs messages={messages} {...baseProps} />);
    expect(tabLabel()).toBe('Tasks (1)');
  });

  it('drops only the acknowledgment -> follow-up cards still count', () => {
    const messages = [
      { type: 'question', text: 'q' },
      { type: 'acknowledgment', text: 'ack' },
      { type: 'followup-question', tool: 'followup-question', text: 'Which namespace?' },
      { type: 'tool_call', tool: 'k8s_orchestrator', id: 'agent-1' },
      { type: 'tool_call', tool: 'kubectl_execute', id: 'tool-1' },
      { type: 'response', id: 'msg-1' },
    ];
    render(<LLMConversationWithTabs messages={messages} {...baseProps} />);
    expect(tabLabel()).toBe('Tasks (3)');
  });

  it('shows 0 for a turn that only acknowledged', () => {
    const messages = [
      { type: 'question', text: 'q' },
      { type: 'acknowledgment', text: 'ack' },
      { type: 'response', id: 'msg-1' },
    ];
    render(<LLMConversationWithTabs messages={messages} {...baseProps} />);
    expect(tabLabel()).toBe('Tasks (0)');
  });
});
