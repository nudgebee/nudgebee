# Classification API

Tracking: #39381. Evaluation of alternative models: #39885.

## Problem

llm-server makes many small fixed-choice decisions: whether the automation builder should answer, edit or create; whether a message is an investigation; which configuration or repository applies. Each one has its own prompt, model choice and answer parsing. Trying a different model, or a decision model with its own API (TypeSafe Jev, Cloudflare Clef, OpenAI Decisions), means rewriting every place that makes a decision.

## Design

### One entry point

A caller describes the decision and gets an answer back:

- **The decision:** a stable name, instructions, and an ordered list of options. Each option has a label and, optionally, a description of when it applies. A decision allows one option, or several.
- **The answer:** the chosen label or labels, whether the answer is usable, the raw reply and which backend answered.

The API never applies a fallback. When the model's answer is not one of the options, it reports the answer as unusable; when the backend fails, it returns an error. Each caller keeps its own policy for both. This matters because callers fail differently: some fall back to the safest action, some skip, and some have an "uncertain" option of their own.

### Backends

The API sits in front of interchangeable backends, so a decision can move to another model or provider without changing its caller.

- **LLM (default):** sends the instructions and options as a strict "answer with only the option" prompt on a low-thinking, temperature-0 call. It accepts only an exact option (case-insensitive) and retries up to twice, telling the model its last answer was invalid.
- **Decision models (beta):** hosted models built for this job, which return a choice with probabilities and a confidence score: Cloudflare's Clef on Workers AI, TypeSafe's Jev and OpenAI Decisions. A configured decision model is used by each decision whose server mode is `on` or `shadow` (see below); an answer below the decision's minimum confidence, a provider error or a timeout goes to the LLM instead. Multi-option decisions always use the LLM, because no decision-model API supports them.

### Decision models

- **Where they are configured.** A decision model is set in the existing LLM configuration, in its own "decision" slot next to the model tiers. The slot is what makes it decision-only: it is never offered for chat, a model tier, an agent or a fallback, and it cannot be pinned. The provider keeps its vendor name (`cloudflare`, `typesafe`, `openai`). Cloudflare and TypeSafe sell only decision models, so they are also refused in every chat setting. OpenAI's decision model (`gpt-6-luna`) is also one of its chat models, so for OpenAI the slot alone keeps the two apart.
- **Settings.** Provider, model, credentials and a minimum confidence (default 0.9). Once a decision model is set, every decision that may use one does. With no decision model, every decision uses the LLM exactly as before.
- **Who can turn them on.** Whoever configures the LLM: a tenant admin in the decision-model section of their LLM settings, or the operator in server settings (ENV). Without a configured decision model, every decision stays on the LLM. Because the approval check is `on` by default, configuring one means a confident "read" runs that command without asking the user; the settings section says so.
- **Which configuration applies.** The same pin rules as the LLM call the decision would fall back to: a per-task pick for that task's model, then a request or conversation pin. A pinned call uses that integration's decision settings only, so its text never reaches a decision model configured elsewhere. Otherwise the account's LLM integration applies, or the server settings on installs without one.
- **Which decisions use one, and how.** Each listed decision has a mode: `on` (the decision model decides when confident, else the LLM), `shadow` (the LLM decides; the decision model is asked afterwards in the background and only logged, adding no latency) or `off`. Defaults: both the automation builder's answer/edit/create decision and the tool approval check are `on`. For the approval check, a confident "read" from the decision model runs the command without approval; set it to `shadow` (`LLM_DECISION_MODE_TOOL_REQUEST_TYPE=shadow`) wherever that is not wanted until hand-labelled data, including injection and hidden-write cases, shows no more false "read" than the LLM (#40902). Decision models are only used where one is configured.
- **Data.** The decision input passes through the same egress filter and PII scrubbing as an LLM call before it is sent. Check the provider's data-retention terms before setting a decision model.
- **Tuning.** A minimum confidence is tuned against the decision's current prompt; after a prompt change, replay the decision again and re-check the threshold.

