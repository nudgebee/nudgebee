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
- **Decision models (planned):** hosted models built for this job, which return a choice with probabilities and a confidence score. They are opt-in and server-configured. An answer below a configured confidence goes to the LLM instead. Multi-option decisions always use the LLM, because no decision-model API supports them.

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
backends:  LLM (default)  │  decision models (planned)
```

- The API changes rarely; callers depend only on it.
- Backends change when a model or vendor does, without touching callers.
- Callers move onto the API one at a time, each checked by replaying real messages, with the LLM backend as the default so behaviour changes only on purpose.
- Moving a decision to another backend is a configuration change: replay it offline against the candidate, enable it in one environment, watch the decision logs and metrics, then keep or revert.

### Telemetry

Every decision is logged as `classification_decision` (task, labels, valid, backend, latency, any error) and counted in metrics. LLM calls also keep their normal token-usage record. The token-usage `task_type` column is not reused, because it already holds the per-turn query/investigation classification.

## Implementation notes

- **API:** `core.Classify(ctx, ClassificationCall, ClassificationTask, input) → ClassificationDecision` in `agents/core/classify.go`.
  - `ClassificationTask` has `Name`, `Instructions`, `Options []ClassificationOption{Label, Description}` and `Multi`.
  - `ClassificationDecision` has `Labels`, `Valid`, `Raw`, `Backend` and `Latency`. Decision-model backends will add `Confidence`, `Probabilities` and `Model`.
  - `ClassificationCall` carries the ids that `GenerateAndTrackLLMContent` needs. `AgentId` is the calling agent's id, because it drives API-key resolution and cost attribution.
  - `Tier` picks the model tier; empty means the classification tier. `KeepCallerTier` instead runs on whatever tier the caller's context has, including none, for callers whose model must not change when they move onto the API (the tool-approval check).
- **LLM backend:** `classifyWithLLM` calls `GenerateAndTrackLLMContent` with `llms.WithTemperature(0)` and `WithThinkingLevel(ThinkingLevelFastTask)`.
  - It does not use structured enum output, because our provider adapters have no common option for it.
  - Production Gemini returned an exact label for 211 of 211 real workflow-intent messages.
- **Tier:** `ModelTierClassification` in `agents/core/llm_config.go`, configured with the usual `llm_tier_*_classification` keys.
  - **Resolution:** `classificationTier` probes `ResolveLLMConfig` with that tier and uses it only when `Source` is `env-tier`, `db-tier`, `context-override-tier` or `conversation-tier`. `TestClassificationTier_RealResolver` pins those labels.
  - **Other tier settings:** the thinking budget is the summary budget. Context size, the connection test and the model picker (`GetAllConfiguredModels`) cover the tier. The tier-layer `Source` values are the `llmConfigSource*Tier` constants.
- **Metrics:** `nb_llm_classification_decisions` (task, backend, valid, ok) and `nb_llm_classification_decision_latency` (task, backend).
- **Tests:**
  - `classify_test.go`: unit tests.
  - `classify_live_test.go`: opt-in; `CLASSIFY_LIVE_MODELS`, `CLASSIFY_LIVE_KEY` and `CLASSIFY_LIVE_ENDPOINT` select cheap models through the Google AI adapter.
  - `classify_e2e_test.go`: `//go:build e2e`, the production LLM path for `TEST_ACCOUNT`. Run it with `make test-e2e-one TEST=TestClassifyE2E PKG=./agents/core/`.
