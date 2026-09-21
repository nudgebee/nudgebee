# Nubi self-awareness: read-only foundation

## Goal

Let an authenticated user ask Nubi about Nudgebee product concepts and the
accounts/integrations that the user is authorized to see, without exposing the
broad `nbctl` command surface or creating a user API token inside llm-server.

## Scope

- Canonical `nudgebee` agent with `nubi` as an alias.
- Existing indexed documentation search under the explicit
  `nudgebee_docs_search` tool name.
- Explicit read-only account and integration list/count/get-status tools.
- Internal `/rpc/query` calls that forward the requesting tenant and user; the
  API server rebuilds its SecurityContext and remains the authorization/data
  scoping authority.

Writes, arbitrary public API calls, generic SQL, shelling out to `nbctl`, and
automatic MCP command exposure are out of scope.

## Security invariants

1. A live-data or docs tool refuses to run without a real requesting user.
   This prevents the internal tenant-only identity shape from taking API
   server's tenant-admin branch.
2. Tenant ID and user ID come only from the request SecurityContext, never tool
   input.
3. Live tools request a curated non-secret column projection.
4. API-server query metadata and RBAC enforce account/tenant visibility again.
5. Every exposed tool reports `ToolRequestTypeRead` and the agent holds no
   mutation, shell, SQL, public-API, or generic RPC tool.
6. Documentation is evidence for product behavior only; current counts and
   status require a live-data tool.

## Acceptance criteria

- `@nudgebee` and `@nubi` resolve to the same canonical agent.
- “What is an account?” can use indexed Nudgebee documentation.
- “How many accounts do I have?” calls `nudgebee_accounts_count`.
- “How many integrations are configured?” calls
  `nudgebee_integrations_count`.
- “Is Datadog active?” calls `nudgebee_integration_get_status`.
- A single live-state question uses one filtered purpose-built tool call; count
  questions do not list resources or request a grouped breakdown first.
- A documentation-only question calls `nudgebee_docs_search`.
- A mixed documentation/live-state question uses only the two corresponding
  tools and combines their evidence.
- Authorization failures stop the tool loop rather than triggering alternate
  or broader calls.
- Default router E2E coverage sends product docs, account inventory, and
  integration-status questions to the canonical `nudgebee` agent.
- Missing user identity fails before an HTTP request is made.
- Requests forward `x-tenant-id`, `x-user-id`, and the service action token.
- Focused unit tests pass, followed by `make validate` in `llm/llm-server`.

## Adversarial verdict

**PROCEED.** The implementation rejects three structural failure modes: broad
command-surface growth (fixed allowlist), silent privilege escalation (real
user required), and stale/docs-derived live answers (prompt and tool boundary).
Reconsider the internal RPC transport if Nudgebee introduces a safe,
short-lived on-behalf-of public API token that preserves the same user identity
and audit trail.