### The classification tier

`classification` is a model tier like reasoning, retrieval and summary. It gives one setting that moves every classification decision to another model.

- The API uses it unless the caller asks for a specific tier.
- **When classification is not defined, it is the summary tier.** "Defined" is decided by the normal configuration resolver for the call's account: the classification tier is used only when the model would come from configuration that names this tier, at server, account, request or conversation level. Otherwise the summary tier is used. Checking server settings alone is not enough, because an account's own configuration takes precedence over server settings. A classification model set only on the server would otherwise be ignored for that account, and the call would land on the account's global model, which is usually larger than its summary model.
- A configured classification model appears in the model picker like the other tiers. The LLM settings screen does not offer the tier yet, so for now it is set through server or account configuration.

### Mapping to decision-model APIs

The contract is neutral; each decision-model backend translates it.

| Contract | TypeSafe Jev / Cloudflare Clef | OpenAI Decisions |
| --- | --- | --- |
| input text | `state` | `input` |
| instructions | question `instructions` | question `instructions` |
| ordered options with descriptions | `criteria` map, label → description | `choices` array of `{value, description}`, order kept |
| one choice question | `questions` map keyed by id | `questions` array with a `name` |
| answer, probabilities, confidence | `answers[id]` with a `probabilities` map | `answers[]` matched by `name`, with a `probabilities` array |
| unusable answer | label not among the options | label not among the options, or a `refusal` answer |

Each decision should record the model version the provider reports (for example `jev-1.13.0`), because confidence thresholds are tuned per version.

### How it is used

```
callers (workflow intent, productivity, … more later)
        │  describe the decision, call the API, keep their own fallback
        ▼
classification API — validation, retries, backend choice, telemetry
        ▼
backends:  LLM (default)  │  decision models
```

- The API changes rarely; callers depend only on it.
- Backends change when a model or vendor does, without touching callers.
- Callers move onto the API one at a time, each checked by replaying real messages, with the LLM backend as the default so behaviour changes only on purpose.
- Moving a decision to another backend is a configuration change: replay it offline against the candidate, enable it in one environment, watch the decision logs and metrics, then keep or revert.

### Telemetry

Every decision is logged as `classification_decision` (task, labels, valid, backend, latency, any error) and counted in metrics. LLM calls also keep their normal token-usage record. The token-usage `task_type` column is not reused, because it already holds the per-turn query/investigation classification.

## Implementation notes

- **API:** `classify.Classify(ctx, ClassificationCall, ClassificationTask, input) → ClassificationDecision` in `llms/classify/classify.go`.
  - `ClassificationTask` has `Name`, `Instructions`, `Options []ClassificationOption{Label, Description}` and `Multi`.
  - `ClassificationDecision` has `Labels`, `Valid`, `Raw`, `Backend` and `Latency`. Decision models add `Confidence`, `Probabilities`, `Model` and `FellBack`.
  - `ClassificationCall` carries the ids that `GenerateAndTrackLLMContent` needs. `AgentId` is the calling agent's id, because it drives API-key resolution and cost attribution.
  - `Tier` picks the model tier; empty means the classification tier. `KeepCallerTier` instead runs on whatever tier the caller's context has, including none, for callers whose model must not change when they move onto the API (classification-type agents, which keep their own tier). The tool-approval check passes the calling agent's tier, so only a caller with no tier lands on the classification tier.
- **LLM backend:** `ClassifyWithLLM` calls `GenerateAndTrackLLMContent` with `llms.WithTemperature(0)` and `WithThinkingLevel(ThinkingLevelFastTask)`.
  - It does not use structured enum output, because our provider adapters have no common option for it.
  - Production Gemini returned an exact label for 211 of 211 real workflow-intent messages.
