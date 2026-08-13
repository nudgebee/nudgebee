# Model-deprecation shield

Status: ✅ built · a routing-rule flavor (same-provider rewrite + signal)

When a provider retires a model, clients pinned to it break. A **deprecation shield**
is a routing rule that centrally **rewrites the retired model to a replacement** and
forwards the request, so callers keep working with a one-line admin change instead of
a client-side scramble.

## How it works

It's a routing rule whose target is marked `deprecated`, with the replacement in the
target model:

```
match:  { provider: anthropic, model: claude-2 }
target: { model: claude-haiku-4-5, deprecated: true }
```

- **Same-provider rewrite.** The route stage rewrites the model to the replacement and
  the request passes through to the same provider — no translation (unlike
  cross-provider substitution). The replacement model is **required** (there's nothing
  to rewrite to otherwise; the backend and UI both enforce it).
- **Distinct, observable.** The decision is `reason=deprecated` (not `alias`), so usage
  can be grouped to answer "how much traffic still hits retired models" — the signal to
  decide when to remove the shield.
- **Signalled.** A served deprecation carries an `x-nb-llm-deprecated: <old>-><new>`
  response header, so clients/operators can see the rewrite happened.

Applies on the native mounts and the generic `/v1` endpoint alike.

## Configuring

In Routing Rules, add a rule matching the retired model, toggle **Deprecation shield**,
and set the replacement model. It shows in the rules table as `Deprecated · → <model>`.

## vs. the other target modes
- **Alias/tier** — a normal same-provider rewrite; not marked, no signal.
- **Substitution** — rewrites to a *different provider* and translates the response
  (see [substitution.md](substitution.md)).
- **Block** — rejects the request (403) instead of rewriting.
- **Deprecation** — a same-provider rewrite that keeps clients working through a
  provider's model retirement, marked + signalled.
