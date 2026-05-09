# Specification: Client-Augmented Tool Support (`nbctl` Integration)

## 1. Overview
Instead of a standalone local CLI, this track focuses on extending the existing `llm-server` to support "Client-Side Tools". The server acts as the brain (Planner), but can delegate execution of specific tools to the connected client (`nbctl`). This allows agents to interact with the user's local system (files, shell) securely and natively.

## 2. Functional Requirements

### 2.1 Tool Registration (Client to Server)
*   **Dynamic Tool Injection:** When `nbctl` starts a `nubi` session, it will send a list of "Client Tools" (name, description, JSON schema) to the server.
*   **Persistence:** These tools are associated with the specific `SessionID` or `ConversationID`.

### 2.2 Bi-directional Tool Execution Flow
*   **Execution Request:** When the Planner chooses a client tool, the server marks the conversation/agent status as `WAITING_FOR_CLIENT_TOOL` and includes the tool name and parameters in the response.
*   **Client Execution:** The client (`nbctl`), upon polling and seeing the `WAITING_FOR_CLIENT_TOOL` status, executes the tool locally.
*   **Result Submission:** The client posts the execution result (stdout/stderr/data) back to the server via a new endpoint.
*   **Resumption:** The server receives the result and triggers the next step in the agent's plan.

### 2.3 Selective Agent/Tool Filtering
*   **Capabilities Negotiating:** Clients can send a list of `enabled_agents` or `disabled_tools` to the server to customize the agent's behavior for that session.
*   **Contextual Defaulting:** The server can provide "Agent Profiles" (e.g., `profile=cli`) that automatically enable/disable certain toolsets.

### 2.4 New Client-Side Tools (implemented in `nbctl`)
*   `shell_execute`: Run local commands.
*   `file_read` / `file_write`: Local filesystem access.
*   `mcp_bridge`: Support for any local MCP server tool.

## 3. Architecture & Design

### 3.1 Server-Side Changes (`llm-server`)
*   **API:**
    *   Update `TriggerInvestigation` (mutation) to accept `client_tools` (list of tool definitions).
    *   New mutation: `ai_submit_client_tool_result(conversation_id, agent_id, result_json)`.
*   **Core:**
    *   Planner must merge `client_tools` into the prompt.
    *   New Agent status: `StatusWaitingForClientTool`.
*   **Persistence:** Store client tool definitions in the DB (linked to conversation/message).

### 3.2 Client-Side Changes (`nbctl`)
*   **Nubi Shell:**
    *   On startup, define local tools (using MCP schemas).
    *   Send these tools in the initial investigation trigger.
    *   In the `poll` loop, check for tool requests and execute them.

## 4. Acceptance Criteria
*   [ ] `nbctl` can register a "local_shell" tool with the server.
*   [ ] Server Planner uses "local_shell" to solve a user query (e.g., "what files are in my current dir?").
*   [ ] `nbctl` executes the shell command locally and sends result back.
*   [ ] Agent completes the task and provides the final answer based on local output.
*   [ ] Client can disable server-side "cloud" tools for a "local-only" session.