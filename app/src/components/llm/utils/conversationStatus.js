const ACTIVE_EXECUTION_STATUSES = new Set(['IN_PROGRESS', 'WAITING_FOR_CLIENT_TOOL']);

export const isConversationActivelyExecuting = (status) => ACTIVE_EXECUTION_STATUSES.has(status);

export const canStopConversation = ({ allowStop, conversationId, conversationStatus, currentlyProcessingQuestion }) =>
  Boolean(allowStop && conversationId && (currentlyProcessingQuestion || isConversationActivelyExecuting(conversationStatus)));
