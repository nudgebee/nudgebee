# Provider failover / resilience

Status: ✅ built (unary + streaming pre-header) · reuses the substitution translate path

When a request hits a **transient** provider error, the gateway transparently retries a
configured **fallback** model/provider instead of surfacing the error — so an org's
tools keep working through a provider blip or a rate-limit spike, with no client change.
A fallback attempt *is* a substitution, triggered by a primary failure rather than a rule.

## Trigger

Failover engages only when **all** of these hold:

- the primary attempt failed with a **transient** status — `429`, `500`, `502`, `503`,
  `504` (a `isRetryable` check). Client errors (4xx other than 429) return immediately —
  a fallback can't fix them;
- the matched routing rule configures `target.fallbacks`;
- for streaming, the primary failed **before any bytes were sent** (see the constraint).

## What happens

The **primary attempt stays byte-perfect passthrough** (fidelity on the happy path).
On a transient failure, the gateway walks `target.fallbacks` in order and, for each,
translates the original client request to that fallback target via the unified engine
(the same machinery as [substitution](substitution.md)) and re-encodes the response
back to the client's native shape.

- **Unary** tries each fallback in turn; the first success is returned.
- **Streaming** commits to the first usable fallback: once a stream opens it can't be
  peeled back to try another.

A fallback may be **same-provider** (another model) or **cross-provider** (translated).
Each fallback needs a concrete model — the requested model name won't exist on a
different provider, and retrying the same model is a no-op.

## The streaming constraint

A stream can only fail over **before the first byte reaches the client**. Once the
`200` SSE header is out, the status is committed and we can't retry — a mid-stream
provider failure ends the stream (as with substitution). In practice a provider `429`
is returned pre-stream, so the common rate-limit case does fail over.

## Signal + metering

- Response header `x-nb-llm-failover: <from>-><to>` on a served fallback.
- The served row is metered against the **fallback target** that ran (cost prices the
  executed model), with `reason=fallback`, the requested provider/model preserved, and
  `attributes.derived.failover = {from, to, primary_status}` recording the primary
  failure. So the added attempt is visible in usage; a failed fallback attempt writes
  and meters nothing (only the surfaced outcome does).

## Configuring

In Routing Rules, add one or more **Fallbacks** to a rule (each a provider + model).
They're tried in order on a transient failure. Same-provider fallbacks preserve
fidelity on retry; cross-provider fallbacks are translated (and carry the same
capability-drop caveats as substitution).
