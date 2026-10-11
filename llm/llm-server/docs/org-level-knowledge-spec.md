# Organisation-wide SOPs and knowledge bases

A tenant admin writes an SOP once, and every account in the organisation can find and load it. An SOP can be limited to selected accounts, and each account can switch an organisation SOP off for itself. Account-owned knowledge bases work exactly as before.

Tracking issue: #40313. Decision record: `docs/architecture-decisions.md` ("Organisation-wide SOPs").

## Behaviour

1. **Default for a tenant admin's SOP:** a new SOP created by a tenant admin is organisation-wide by default.
2. **Who manages organisation KBs:** only `tenant_admin` can create, edit, widen or restrict one. An `account_admin` creates account KBs only.
3. **Restriction:** an account list. Labels never grant or hide access.
4. **Switch-off:** each account can switch an organisation KB off for itself.
5. **Name clash:** the account's own KB wins over the organisation KB.
6. **Existing KBs:** they stay account-owned. A tenant admin can widen one to the organisation, and turn an organisation KB back into an account KB by choosing the account.
7. **Timing:** restrict and switch-off apply to new searches and loads immediately.
8. **Tenant boundary:** a KB is never visible outside its tenant.
9. **Account admins:** they see the organisation KBs visible to their account read-only, marked "Organisation", and can switch one off for their account.

**Scope:** manual knowledge bases, and since #40605 integration knowledge bases (Confluence, ServiceNow): each integration has one organisation knowledge base, shared with every account by default; a tenant admin can restrict it to selected accounts, and an account admin can switch it off for their account. Its content comes from the integration, so only who can use it, the per-account switch and its documents' Fact/SOP marks are set by people. The integration's linked accounts still decide where credentials and scraping come from: the knowledge base exists while a Confluence integration is linked to at least one account, or a ServiceNow integration has knowledge sync on.

## Data model

### Ownership: `llm_knowledgebases`

- **`account_id`:** set means owned by that account. NULL means owned by the organisation (`tenant_id`).
- **`visibility`:** one of three values. `account` exactly when `account_id` is set.

| `visibility` | Owner | Who sees it |
|---|---|---|
| `account` | that account | that account |
| `organisation` | tenant | every account in the tenant, except accounts that switched it off |
| `selected_accounts` | tenant | only the accounts switched on |

- **Names:** account KB names are unique per account, and manual organisation KB names are unique per tenant. The same name may exist at both levels; the account KB wins for that account — except over an integration's knowledge base, whose name comes from the integration.
- **Integration rows** have no account: one organisation row per integration (unique on `integration_id`). The V1013 migration folded the earlier per-account rows into it; a per-account integration row is ignored if an older llm-server re-creates one during a rolling deploy.

### Per-account switches: `llm_kb_account_overrides`

- One row per `(kb_id, account_id)`, with `enabled`, `updated_by` and `updated_at`. It is used only for organisation KBs.
- **For `organisation`:** a row with `enabled = false` hides the KB from that account.
- **For `selected_accounts`:** a row with `enabled = true` shows the KB to that account.
- **Deletes:** rows are deleted with the KB, the account or the tenant.

### Fact/SOP marks: `llm_kb_capability_entries`

- **Manual KBs:** their Fact/SOP category is on the KB row (`note_category`) and stays with it when the KB is widened or moved back to an account.
- **Per-document marks** apply to integration KBs (Confluence, ServiceNow pages). A tenant admin sets a document's mark once for the tenant (`account_id` NULL, unique per `(tenant_id, document_key)`); setting it replaces every account's own mark on that document. An account-level mark, where one remains, overrides the tenant mark for that account, and the Documents list counts those accounts.

### Agent mappings

A manual organisation KB is used by all agents in every account that can see it, as if each of those accounts had mapped it to "All agents"; an account opts out with its switch. An integration's organisation KB is not: as before #40605, it reaches an agent's skill list and mapped search only through mappings the account made, and is otherwise found by the account-wide search. Mappings are keyed by `(kb_id, agent_id, account_id)`, so each account maps an organisation KB independently. Account KBs keep their per-account mappings.

## Visibility

**One rule decides what an account sees:**
- its own KBs; plus
- the organisation KBs of its tenant that pass the switches above;
- minus organisation KBs shadowed by an own manual KB with the same name. Names compare case-insensitively, and an own KB shadows even while switched off.

