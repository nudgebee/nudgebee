# Gemini guidance — see CLAUDE.md

This file used to mirror [`CLAUDE.md`](./CLAUDE.md). Maintaining two long copies of the same document caused drift — they were updated independently and disagreed on conventions, decisions, and the architecture-decisions log.

**Gemini agents (and any other AI tooling): read [`CLAUDE.md`](./CLAUDE.md).** It is the single source of truth for:

- Module structure + the 14-service inventory
- AI Coding Principles (adversarial pre-implementation pass, AI first-pass review, parallel-session patterns)
- Architecture Decisions / "Living Constitution" log
- Build commands per service type
- Database migrations + RPC action naming convention
- The "for human contributors" callout at the top — points at the sections worth a human reader's time

If your harness needs a Gemini-specific entrypoint, configure it to load `CLAUDE.md`. The content is agent-agnostic — every principle and convention applies regardless of which model is driving.
