# Generic endpoint (`/v1`) — OpenAI-compatible

Status: ✅ built + verified (chat completions, unary + streaming) · 🟡 `/v1/responses` future

Alongside the provider-native mounts (`/anthropic`, `/openai`, `/genai`), the gateway
exposes **one provider-agnostic endpoint** any OpenAI-compatible tool can point at:

```
OPENAI_BASE_URL = <gateway>/v1
Authorization: Bearer <NB token>
```

The caller addresses a **model by name** and the gateway routes it to the right
provider, returning a canonical OpenAI-shaped response. This is the first slice of
the P2 translation layer; transparent cross-provider substitution on the *native*
mounts (Phase 2b) reuses the same engine.

## Addressing a model

Two forms are accepted for the `model` field:

- **Explicit** `provider/model` — unambiguous, recommended:
  `anthropic/claude-opus-4-8`, `openai/gpt-5`, `gemini/gemini-3.1-flash`
  (`google/…` is accepted as an alias for `gemini/…`).
- **Bare** well-known name — resolved by prefix: `claude*` → Anthropic,
  `gpt*` / `o1|o3|o4*` / `chatgpt*` → OpenAI, `gemini*` → Gemini.

A name that matches neither is a clear `400 invalid_request_error`, never a guessed
provider.

`GET /v1/models` returns an advisory list of current-generation models (in
`provider/model` form) for tool model-pickers. It is **not** a whitelist — any valid
`provider/model` (or well-known bare name) works whether listed or not.

## Request lifecycle

The generic endpoint runs the **same** spine as the native mounts:

```
auth → route → ratelimit → resolver → filter → dispatch → meter
```

The only differences from passthrough:

1. **Provider is derived from the model name** (native mounts derive it from the URL
   prefix). The resolved provider/model feed the routing rules, so aliases/tiers and
   block rules apply exactly as on the native mounts.
2. **Dispatch is through the unified engine** (`ChatCompletionRequest` /
   `ChatCompletionStreamRequest`), which builds the target-native call, invokes it,
   and returns a unified `BifrostChatResponse`. This is what lets one endpoint reach
   any provider.
3. **The response is marshalled in canonical OpenAI shape.** Bifrost's internal
   `extra_fields` annotation is stripped — it carries operator provider org/project
   ids and raw provider headers that must not leak to the client, and a real OpenAI
   response never contains it.

Streaming re-frames each unified chunk as an OpenAI `chat.completion.chunk` SSE event
(`data: {json}\n\n`), ending with `data: [DONE]`. Usage arrives on the final chunk
(`include_usage` is on by default).

## Metering

One usage row per request, same sink and cost snapshot as the native mounts. Two
things worth noting:

- **`surface` attribute** — every row records `attributes.derived.surface` =
  `native` | `generic`, so traffic can be grouped by how it arrived (independent of
  whether a routing rule then translated it).
- **Cost keys off the requested model**, not the (possibly dated) response model —
  e.g. a request for `gpt-4o-mini` served as `gpt-4o-mini-2024-07-18` prices against
  `gpt-4o-mini`. The pricer also normalizes dated/versioned ids (both Anthropic's
  `-YYYYMMDD` and OpenAI's `-YYYY-MM-DD` forms) as a fallback.

## Not in scope here

- **Cross-provider substitution on native mounts** (Phase 2b, now built — see
  [substitution.md](substitution.md)) — keeping a native SDK but running the request
  on a different provider and translating the response *back* to the client's native
  shape. The generic endpoint sidesteps that re-encoding by always emitting the
  canonical shape.
- **`/v1/responses`** (OpenAI Responses API surface) — future.