**The rule is implemented twice, and a shared fixture keeps the two copies identical:**
- **llm-server (Go): one helper used by every read.** That covers KB lists and gets, skill search and fetch, `load_skills` resolution, documents and PDF reads, the agents a KB is mapped to, and the knowledge pre-step (including reference attribution). Reads driven by agent mappings apply the rule too, so an organisation KB mapped to an agent drops out once it is restricted or switched off for that account. Full KB rows return `account_id` as an empty string for organisation KBs. Writes are owner-scoped: account KBs need account access, organisation KBs need `tenant_admin` of the KB's tenant.
- **rag-server (Python): the live-KB gate.** It applies the same rule to every search and exact document read, and shows nothing when the lookup fails. Organisation KB collections are indexed with the tenant as their scope, so the tenant boundary stays in rag-server.
- **The fixture:** one JSON file of cases (tenants, accounts, KBs, switches, expected visible KBs), run by both the Go and the Python test suites.

**Embeddings, labels and usage:** organisation KBs are embedded and Fact/SOP-classified with the LLM settings of an active account in the tenant, as integration KBs are. Their embedding usage is recorded in the shared (global) usage bucket, like the tenant's integration KBs.

**Changing a KB's scope updates its search collection's metadata in place.** No re-embedding is needed.
- **Widening to the organisation:** `account` is removed and `tenant_id` is set.
- **Changing it back to an account KB:** the reverse.
- **Order:** the KB row is updated first, then the collection is re-scoped. Narrowing is enforced by the live-KB gate from the row alone.
- **Cache:** rag-server's collection caches are cleared in the same step. Other rag-server replicas may keep the old scope until their collection cache expires, so a widened KB can take up to that long to appear for other accounts there.
- **Restricting and switch-off** need no metadata change: they are enforced by the live-KB gate.

## Cache freshness

- **Trigger:** a change to an organisation KB (create, edit, delete, widen, restrict, switch-off) sends a tenant-scoped message on the existing cache-invalidation channel. So does an account-KB create, rename or delete that changes a name clash.
- **Effect:** every llm-server replica clears its KB-mapping and skill-content caches.

## Permissions

| Action | Who |
|---|---|
| Read | Users with access to the account; visibility as above |
| Create, edit, delete, widen, restrict or move back to an account an organisation KB | `tenant_admin` of the KB's tenant |
| Switch an organisation-wide KB off or on for one account | `account_admin` of that account |
| Add an account to, or remove it from, a selected-accounts KB | `tenant_admin` of the KB's tenant |
| Read-only roles | Read only |

## API and UI

- **API:**
  - `ai_create_kb` accepts `visibility` (default `account`) and, for selected accounts, `account_ids`. An organisation KB is created without an `account_id`.
  - `ai_update_kb`, `ai_update_kb_enabled`, `ai_retrigger_kb` and `ai_delete_kb` act on an organisation KB when a tenant admin sends an empty `account_id`.
  - `ai_update_kb_visibility` changes scope: widen to the organisation, switch between organisation-wide and selected accounts (with `account_ids`), or move back to one account (with `target_account_id`).
  - `ai_update_kb_account_override` switches an organisation KB off or on for one account; `ai_list_kb_account_overrides` lists the switches of one KB.
  - `ai_list_kb` also returns the organisation KBs the account switched off, marked `switched_off`, so they can be switched back on. No other read returns them.
  - `ai_create_kb_mapping` accepts any organisation KB visible to the account.
  - KB names are trimmed. An organisation KB's name must be unique in its tenant, ignoring case.
- **UI:**
  - The KB form offers tenant admins "Who can use it": This account, Organisation, or Selected accounts (with an account picker). The tenant-wide view creates organisation KBs only.
  - In an account's list, an organisation KB shows an "Organisation" (or "Selected accounts") badge, is read-only for account admins, and its switch is that account's own on/off. Tenant admins can also edit it, delete it, or turn it off for all accounts.
  - In the tenant-wide list, an organisation KB's switch turns it off or on for every account.

There is no feature flag: once organisation KBs exist the change cannot be reverted, and with none, every path behaves as for account KBs alone.

## Known limits

- **If the cache-invalidation channel is down,** cached skill bodies and mappings expire normally after 15–30 minutes. Search and document reads are unaffected, because rag-server's gate is not cached.
- **Widening on a multi-replica rag-server** can take up to one collection-cache TTL to show on every replica (see Visibility). Narrowing is immediate, because the gate reads the database.
- **A conversation that already loaded an SOP** into its workspace keeps that copy until it ends.
