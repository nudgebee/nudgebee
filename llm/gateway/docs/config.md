# Configuration model & llm-server alignment

How the gateway resolves provider credentials, and how that stays consistent with
`llm/llm-server`. The two services deliberately share the **same config-key vocabulary**
and the **same per-tenant integration store**, so an operator or a tenant configures a
provider once and both consume it. This doc records what is shared, what differs, and
why — so the two don't drift.

## Two credential sources

1. **Operator default** — process env.
   - Per-provider api keys: `GATEWAY_ANTHROPIC_API_KEY`, `GATEWAY_OPENAI_API_KEY`,
     `GATEWAY_GEMINI_API_KEY`, `GATEWAY_HUGGINGFACE_API_KEY` (serve several at once).
   - The `LLM_PROVIDER_*` block (a single provider incl. structured cloud): `LLM_PROVIDER`,
     `LLM_PROVIDER_API_KEY`, `LLM_PROVIDER_API_ENDPOINT`, `LLM_PROVIDER_REGION`,
     `LLM_PROVIDER_ACCESS_KEY` / `_SECRET_KEY` / `_SESSION_TOKEN`.
2. **Per-tenant BYO** — the `integrations` (`type='llm'`) + `integration_config_values`
   rows, resolved per request (EE `secrets.Resolver`). **This is the same table
   llm-server reads** (`agents/core/llm_common.go`), written by the integration UI whose
   schema lives in `api-server/services/integrations/llm.go`. A tenant key wins per
   request; the operator default is the fallback.

Both sources funnel through one key-builder — `engine.buildKey` (operator via `buildCred`,
tenant via `BuildTenantKey`) — so a provider's credential is assembled identically
regardless of source. The **only** operator/tenant difference is `allowKeyless` (below).

## Shared config-key vocabulary

The gateway uses llm-server's exact `llm_provider_*` names for both the operator block and
the tenant integration values. The authoritative field set + provider enum is
`api-server/services/integrations/llm.go`. Keeping these names identical is what lets one
`llm` integration drive both services.

## Provider categories (how creds map)

| Category | Providers | Credential | Tenant BYO |
|---|---|---|---|
| **api-key (integration enum)** | anthropic, openai, gemini (`googleai`), huggingface | `llm_provider_api_key` → `Key.Value` | ✅ |
| **api-key (gateway-only aliases)** | groq, mistral, cohere, deepseek, xai, perplexity, openrouter, fireworks, cerebras, nebius, parasail | `llm_provider_api_key` → `Key.Value` | ❌ operator-only¹ |
| **OpenAI-compatible via endpoint** | `openai` + a custom base URL | `Key.Value` + `LLM_PROVIDER_API_ENDPOINT` → provider `BaseURL` | ❌ operator-only² |
| **structured cloud** | bedrock, azure | endpoint/region + keys carried **on the key** (`BedrockKeyConfig` / `AzureKeyConfig`) | ✅ |
| **self-hosted** | ollama, vllm, sgl | base URL (+ optional bearer) | ❌ operator-only² |

¹ The gateway *resolver* would build a tenant key for these, but the integration schema
(`api-server/services/integrations/llm.go`) restricts `llm_provider` to a fixed enum
(`anthropic, azure, bedrock, googleai, huggingface, openai, sagemaker, vertexai`) and
`ValidateConfig` **rejects** anything else — so a tenant can't save a `groq`/`deepseek`/…
integration today. They're operator-level (env) until the shared enum is widened (which
also requires llm-server to handle them, since the integration is shared — see below).

² Operator-only for a *technical* reason (a base URL can't ride a per-request `DirectKey`),
not just the enum. Even where the enum allows `openai`, a tenant's custom endpoint isn't
carried — see Known limitations.

**OpenAI-compatible = `openai` + endpoint** is the shared convention with llm-server
(`getOpenAILLM` sets `WithBaseURL`). Any OpenAI-compatible backend (Groq, Mistral, a
self-hosted vLLM, …) can be reached this way without a dedicated provider. The gateway
*also* exposes named aliases (`groq/…`, `mistral/…`) on the `/v1` endpoint as a
convenience, but the durable, cross-service pattern is `openai` + base URL.

## Keyless credential invariant (Bedrock IRSA / Azure managed identity)

Bedrock (AWS default chain / IRSA) and Azure (Azure default chain / managed identity) can
run **keyless** — but keyless resolves to the *pod's* cloud identity, which is the
operator's. So keyless is **operator-only**: `buildKey`'s `allowKeyless` is `true` on the
operator path and `false` on the tenant path. A tenant BYO for one of these **must** carry
static credentials; a keyless tenant config is rejected and falls back to the operator
default — it never borrows the pod identity. (Self-hosted is rejected on the tenant path
outright, for the same class of reason: a `DirectKey` can't carry a base URL.)

## Where the gateway and llm-server differ (and why)

| Aspect | llm-server | gateway | Why |
|---|---|---|---|
| `llm_provider_api_version` | Azure (deployment API) | **ignored** | Gateway uses Bifrost's Azure **v1 API** (`{endpoint}/openai/v1/...`), which is version-less. |
| `llm_provider_api_type` (`openai`/`azure`/`azure_ad`) | selects the OpenAI client mode | **ignored** | Provider is explicit in the gateway (`llm_provider=azure` vs `openai`), so the mode is already unambiguous. |
| `llm_provider_max_retries` | AWS/HTTP retry count | Bifrost defaults | No per-config knob today (cosmetic). |
| `llm_provider_embedding_model`, `thinking_*`, adapters | agent runtime | N/A | Gateway is a chat passthrough, not an agent runtime. |
| Config **resolution** | global → tier → agent × env/DB (per-account/agent/tier) | operator env + per-tenant DB | Gateway has no agent/tier grain; a flat operator + per-tenant model fits a passthrough. |

None of these are bugs — they're the gateway using a newer Bifrost surface (Azure v1) or
not needing an agent-runtime concept.

## Known limitations & direction

- **Tenant `openai` + custom endpoint is not carried.** Bifrost's OpenAI provider reads
  its base URL from the *provider-level* `NetworkConfig.BaseURL`, not per-key or
  per-request (unlike `AzureKeyConfig.Endpoint`, which is on the key). A per-request
  `DirectKey` therefore can't override it, so an OpenAI-compatible-via-endpoint provider
  is **operator-scoped** on the gateway. Same root cause as self-hosted being
  operator-only. Lifting this needs upstream Bifrost support (a per-key/context base URL).
- **Tenant BYO is gated by the integration schema enum.** `ValidateConfig` in
  `api-server/services/integrations/llm.go` accepts `llm_provider` only from a fixed set
  (`anthropic, azure, bedrock, googleai, huggingface, openai, sagemaker, vertexai`), so
  the gateway's api-key long-tail (groq/mistral/…) and self-hosted providers are **not
  tenant-configurable** even though the resolver could build keys for them. Widening the
  enum is a shared decision: the same `llm` integration drives llm-server, whose provider
  switch **errors** on anything it doesn't implement — so the enum only lists providers
  **both** services can serve. For now those extras are operator-level.
- **SageMaker** is in the integration enum (llm-server serves it) but **Bifrost has no
  SageMaker provider**, so the gateway can't. Don't offer it as a gateway BYO target.
- **Direction:** the gateway is the intended *superset* provider layer — llm-server will
  route OpenAI-compatible (and eventually more) providers *through* the gateway rather
  than re-implementing them. That's why the gateway carries more providers than
  llm-server's own switch, and why aligning the config vocabulary (above) matters.
