# Implementation Plan - Client-Augmented Tool Support

## Phase 1: Server-Side Protocol Extension [checkpoint: eab5c78]
*Goal: Prepare the server to accept client-side tool definitions and results.*

- [x] Task: Update Conversation API Request (ba38321)
    - [x] Modify `ConversationApiRequest` struct in `api/chains.go` to include `ClientTools` (list of tool definitions).
    - [x] Update `POST /v1/completions/chat` handler in `api/chains.go` to extract `ClientTools` and pass them to `core.HandleConversationSessionRequest`.
- [x] Task: Update Core Conversation Logic (ba38321)
    - [x] Update `ConversationSessionRequest` options in `agents/core/conversation.go` (or similar) to accept `ClientTools`.
    - [x] Modify `HandleConversationSessionRequest` in `agents/core/conversation.go` to store these tools in the conversation context/DB.
- [x] Task: Implement Tool Result Submission API (6a18721)
    - [x] Create new struct `ClientToolResultApiRequest` in `api/chains.go`.
    - [x] Register new endpoint `POST /v1/completions/client-tool-result` in `api/chains.go`.
    - [x] Implement handler to update the agent's state with the tool output and resume execution.
- [x] Task: Update Agent State Machine (6a18721)
    - [x] Add `StatusWaitingForClientTool` constant in `agents/core/types.go` (or similar).
    - [x] Update `agents/core/executor.go` (ReAct/ReWOO) to handle the pause/resume logic for client tools.
- [x] Task: Conductor - User Manual Verification 'Server-Side Protocol Extension' (Protocol in workflow.md)

## Phase 2: Client-Side Implementation (`nbctl`) [checkpoint: delegated]
*Goal: Enhance nbctl to register and execute local tools. (Delegated to user)*

- [x] Task: Define Local Tools in `nbctl` (manual-edit)
    - [x] Implement `local_shell`, `local_read_file`, `local_write_file` using Go `os` and `exec` packages in `nbctl/pkg/nubi/tools`.
- [x] Task: Update `nubi` initialization (manual-edit)
    - [x] Update `TriggerInvestigation` in `nbctl/pkg/nubi/nubi.go` to include definitions of local tools in the request payload.
- [x] Task: Implement Tool Execution Loop in `nbctl` (manual-edit)
    - [x] Update `poll` function in `nbctl/cmd/nubi.go` to check for `WAITING_FOR_CLIENT_TOOL` status.
    - [x] Execute the requested tool locally.
    - [x] Call `ai_submit_client_tool_result` (via new client method) with the output.
- [x] Task: Conductor - User Manual Verification 'Client-Side Implementation (nbctl)' (Protocol in workflow.md)

## Phase 3: Planner Integration & Filtering [checkpoint: 75c0fb6]
*Goal: Teach the server's AI how to use client tools and allow filtering.*

- [x] Task: Update Planner Prompting (75c0fb6)
    - [x] Ensure `client_tools` are correctly formatted and injected into the LLM system prompt for both ReWOO and ReAct (likely in `agents/prompts_repo/`).
- [x] Task: Implement Agent/Tool Filtering (75c0fb6)
    - [x] Add `Capabilities` field to `ConversationApiRequest` in `api/chains.go`.
    - [x] Update `HandleConversationSessionRequest` to filter available tools/agents based on `Capabilities`.
- [x] Task: Conductor - User Manual Verification 'Planner Integration & Filtering' (Protocol in workflow.md)
