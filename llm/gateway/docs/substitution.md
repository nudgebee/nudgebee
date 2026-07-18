# Cross-provider substitution (P2b) — transparent translation

Status: ✅ built (all client×target pairs, unary + streaming) · 🟡 failover on fallbacks is a follow-on

Substitution lets a caller keep its **native SDK** unchanged while an admin rule runs
the request on a **different provider**, with the response translated **back** to the
client's native shape so the SDK never knows. The headline use case: route an
expensive model to a cheaper one org-wide with zero client change (e.g. Claude Code on
`ANTHROPIC_BASE_URL=<gateway>/anthropic` served by Gemini).

This differs from the generic `/v1` endpoint (see [generic-endpoint.md](generic-endpoint.md)):
there the client adopts OpenAI format and addresses a model by name; here the client
stays on its native mount and the swap is invisible to it.

## How a substitution is triggered

A routing rule whose target provider differs from the matched provider:

```
match:  { provider: anthropic, model: claude-opus-4-8 }
target: { provider: gemini,    model: gemini-3.1-flash }
```

The engine marks the decision `ReasonSubstitute` and sets `ResolvedProvider`. The
proxy then forks out of passthrough into the translate path (any other rule keeps the
faithful passthrough/model-rewrite behavior).

## Translation

The pivot is chosen by the **client's** native format:

| Client (mount) | Engine | Parser | Response re-encoder |
|----------------|--------|--------|---------------------|
| Anthropic (`/anthropic`) | Responses | `AnthropicMessageRequest → unified` | `unified → Anthropic Messages` (+ SSE events) |
| Gemini (`/genai`) | Responses | `GeminiGenerationRequest → unified` | `unified → generateContent` (+ SSE) |
| OpenAI (`/openai`) | Chat | `OpenAIChatRequest → unified` | `unified → chat.completion` (marshals directly) |

The **target** provider is set on the unified request; core's engine builds the
target-native call, invokes it, and returns a unified response. So the re-encoder
depends only on the client (what shape to return), not the target — 3 parsers + 3
re-encoders, not a 9-cell matrix. Streaming re-frames each unified chunk into the
client's native SSE (Anthropic named `event:` frames; Gemini/OpenAI `data:` frames;
OpenAI ends with `[DONE]`).

## Degradation: best-effort + signal

Substitution is opt-in per rule (an admin chose it), and it never fails silently on a
capability gap. When the request uses a feature the target can't honor — Anthropic
prompt caching (`cache_control`) or signed/redacted thinking blocks against a
non-Anthropic target — core's converter drops it building the target request, and the
gateway **signals** the loss:

- response header `x-nb-llm-substituted: <from>-><to>` on every substituted response;
- response header `x-nb-llm-degraded: <features>` when something was dropped;
- `attributes.derived.degraded` on the usage row.

The client SDK ignores these headers (a transparent swap is invisible to it by
design), but the operator sees them in logs/proxies and in the dashboard.

## Metering

The substituted request is metered against the **target** provider/model that
actually ran (so cost prices the executed model), while `requested_provider` /
`requested_model` and `attributes.derived.routing.reason = substitute` record what the
client asked for. `surface` stays `native` — the request arrived on a native mount;
"was it translated" is a separate, derivable fact.

## Not preserved across a substitution

Provider-specific features with no cross-provider equivalent: Anthropic prompt-cache
hit accounting, signed thinking-block verification on multi-turn tool use, and any
server-side tool types the target lacks. These are the reason substitution is
opt-in and surfaced with a fidelity warning in the Routing Rules admin UI.