- **Tier:** `ModelTierClassification` in `llms/runtime/llm_config.go`, configured with the usual `llm_tier_*_classification` keys.
  - **Resolution:** `classificationTier` probes `ResolveLLMConfig` with that tier and uses it only when `Source` is `env-tier`, `db-tier`, `context-override-tier` or `conversation-tier`. `TestClassificationTier_RealResolver` pins those labels.
  - **Other tier settings:** the thinking budget is the summary budget. Context size, the connection test and the model picker (`GetAllConfiguredModels`) cover the tier. The tier-layer `Source` values are the `llmConfigSource*Tier` constants.
- **Metrics:** `nb_llm_classification_decisions` (task, backend, valid, ok) and `nb_llm_classification_decision_latency` (task, backend).
- **Decision models:** `llms/classify/classify_decision.go` and the `llms/decision` client.
  - Keys: `llm_tier_provider_decision` (`cloudflare`, `typesafe` or `openai`), `llm_tier_model_decision` (`clef` / `clef-flash`, `jev-latest`, `gpt-6-luna`), `llm_tier_api_key_decision` (the provider's API key, encrypted), `llm_tier_api_endpoint_decision` (Cloudflare only: account id or a base URL; TypeSafe and OpenAI use fixed URLs and ignore it) and `llm_tier_min_confidence_decision` (default 0.9). The same keys work as ENV variables.
  - Models per provider: `decision.Models` in llm-server and `decisionModels` in api-server (kept in sync). An OpenAI refusal (`type: refusal`) is treated like a low-confidence answer: the LLM decides.
  - A decision token belongs to its provider: a save that changes `llm_tier_provider_decision` does not carry the stored token forward, so it must include a new one.
  - Decisions that use it and their default modes: `decisionModes` in llm-server; `decisionModeFor` applies the `llm_decision_mode_<task>` server setting. Shadow answers are logged as `classification_shadow` (task, `llm_labels`, `decision_answer`, `confidence`, `decision_model`, `would_use` (confident enough for `on`), `agree`, latency); the call is also recorded in token usage.
  - Resolution: `decisionConfigSource` uses `effectivePin`, the same function as `ResolveLLMConfig` (the fallback tier's per-task pick, then the request pin, then the conversation pin); else the account's integration; else ENV. An invalid or unavailable pin turns decisions off.
  - Validation (api-server): the model must be one of the provider's decision models, the endpoint a Cloudflare account id or an https URL, and a fallback reference to the decision slot is rejected. `parseConfigSourceId` rejects `tier:decision`; `GetLLMModel` refuses decision-only providers; api-server rejects them in every chat key.
  - Call: the client is wrapped by `wrapOutboundModel`, the same egress filter and scrub decorators as `GetLLMModel`, with a 2 s deadline. Confidence is the provider's, or the chosen option's probability. The call is recorded on the token-usage table with the provider, the configured model (not the version the provider reports, so the price row keeps matching; the version is in the decision log), `model_tier = decision` and the calling agent's name.
  - Decision fields: `Confidence`, `Probabilities`, `Model`, `FellBack` (asked, but the LLM answered), `DecisionAnswer` (the decision model's answer when the LLM answered) and `DecisionLatency` (its own call time when the LLM answered; `Latency` covers both). The `classification_decision` log adds `confidence`, `decision_model`, `fell_back`, `decision_answer` and `decision_latency_ms`.
- **Tests:**
  - `classify_test.go`: unit tests.
  - `classify_live_test.go`: opt-in; `CLASSIFY_LIVE_MODELS`, `CLASSIFY_LIVE_KEY` and `CLASSIFY_LIVE_ENDPOINT` select cheap models through the Google AI adapter.
  - `classify_e2e_test.go`: `//go:build e2e`, the production LLM path for `TEST_ACCOUNT`. Run it with `make test-e2e-one TEST=TestClassifyE2E PKG=./agents/core/`.
