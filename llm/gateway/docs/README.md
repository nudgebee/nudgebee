# NB AI Gateway — Docs

Service-specific design docs for `nudgebee/llm-gateway`. The cross-service decision
log lives in the repo root `docs/architecture-decisions.md`; these are the gateway's
own deep dives.

- [architecture.md](architecture.md) — what the gateway is, topology, why embed
  `bifrost-core`, request lifecycle, the seam, data model, phasing (P1/P2/P3).
- [routing.md](routing.md) — routing design: landscape, P1 rule engine, workload
  hints (NB tier edge), fidelity constraint, cache affinity, explainable-routing
  decision capture.

Status legend used throughout: ✅ built + verified · 🟡 designed, not built ·
⬜ future phase.
