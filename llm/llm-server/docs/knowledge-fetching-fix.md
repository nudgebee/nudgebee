# Knowledge fetching correctness

Part of #36421. Scope: fetching first, policy API/UI next, remaining P1/P2 later.
Tenant-owned KB migration remains P2.

Exact integration names search only their resolved collection. Query-dependent
results never enter the name cache. Manual vector hits resolve by KB ID to an
active account row and deduplicate against lexical search. Unknown names are
never substituted with semantically similar documents.

Large-document handling is specified in [knowledge-size-design.md](knowledge-size-design.md).
Discovery caches bounded excerpts and exact handles. With workspace reading
enabled, selection resolves the exact indexed document, saves large content to
the conversation workspace and provides bounded keyword/line reads through
`load_skills`. Small documents remain inline. The flag is disabled by default.

Acceptance: no large body in candidate caches or prompt menus; no cross-KB or
cross-account substitution; exact source citations; accessible final sections;
explicit errors for unavailable, changed, oversized or expired content. Indexed
content is never represented as a complete upstream article when ingestion may
have omitted sections.

Validation includes service checks, focused identity/size/access regressions and
a local HTTP flow through all three participating services. Dev CLI results
from the original sanity check establish deployed baseline behavior only.
