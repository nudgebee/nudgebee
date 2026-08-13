# Routing Gemini traffic through the NB AI Gateway

> **Status:** implemented for the `googleai` (Gemini-API) provider. Tracking issue: #34144 (parent epic #33932).
> **Audience:** llm-server contributors + gateway/ops.

llm-server can send its own Google Gemini calls through the NB AI Gateway's
`/genai` mount instead of calling Google directly, so NB's own LLM usage shows
up in the same cost-attribution, metering, routing, and rate-limit surfaces as
external gateway traffic (the "dogfood" step). Inference results are unchanged —
this is a transport change only.

Applies to the **Gemini-API (API-key)** path only. The Vertex (OAuth/ADC) path
does not fit the `/genai` mount and is out of scope.

## How it's wired

`WithBaseURL` on the googleai client sets `genai.ClientConfig.HTTPOptions.BaseURL`.
The base URL is resolved from the existing endpoint layering (`getLLMApiEndpoint`),
the same resolver Azure already uses, and is plumbed into **both** the generation
client (`getGoogleAILLM`) and every caching-helper client (`llm_cache.go`, via
`CacheRequest.Endpoint`). The NB token goes in the API-key slot; the gateway
swaps in the real Google key.

## Configuration contract

To route an account (or the whole deployment) through the gateway, set, for the
`googleai` provider:

| Setting | Value |
|---|---|
| provider | `googleai` |
| endpoint (`LLM_PROVIDER_API_ENDPOINT` / `llm_provider_api_endpoint`) | the gateway's `/genai` mount root, e.g. `https://<gateway-host>/genai` — **no** trailing `/v1beta` (the SDK appends `/v1beta/...`) |
| token (`LLM_PROVIDER_API_KEY` / `llm_provider_api_key`) | the NB gateway token (not a raw Google key) |

Both env and DB config feed the same layering (`ENV-global → ENV-tier → ENV-agent
→ DB-global → DB-tier → DB-agent`, DB wins). DB config is account-scoped, so it
doubles as a per-tenant canary: one account on the gateway while others stay
direct. Cache slots and cached clients are isolated per account+credential
(the fingerprint folds in endpoint + key + accountId), so there is no
cross-account collision.

### Two rules that matter

1. **Endpoint presence is the on-switch.** Empty endpoint = talk to Google
   directly (the default, byte-identical to pre-gateway behavior). A non-empty
   endpoint for the `googleai` provider routes through the gateway. Nothing is
   activated implicitly — in Helm it only reaches llm-server if set in the
   deployment's `nudgebee_secret` / `additional_env_vars`, or in an account's DB
   integration config.

2. **Set endpoint and token together, at the same scope.** The endpoint and the
   token are resolved by two independent ladder-walks. A *partial* config —
   e.g. a DB endpoint with the token still falling back to an ENV Google key —
   sends a mismatched credential to the gateway and **fails hard** (auth error /
   401) for that scope. It fails visibly, not silently, but all `googleai` calls
   for that scope break until the pair is corrected. The in-app "test connection"
   flow validates the (endpoint, token) pair from the same config row, so use it
   after configuring.

## Caching notes

llm-server uses explicit Gemini `CachedContent` caching (see [caching.md](caching.md)).
Gemini caches are isolated by credential/project, so **create and reference must
resolve to the same Google key through the gateway.** That holds because the
generation and caching paths resolve endpoint + token through the same layering.
Operationally this requires the gateway to map the NB token to a **stable** Google
key (a single operator key, or a pinned per-tenant key); a pooled/rotating key
would miss/403 on cache reference.

On a direct→gateway cutover the credential changes, so existing caches created
under the direct key are orphaned (cold start, then warm) and pay storage until
their TTL expires — transient, self-healing.

## Known follow-up

The gateway returns a real HTTP `429` on rate-limit, but its error body omits the
`code`/`status` fields, so the `google.golang.org/genai` SDK surfaces it as
`Error 0` and llm-server's `isQuotaError` classifier misses it — the call then
hard-fails instead of backing off / falling back. Fix belongs with the gateway's
error-shape hardening (#34107 / #34108): return Google-native error bodies
(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED",...}}`). A defensive
one-line match in `isQuotaError` is a possible safety net. Size the gateway's
rate limits with the cache path in mind — each agent turn adds `countTokens` +
`caches.create`/`get` round-trips.
