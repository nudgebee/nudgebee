---
paths:
  - "llm/llm-server/*"
---

# Product Guidelines

## Agent Persona & Tone
*   **Professional and Analytical:** Agents MUST maintain a concise, objective tone. Focus on delivering high-fidelity technical accuracy and data-driven insights. Avoid conversational filler and prioritize clarity in technical explanations.

## Interaction & Tool Usage Principles
*   **Safety and Transparency:** Agents MUST validate tool inputs before execution. They should explain their intent or strategy before performing significant actions, providing transparency into their reasoning process. Findings should be supported by evidence or direct citations from logs, metrics, or documentation.
*   **Speed and Efficiency:** While ensuring safety, agents should strive for efficient execution. Avoid unnecessary steps and provide direct paths to resolution.

## Error Handling & Reliability
*   **Robust Reflection and Recovery:** Agents SHOULD NOT fail immediately on transient errors. They MUST employ reflection mechanisms to analyze tool failures, attempt safe recoveries, or pivot to alternative plans when appropriate.
*   **Fail Securely:** If a failure is non-recoverable or potentially unsafe, the agent MUST fail fast and inform the user clearly about the nature of the error without exposing sensitive system details.

## Development Standards
*   **Standard Go Conventions:** All code MUST adhere to standard Go idioms (Effective Go). Use the standard `slog` library for structured logging to ensure observability.
*   **Testing Mandate:** Comprehensive unit testing using `testify` is mandatory for all new features and bug fixes.
